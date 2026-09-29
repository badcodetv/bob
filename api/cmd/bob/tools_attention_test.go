package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/badcodetv/bob/internal/access"
	"github.com/badcodetv/bob/internal/auth"
	"github.com/badcodetv/bob/internal/store"
)

func attentionApp(t *testing.T) (a *app, humanToken, workerToken string, human store.Session) {
	t.Helper()
	a, humanToken, workerToken, _, _ = memoryToolsApp(t)
	a.mcp.Register(a.attentionTools()...)
	id, _ := verifyMCPToken(a.auth.Secret, humanToken)
	human, err := a.store.Session(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return a, humanToken, workerToken, human
}

func TestRequestHumanAttentionStoresAndPostsTheWebhook(t *testing.T) {
	a, _, workerTok, _ := attentionApp(t)
	got := make(chan map[string]string, 1)
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var m map[string]string
		json.Unmarshal(b, &m)
		got <- m
	}))
	defer hook.Close()
	a.attentionWebhook = func(project string) string {
		if project == "wolf" {
			return hook.URL
		}
		return ""
	}

	status, isErr, text := callTool(t, a, workerTok, "request_human_attention", `{"message":"may I post it?"}`)
	if status != 200 || isErr {
		t.Fatalf("call = %d isError=%v %s", status, isErr, text)
	}
	var res struct {
		ID      string `json:"id"`
		ChatURL string `json:"chat_url"`
		Note    string `json:"note"`
	}
	if err := json.Unmarshal([]byte(text), &res); err != nil || res.ID == "" {
		t.Fatalf("result = %s, %v", text, err)
	}
	if !strings.Contains(res.Note, "End your turn") {
		t.Errorf("note = %q", res.Note)
	}
	x, err := a.store.Attention(t.Context(), res.ID)
	if err != nil || x.Project != "wolf" || x.Worker != "researcher" || x.Kind != "ask" || x.Message != "may I post it?" || x.ClosedAt != nil {
		t.Fatalf("row = %+v, %v", x, err)
	}
	if want := "https://bob.example/#/p/wolf/s/" + x.SessionID; res.ChatURL != want {
		t.Errorf("chat_url = %q, want %q", res.ChatURL, want)
	}
	a.webhooks.Wait()
	select {
	case m := <-got:
		want := map[string]string{"project": "wolf", "worker": "researcher", "kind": "ask", "message": "may I post it?", "chat_url": res.ChatURL}
		if len(m) != len(want) {
			t.Errorf("webhook body = %v", m)
		}
		for k, v := range want {
			if m[k] != v {
				t.Errorf("webhook %s = %q, want %q", k, m[k], v)
			}
		}
	default:
		t.Fatal("the webhook was not called")
	}
}

func TestRequestHumanAttentionNoticeAndValidation(t *testing.T) {
	a, human, _, _ := attentionApp(t)
	a.attentionWebhook = func(string) string { return "http://127.0.0.1:1/unreachable" } // failure is only logged
	status, isErr, text := callTool(t, a, human, "request_human_attention", `{"message":"posted it","notice":true}`)
	if status != 200 || isErr {
		t.Fatalf("notice = %d %v %s", status, isErr, text)
	}
	a.webhooks.Wait()
	xs, _ := a.store.Attentions(t.Context(), "wolf", true)
	if len(xs) != 1 || xs[0].Kind != "notice" || xs[0].Worker != "" {
		t.Errorf("rows = %+v", xs)
	}
	if _, isErr, _ := callTool(t, a, human, "request_human_attention", `{"message":"  "}`); !isErr {
		t.Error("a blank message was accepted")
	}
	if _, isErr, _ := callTool(t, a, human, "request_human_attention", `{}`); !isErr {
		t.Error("a missing message was accepted")
	}
}

func TestAttentionWebhookVar(t *testing.T) {
	if got := attentionWebhookVar("marketing-team"); got != "BOB_ATTENTION_WEBHOOK_MARKETING_TEAM" {
		t.Errorf("got %s", got)
	}
}

// A person's message closes the chat's open requests as answered; a schedule's does not.
func TestAPersonsReplyClosesAttention(t *testing.T) {
	a, _, _, chat := attentionApp(t)
	x, _ := a.store.CreateAttention(t.Context(), chat.ID, "", "ok?", "ask")

	_, _, err := a.startTurn(t.Context(), chat, auth.User{Email: "schedule:abc"}, "tick")
	if err != nil {
		t.Fatal(err)
	}
	a.endTurn(chat.ID)
	if got, _ := a.store.Attention(t.Context(), x.ID); got.ClosedAt != nil {
		t.Fatalf("a scheduled message closed it: %+v", got)
	}

	if _, _, err := a.startTurn(t.Context(), chat, auth.User{Email: "kai@example.com"}, "yes, post it"); err != nil {
		t.Fatal(err)
	}
	a.endTurn(chat.ID)
	got, _ := a.store.Attention(t.Context(), x.ID)
	if got.ClosedAt == nil || got.CloseReason != "answered" || got.ClosedBy != "kai@example.com" {
		t.Errorf("after a reply: %+v", got)
	}
}

func TestAttentionHTTP(t *testing.T) {
	a, _, _, chat := attentionApp(t)
	open, _ := a.store.CreateAttention(t.Context(), chat.ID, "", "look", "ask")
	done, _ := a.store.CreateAttention(t.Context(), chat.ID, "", "old", "ask")
	a.store.CloseAttention(t.Context(), done.ID, "x@example.com", "dismissed")

	people := access.Map{"tester@example.com": {"wolf"}, "other@example.com": {"dev"}}
	a.auth, a.access = &auth.Auth{Secret: a.auth.Secret, Allowed: people.Allowed}, people
	a.projectOf = a.storeProjectOf
	do := func(email, method, path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		a.auth.SetSession(rec, auth.User{Email: email})
		req := httptest.NewRequest(method, path, nil)
		req.AddCookie(rec.Result().Cookies()[0])
		res := httptest.NewRecorder()
		a.routes().ServeHTTP(res, req)
		return res
	}
	list := func(q string) []attentionView {
		res := do("tester@example.com", "GET", "/api/projects/wolf/attention"+q)
		if res.Code != 200 {
			t.Fatalf("list = %d %s", res.Code, res.Body)
		}
		var out struct{ Attention []attentionView }
		json.Unmarshal(res.Body.Bytes(), &out)
		return out.Attention
	}
	if xs := list("?state=open"); len(xs) != 1 || xs[0].ID != open.ID || xs[0].ChatURL == "" {
		t.Errorf("open = %+v", xs)
	}
	if xs := list(""); len(xs) != 2 {
		t.Errorf("all = %+v", xs)
	}
	if res := do("other@example.com", "POST", "/api/attention/"+open.ID+"/dismiss"); res.Code != 404 {
		t.Errorf("another project's member dismissing = %d, want 404", res.Code)
	}
	if got, _ := a.store.Attention(t.Context(), open.ID); got.ClosedAt != nil {
		t.Fatal("a non-member closed it")
	}
	if res := do("tester@example.com", "POST", "/api/attention/"+open.ID+"/dismiss"); res.Code != 200 {
		t.Fatalf("dismiss = %d %s", res.Code, res.Body)
	}
	got, _ := a.store.Attention(t.Context(), open.ID)
	if got.ClosedAt == nil || got.CloseReason != "dismissed" || got.ClosedBy != "tester@example.com" {
		t.Errorf("after dismiss: %+v", got)
	}
	if xs := list("?state=open"); len(xs) != 0 {
		t.Errorf("open after dismiss = %+v", xs)
	}
}

func TestAttentionToolSchemasAreValidJSON(t *testing.T) {
	a := &app{}
	for _, tool := range a.attentionTools() {
		if !json.Valid(tool.InputSchema) {
			t.Errorf("%s: input schema is not valid JSON: %s", tool.Name, tool.InputSchema)
		}
	}
}
