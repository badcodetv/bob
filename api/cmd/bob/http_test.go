package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/badcodetv/bob/internal/access"
	"github.com/badcodetv/bob/internal/auth"
)

// A turn carries everything the runtime runs it with, read from the database: the worker's engine,
// tools and prompt, and the chat's model and effort where it overrides the worker's.
func TestTurnCarriesTheWorkersSettings(t *testing.T) {
	a, rt, _ := newScheduleApp(t)
	id := researcherID[a]
	execSQL(t, a.store, `UPDATE workers SET model = 'opus', effort = 'high', tools = '["Read", "Bash(git:*)"]',
		prompt = 'Research the market.' WHERE id = $1`, id)

	own, err := a.store.CreateSession(t.Context(), "wolf", &id, "researcher", "claude", "", "")
	if err != nil {
		t.Fatal(err)
	}
	overridden, err := a.store.CreateSession(t.Context(), "wolf", &id, "researcher", "claude", "sonnet", "low")
	if err != nil {
		t.Fatal(err)
	}
	kai := auth.User{Email: "kai@example.com", Name: "Kai"}
	for _, sess := range []string{own.ID, overridden.ID} {
		s, _ := a.store.Session(t.Context(), sess)
		if err := a.runTurn(t.Context(), s, kai, "hello"); err != nil {
			t.Fatal(err)
		}
	}
	turns, _ := rt.sent()
	if len(turns) != 2 {
		t.Fatalf("turns = %+v", turns)
	}
	got := func(i int) string {
		x := turns[i]
		return fmt.Sprintf("%s %s %s/%s %q %q %q %s %s <%s>", x.SessionID, x.Engine, x.Model, x.Effort, x.Tools, x.SystemPrompt, x.MCPToken, x.Text, x.UserName, x.UserEmail)
	}
	prompt := fmt.Sprintf("%q", composePrompt(bobNote("wolf"), "", "Research the market."))
	if want := fmt.Sprintf(`%s claude opus/high ["Read" "Bash(git:*)"] %s %q hello Kai <kai@example.com>`, own.ID, prompt, mcpToken(a.auth.Secret, own.ID)); got(0) != want {
		t.Errorf("turn on the worker's settings:\n got %s\nwant %s", got(0), want)
	}
	if want := fmt.Sprintf(`%s claude sonnet/low ["Read" "Bash(git:*)"] %s %q hello Kai <kai@example.com>`, overridden.ID, prompt, mcpToken(a.auth.Secret, overridden.ID)); got(1) != want {
		t.Errorf("turn with the chat's overrides:\n got %s\nwant %s", got(1), want)
	}
	if turns[1].Resume != "" {
		t.Errorf("a first turn resumes nothing, got %q", turns[1].Resume)
	}
}

// A chat whose worker has been deleted is not run: there is no prompt or settings left to run it
// with. The failure is recorded on the chat, and the runtime never hears of it.
func TestTurnOnADeletedWorkerFails(t *testing.T) {
	a, rt, _ := newScheduleApp(t)
	id := researcherID[a]
	sess, err := a.store.CreateSession(t.Context(), "wolf", &id, "researcher", "claude", "", "")
	if err != nil {
		t.Fatal(err)
	}
	execSQL(t, a.store, `DELETE FROM workers WHERE id = $1`, id)
	sess, _ = a.store.Session(t.Context(), sess.ID)
	err = a.runTurn(t.Context(), sess, auth.User{Email: "kai@example.com"}, "hello")
	if err == nil || !strings.Contains(err.Error(), "this chat's worker was deleted") {
		t.Errorf("err = %v, want the deleted-worker refusal", err)
	}
	if turns, _ := rt.sent(); len(turns) != 0 {
		t.Errorf("runtime was sent %+v", turns)
	}
	events, _ := a.store.Events(t.Context(), sess.ID, 0)
	if len(events) != 1 || events[0].Kind != "bob.turn_failed" {
		t.Errorf("events = %+v, want one bob.turn_failed", events)
	}
}

// A chat is started on a worker named in the database, and points at that worker's row.
func TestCreateSessionFindsTheWorkerInTheDatabase(t *testing.T) {
	a, rt, _ := newScheduleApp(t)
	people := access.Map{"tester@example.com": {"wolf"}}
	a.auth, a.access = &auth.Auth{Secret: []byte("0123456789abcdef"), Allowed: people.Allowed}, people
	a.projectOf = a.storeProjectOf
	rec := httptest.NewRecorder()
	a.auth.SetSession(rec, auth.User{Email: "tester@example.com"})
	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/api/projects/wolf/sessions", strings.NewReader(body))
		req.AddCookie(rec.Result().Cookies()[0])
		res := httptest.NewRecorder()
		a.routes().ServeHTTP(res, req)
		return res
	}
	res := post(`{"worker":"researcher","model":"sonnet"}`)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"worker_id":"`+researcherID[a]+`"`) ||
		!strings.Contains(res.Body.String(), `"engine":"claude"`) {
		t.Errorf("create = %d %s, want the researcher's id and engine", res.Code, res.Body)
	}
	if res := post(`{"worker":"nobody"}`); res.Code != http.StatusBadRequest {
		t.Errorf("unknown worker = %d %s, want 400", res.Code, res.Body)
	}
	if _, unexpected := rt.sent(); len(unexpected) != 0 {
		t.Errorf("runtime was asked for %v; workers are Bob's, not the runtime's", unexpected)
	}
}
