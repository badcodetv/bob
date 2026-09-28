package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/badcodetv/bob/internal/access"
	"github.com/badcodetv/bob/internal/auth"
	"github.com/badcodetv/bob/internal/store"
)

// authedApp wires a's auth and access so tester@example.com is a member of wolf and admin@example.com
// is an admin, and returns cookies for both plus a do() helper that runs a request through the
// full router.
func authedApp(t *testing.T, a *app) (testerCookie, adminCookie *http.Cookie, do func(who *http.Cookie, method, path, body string) *httptest.ResponseRecorder) {
	t.Helper()
	people := access.Map{"tester@example.com": {"wolf"}, "admin@example.com": {"*"}}
	a.auth, a.access = &auth.Auth{Secret: []byte("0123456789abcdef"), Allowed: people.Allowed}, people
	a.projectOf = a.storeProjectOf
	cookieFor := func(email string) *http.Cookie {
		rec := httptest.NewRecorder()
		a.auth.SetSession(rec, auth.User{Email: email})
		return rec.Result().Cookies()[0]
	}
	testerCookie, adminCookie = cookieFor("tester@example.com"), cookieFor("admin@example.com")
	do = func(who *http.Cookie, method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if who != nil {
			req.AddCookie(who)
		}
		res := httptest.NewRecorder()
		a.routes().ServeHTTP(res, req)
		return res
	}
	return
}

// A worker can be created, updated and deleted, each recording a why; the change shows up in
// its version history.
func TestWorkerCRUDAndVersions(t *testing.T) {
	a, _, _ := newScheduleApp(t)
	tester, _, do := authedApp(t, a)

	res := do(tester, "POST", "/api/projects/wolf/workers", `{"name":"poet","engine":"claude","prompt":"write haiku","why":"new"}`)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"name":"poet"`) {
		t.Fatalf("create = %d %s", res.Code, res.Body)
	}

	res = do(tester, "PATCH", "/api/projects/wolf/workers/poet", `{"engine":"claude","prompt":"write limericks","why":"change style"}`)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"prompt":"write limericks"`) {
		t.Fatalf("update = %d %s", res.Code, res.Body)
	}

	res = do(tester, "GET", "/api/projects/wolf/workers/poet/versions", "")
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"action":"update"`) || !strings.Contains(res.Body.String(), `"action":"create"`) {
		t.Fatalf("versions = %d %s", res.Code, res.Body)
	}

	res = do(tester, "GET", "/api/projects/wolf/workers", "")
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"name":"poet"`) {
		t.Fatalf("list = %d %s", res.Code, res.Body)
	}

	res = do(tester, "DELETE", "/api/projects/wolf/workers/poet?why=cleanup", "")
	if res.Code != http.StatusOK {
		t.Fatalf("delete = %d %s", res.Code, res.Body)
	}
	res = do(tester, "GET", "/api/projects/wolf/workers", "")
	if res.Code != http.StatusOK || strings.Contains(res.Body.String(), `"name":"poet"`) {
		t.Fatalf("list after delete = %d %s, want poet gone", res.Code, res.Body)
	}
}

// Labels round-trip through create/update, and GET .../workers?selector= filters the list;
// an invalid selector is a 400 with a readable message.
func TestWorkerLabelsAndSelector(t *testing.T) {
	a, _, _ := newScheduleApp(t)
	tester, _, do := authedApp(t, a)

	res := do(tester, "POST", "/api/projects/wolf/workers", `{"name":"scout","engine":"claude","prompt":"x","labels":{"kind":"hypothesis"},"why":"new"}`)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"kind":"hypothesis"`) {
		t.Fatalf("create with labels = %d %s", res.Code, res.Body)
	}
	res = do(tester, "POST", "/api/projects/wolf/workers", `{"name":"other","engine":"claude","prompt":"x","labels":{"kind":"scraper"},"why":"new"}`)
	if res.Code != http.StatusOK {
		t.Fatalf("create other = %d %s", res.Code, res.Body)
	}

	res = do(tester, "GET", "/api/projects/wolf/workers?selector=kind%3Dhypothesis", "")
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"name":"scout"`) || strings.Contains(res.Body.String(), `"name":"other"`) {
		t.Fatalf("list by selector = %d %s", res.Code, res.Body)
	}

	res = do(tester, "GET", "/api/projects/wolf/workers?selector=kind+in+(a", "")
	if res.Code != http.StatusBadRequest {
		t.Fatalf("invalid selector = %d %s, want 400", res.Code, res.Body)
	}

	res = do(tester, "PATCH", "/api/projects/wolf/workers/scout", `{"engine":"claude","prompt":"x","labels":{"kind":"retired"},"why":"relabel"}`)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"kind":"retired"`) {
		t.Fatalf("update labels = %d %s", res.Code, res.Body)
	}

	res = do(tester, "POST", "/api/projects/wolf/workers", `{"name":"bad","engine":"claude","prompt":"x","labels":{"bob.reserved":"v"},"why":"new"}`)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("reserved label = %d %s, want 400", res.Code, res.Body)
	}
}

// A duplicate worker name in the same project is a conflict, not a generic error.
func TestCreateWorkerDuplicateIsConflict(t *testing.T) {
	a, _, _ := newScheduleApp(t)
	tester, _, do := authedApp(t, a)
	do(tester, "POST", "/api/projects/wolf/workers", `{"name":"poet","engine":"claude","prompt":"x","why":"new"}`)
	res := do(tester, "POST", "/api/projects/wolf/workers", `{"name":"poet","engine":"claude","prompt":"y","why":"again"}`)
	if res.Code != http.StatusConflict {
		t.Errorf("duplicate create = %d %s, want 409", res.Code, res.Body)
	}
}

// Validation: engine, effort per engine, model, and tools only for claude.
func TestWorkerValidation(t *testing.T) {
	a, _, _ := newScheduleApp(t)
	tester, _, do := authedApp(t, a)
	cases := []struct {
		name, body string
	}{
		{"bad engine", `{"name":"a","engine":"gpt4","prompt":"x","why":"w"}`},
		{"bad name", `{"name":"BAD NAME","engine":"claude","prompt":"x","why":"w"}`},
		{"claude bad effort", `{"name":"a","engine":"claude","effort":"minimal","prompt":"x","why":"w"}`},
		{"codex bad effort", `{"name":"a","engine":"codex","effort":"max","prompt":"x","why":"w"}`},
		{"tools on codex", `{"name":"a","engine":"codex","tools":["Read"],"prompt":"x","why":"w"}`},
		{"bad model", `{"name":"a","engine":"claude","model":"not a model!","prompt":"x","why":"w"}`},
	}
	for _, c := range cases {
		res := do(tester, "POST", "/api/projects/wolf/workers", c.body)
		if res.Code != http.StatusBadRequest {
			t.Errorf("%s: create = %d %s, want 400", c.name, res.Code, res.Body)
		}
	}
	// Accepted: codex effort minimal, claude effort max, claude tools.
	res := do(tester, "POST", "/api/projects/wolf/workers", `{"name":"codex-worker","engine":"codex","effort":"minimal","prompt":"x","why":"w"}`)
	if res.Code != http.StatusOK {
		t.Errorf("codex minimal effort = %d %s, want 200", res.Code, res.Body)
	}
	res = do(tester, "POST", "/api/projects/wolf/workers", `{"name":"claude-worker","engine":"claude","effort":"max","tools":["Read"],"prompt":"x","why":"w"}`)
	if res.Code != http.StatusOK {
		t.Errorf("claude max effort + tools = %d %s, want 200", res.Code, res.Body)
	}
}

// The project prompt can be read and changed, with history.
func TestProjectPrompt(t *testing.T) {
	a, _, _ := newScheduleApp(t)
	tester, _, do := authedApp(t, a)
	res := do(tester, "PUT", "/api/projects/wolf/prompt", `{"prompt":"be concise","why":"set tone"}`)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"prompt":"be concise"`) {
		t.Fatalf("put = %d %s", res.Code, res.Body)
	}
	res = do(tester, "GET", "/api/projects/wolf/prompt", "")
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"prompt":"be concise"`) || !strings.Contains(res.Body.String(), `"why":"set tone"`) {
		t.Fatalf("get = %d %s", res.Code, res.Body)
	}
}

// A plain chat (worker "") requires an engine; a codex plain chat accepts effort "minimal".
func TestPlainChatRequiresEngine(t *testing.T) {
	a, _, _ := newScheduleApp(t)
	tester, _, do := authedApp(t, a)
	res := do(tester, "POST", "/api/projects/wolf/sessions", `{"worker":""}`)
	if res.Code != http.StatusBadRequest {
		t.Errorf("plain chat without engine = %d %s, want 400", res.Code, res.Body)
	}
	res = do(tester, "POST", "/api/projects/wolf/sessions", `{"worker":"","engine":"codex","effort":"minimal"}`)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"engine":"codex"`) || !strings.Contains(res.Body.String(), `"worker":""`) {
		t.Errorf("codex plain chat with minimal effort = %d %s", res.Code, res.Body)
	}
}

// A chat whose worker's engine has changed since the chat was created refuses new messages
// without calling the runtime.
func TestTurnRefusedWhenWorkerEngineChanged(t *testing.T) {
	a, rt, _ := newScheduleApp(t)
	id := researcherID[a] // created with engine "claude"
	sess, err := a.store.CreateSession(t.Context(), "wolf", &id, "researcher", "claude", "", "")
	if err != nil {
		t.Fatal(err)
	}
	execSQL(t, a.store, `UPDATE workers SET engine = 'codex' WHERE id = $1`, id)
	sess, _ = a.store.Session(t.Context(), sess.ID)
	err = a.runTurn(t.Context(), sess, auth.User{Email: "kai@example.com"}, "hello")
	if err == nil || !strings.Contains(err.Error(), "now runs on codex") {
		t.Errorf("err = %v, want an engine-changed refusal", err)
	}
	if turns, _ := rt.sent(); len(turns) != 0 {
		t.Errorf("runtime was sent %+v", turns)
	}
	events, _ := a.store.Events(t.Context(), sess.ID, 0)
	if len(events) != 1 || events[0].Kind != "bob.turn_failed" {
		t.Errorf("events = %+v, want one bob.turn_failed", events)
	}
}

// Schedule create/update accept a worker name and resolve it to worker_id; an unknown name is 400.
func TestScheduleResolvesWorkerName(t *testing.T) {
	a, _, _ := newScheduleApp(t)
	_, admin, do := authedApp(t, a)

	res := do(admin, "POST", "/api/projects/wolf/schedules", `{"name":"daily","worker":"researcher","cron":"0 6 * * *","message":"go"}`)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"worker":"researcher"`) {
		t.Fatalf("create = %d %s", res.Code, res.Body)
	}
	var created struct{ ID string }
	if err := json.Unmarshal(res.Body.Bytes(), &created); err != nil || created.ID == "" {
		t.Fatalf("create response: %v %s", err, res.Body)
	}

	res = do(admin, "POST", "/api/projects/wolf/schedules", `{"name":"bad","worker":"nobody","cron":"0 6 * * *","message":"go"}`)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("unknown worker on create = %d %s, want 400", res.Code, res.Body)
	}

	// A new worker for the update, and an update to the unknown worker.
	poet, err := a.store.CreateWorker(t.Context(), store.Worker{Project: "wolf", Name: "poet", Engine: "claude"}, "kai", "new")
	if err != nil {
		t.Fatal(err)
	}
	res = do(admin, "PATCH", "/api/schedules/"+created.ID, `{"worker":"poet"}`)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"worker_id":"`+poet.ID+`"`) {
		t.Fatalf("update to poet = %d %s", res.Code, res.Body)
	}
	res = do(admin, "PATCH", "/api/schedules/"+created.ID, `{"worker":"nobody"}`)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("unknown worker on update = %d %s, want 400", res.Code, res.Body)
	}
}
