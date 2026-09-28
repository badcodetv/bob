package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/badcodetv/bob/internal/auth"
	"github.com/badcodetv/bob/internal/mcp"
	"github.com/badcodetv/bob/internal/store"
)

func TestMCPToken(t *testing.T) {
	secret := []byte("0123456789abcdef")
	const id = "9b2f7c1e-8a4d-4f0b-9c3e-1d2a3b4c5d6e"
	tok := mcpToken(secret, id)
	if !strings.HasPrefix(tok, id+".") || len(tok) != len(id)+1+64 {
		t.Fatalf("token = %q", tok)
	}
	if got, ok := verifyMCPToken(secret, tok); !ok || got != id {
		t.Errorf("verify(own token) = %q, %v", got, ok)
	}
	other := mcpToken(secret, "0b2f7c1e-8a4d-4f0b-9c3e-1d2a3b4c5d6e")
	flipped := []byte(tok)
	flipped[len(flipped)-1] ^= 1
	for name, bad := range map[string]string{
		"empty":            "",
		"no signature":     id,
		"empty signature":  id + ".",
		"tampered":         string(flipped),
		"another's sig":    id + other[strings.IndexByte(other, '.'):],
		"another secret":   mcpToken([]byte("fedcba9876543210"), id),
		"no session":       tok[len(id):],
		"uppercase hex":    id + "." + strings.ToUpper(tok[len(id)+1:]),
		"trailing garbage": tok + "x",
	} {
		if got, ok := verifyMCPToken(secret, bad); ok {
			t.Errorf("%s: verified as %q", name, got)
		}
	}
}

// whoami is a test tool that returns the Caller it was called as.
var whoami = mcp.Tool{Name: "whoami", Call: func(_ context.Context, c mcp.Caller, _ json.RawMessage) (any, error) { return c, nil }}

// callWhoami calls whoami over POST /mcp through the app's routes, with Authorization "Bearer
// <token>" (none when token is ""), and returns the status and, on 200, the Caller.
func callWhoami(t *testing.T, a *app, token string) (int, mcp.Caller) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"whoami"}}`))
	req.Host = "api:8070"
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res := httptest.NewRecorder()
	a.routes().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		return res.Code, mcp.Caller{}
	}
	var r struct {
		Result struct{ Content []struct{ Text string } }
	}
	json.Unmarshal(res.Body.Bytes(), &r)
	var c mcp.Caller
	if len(r.Result.Content) != 1 || json.Unmarshal([]byte(r.Result.Content[0].Text), &c) != nil {
		t.Fatalf("whoami: %s", res.Body)
	}
	return res.Code, c
}

func userMessage(t *testing.T, a *app, sess store.Session, email string) {
	t.Helper()
	payload, _ := json.Marshal(map[string]string{"text": "hi", "user_email": email})
	if _, err := a.store.AppendEvent(t.Context(), store.Event{SessionID: sess.ID, Engine: sess.Engine, Kind: "bob.user_message", Payload: payload}); err != nil {
		t.Fatal(err)
	}
}

// /mcp takes no cookie: the chat's MCP token says which chat, and so which project, is calling.
func TestMCPCaller(t *testing.T) {
	a, _, _ := newScheduleApp(t)
	a.mcp = mcp.New()
	a.mcp.Register(whoami)
	id := researcherID[a]
	worker, err := a.store.CreateSession(t.Context(), "wolf", &id, "researcher", "claude", "", "")
	if err != nil {
		t.Fatal(err)
	}
	plain, err := a.store.CreateSession(t.Context(), "wolf", nil, "", "codex", "", "")
	if err != nil {
		t.Fatal(err)
	}
	userMessage(t, a, worker, "kai@example.com")
	userMessage(t, a, worker, "tester@example.com") // the latest message is who the tool acts for
	userMessage(t, a, plain, "schedule:0b2f7c1e-8a4d-4f0b-9c3e-1d2a3b4c5d6e")

	code, c := callWhoami(t, a, mcpToken(a.auth.Secret, worker.ID))
	if want := (mcp.Caller{Project: "wolf", SessionID: worker.ID, Worker: "researcher", User: "tester@example.com", APIBase: "http://api:8070"}); code != 200 || c != want {
		t.Errorf("worker chat: %d %+v\nwant %+v", code, c, want)
	}
	code, c = callWhoami(t, a, mcpToken(a.auth.Secret, plain.ID))
	if want := (mcp.Caller{Project: "wolf", SessionID: plain.ID, User: "schedule:0b2f7c1e-8a4d-4f0b-9c3e-1d2a3b4c5d6e", APIBase: "http://api:8070"}); code != 200 || c != want {
		t.Errorf("plain chat: %d %+v\nwant %+v", code, c, want)
	}
}

func TestMCPRefusesBadTokens(t *testing.T) {
	a, _, _ := newScheduleApp(t)
	a.mcp = mcp.New()
	a.mcp.Register(whoami)
	sess, err := a.store.CreateSession(t.Context(), "wolf", nil, "", "claude", "", "")
	if err != nil {
		t.Fatal(err)
	}
	good := mcpToken(a.auth.Secret, sess.ID)
	for name, tok := range map[string]string{
		"no token":       "",
		"garbage":        "garbage",
		"tampered":       good[:len(good)-1] + "0",
		"another secret": mcpToken([]byte("fedcba9876543210"), sess.ID),
	} {
		if tok == good {
			tok = good[:len(good)-1] + "1"
		}
		if code, _ := callWhoami(t, a, tok); code != http.StatusUnauthorized {
			t.Errorf("%s: status %d, want 401", name, code)
		}
	}
	// A signed-in person's cookie is no MCP token.
	_, admin, _ := authedApp(t, a)
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	req.AddCookie(admin)
	res := httptest.NewRecorder()
	a.routes().ServeHTTP(res, req)
	if res.Code != http.StatusUnauthorized {
		t.Errorf("cookie only: status %d, want 401", res.Code)
	}

	if err := a.store.DeleteSession(t.Context(), sess.ID); err != nil {
		t.Fatal(err)
	}
	if code, _ := callWhoami(t, a, good); code != http.StatusNotFound {
		t.Errorf("deleted chat: status %d, want 404", code)
	}
}

// Each turn carries its chat's MCP token, so the agent can call back into Bob as that chat.
func TestTurnCarriesTheMCPToken(t *testing.T) {
	a, rt, _ := newScheduleApp(t)
	sess, err := a.store.CreateSession(t.Context(), "wolf", nil, "", "claude", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.runTurn(t.Context(), sess, auth.User{Email: "kai@example.com"}, "hello"); err != nil {
		t.Fatal(err)
	}
	turns, _ := rt.sent()
	if len(turns) != 1 {
		t.Fatalf("turns = %+v", turns)
	}
	if got, ok := verifyMCPToken(a.auth.Secret, turns[0].MCPToken); !ok || got != sess.ID {
		t.Errorf("mcp_token = %q (verifies as %q, %v)", turns[0].MCPToken, got, ok)
	}
}
