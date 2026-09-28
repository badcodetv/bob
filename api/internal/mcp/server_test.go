package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestServer serves two tools: echo (always there) and secret (only for worker "wolfie"), plus
// fails, whose call errors. Its auth lets "Bearer good" in as worker "wolfie" and "Bearer plain" in
// as a plain chat; anything else is refused.
func newTestServer(t *testing.T) http.Handler {
	t.Helper()
	s := New()
	s.Register(
		Tool{Name: "echo", Description: "Echoes its arguments and who called.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"say":{"type":"string"}}}`),
			Call: func(_ context.Context, c Caller, args json.RawMessage) (any, error) {
				var a struct{ Say string }
				json.Unmarshal(args, &a)
				return map[string]string{"said": a.Say, "project": c.Project, "session": c.SessionID}, nil
			}},
		Tool{Name: "secret", Description: "Only for wolfie.",
			Available: func(c Caller) bool { return c.Worker == "wolfie" },
			Call:      func(context.Context, Caller, json.RawMessage) (any, error) { return "hush", nil }},
		Tool{Name: "fails", Description: "Always fails.",
			Call: func(context.Context, Caller, json.RawMessage) (any, error) { return nil, errors.New("no such worker") }},
	)
	return s.Handler(func(r *http.Request) (Caller, error) {
		switch r.Header.Get("Authorization") {
		case "Bearer good":
			return Caller{Project: "wolf", SessionID: "s1", Worker: "wolfie"}, nil
		case "Bearer plain":
			return Caller{Project: "wolf", SessionID: "s2"}, nil
		case "Bearer gone":
			return Caller{}, ErrNoSession
		}
		return Caller{}, ErrUnauthorized
	})
}

func post(t *testing.T, h http.Handler, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	return res
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func rpc(t *testing.T, h http.Handler, token, body string) response {
	t.Helper()
	res := post(t, h, token, body)
	if res.Code != http.StatusOK {
		t.Fatalf("status %d: %s", res.Code, res.Body)
	}
	if ct := res.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	var r response
	if err := json.Unmarshal(res.Body.Bytes(), &r); err != nil {
		t.Fatalf("%v: %s", err, res.Body)
	}
	if r.JSONRPC != "2.0" {
		t.Errorf("jsonrpc = %q", r.JSONRPC)
	}
	return r
}

func TestInitialize(t *testing.T) {
	h := newTestServer(t)
	r := rpc(t, h, "good", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"claude","version":"1"}}}`)
	if string(r.ID) != "1" || r.Error != nil {
		t.Fatalf("id %s, error %+v", r.ID, r.Error)
	}
	var got struct {
		ProtocolVersion string                     `json:"protocolVersion"`
		Capabilities    map[string]json.RawMessage `json:"capabilities"`
		ServerInfo      struct{ Name string }      `json:"serverInfo"`
	}
	json.Unmarshal(r.Result, &got)
	if got.ProtocolVersion != "2025-06-18" || string(got.Capabilities["tools"]) != "{}" || got.ServerInfo.Name != "bob" {
		t.Errorf("initialize = %s", r.Result)
	}
}

// A notification (no id) is acknowledged with 202 and nothing else.
func TestNotificationIsAccepted(t *testing.T) {
	res := post(t, newTestServer(t), "good", `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	if res.Code != http.StatusAccepted || res.Body.Len() != 0 {
		t.Errorf("status %d, body %q", res.Code, res.Body)
	}
}

func TestPing(t *testing.T) {
	r := rpc(t, newTestServer(t), "good", `{"jsonrpc":"2.0","id":"p","method":"ping"}`)
	if string(r.ID) != `"p"` || r.Error != nil || string(r.Result) != "{}" {
		t.Errorf("ping = %+v %s", r.Error, r.Result)
	}
}

func toolNames(t *testing.T, h http.Handler, token string) string {
	t.Helper()
	r := rpc(t, h, token, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	var got struct {
		Tools []struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			InputSchema json.RawMessage `json:"inputSchema"`
		} `json:"tools"`
	}
	json.Unmarshal(r.Result, &got)
	var names []string
	for _, tl := range got.Tools {
		if tl.Description == "" || len(tl.InputSchema) == 0 {
			t.Errorf("tool %s has no description or schema: %s", tl.Name, r.Result)
		}
		names = append(names, tl.Name)
	}
	return strings.Join(names, " ")
}

// tools/list shows only the tools available to the caller, in registration order.
func TestToolsListOnlyAvailable(t *testing.T) {
	h := newTestServer(t)
	if got := toolNames(t, h, "good"); got != "echo secret fails" {
		t.Errorf("wolfie's tools = %q", got)
	}
	if got := toolNames(t, h, "plain"); got != "echo fails" {
		t.Errorf("a plain chat's tools = %q", got)
	}
}

func TestToolsCall(t *testing.T) {
	h := newTestServer(t)
	r := rpc(t, h, "good", `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo","arguments":{"say":"hi"}}}`)
	if r.Error != nil {
		t.Fatalf("error %+v", r.Error)
	}
	var got struct {
		IsError bool `json:"isError"`
		Content []struct{ Type, Text string }
	}
	json.Unmarshal(r.Result, &got)
	if got.IsError || len(got.Content) != 1 || got.Content[0].Type != "text" {
		t.Fatalf("result = %s", r.Result)
	}
	var said map[string]string
	if err := json.Unmarshal([]byte(got.Content[0].Text), &said); err != nil {
		t.Fatalf("text is not JSON: %q", got.Content[0].Text)
	}
	if said["said"] != "hi" || said["project"] != "wolf" || said["session"] != "s1" {
		t.Errorf("echo = %v", said)
	}
}

// A tool that fails reports it to the model as an isError result, not a protocol error.
func TestToolErrorIsAResult(t *testing.T) {
	r := rpc(t, newTestServer(t), "good", `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"fails"}}`)
	if r.Error != nil {
		t.Fatalf("error %+v", r.Error)
	}
	var got struct {
		IsError bool `json:"isError"`
		Content []struct{ Type, Text string }
	}
	json.Unmarshal(r.Result, &got)
	if !got.IsError || len(got.Content) != 1 || got.Content[0].Text != "no such worker" {
		t.Errorf("result = %s", r.Result)
	}
}

// Calling a tool that does not exist, or is not available to the caller, is invalid params.
func TestUnknownOrUnavailableToolIsInvalidParams(t *testing.T) {
	h := newTestServer(t)
	for _, c := range []struct{ token, tool string }{{"good", "nope"}, {"plain", "secret"}} {
		r := rpc(t, h, c.token, `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"`+c.tool+`"}}`)
		if r.Error == nil || r.Error.Code != -32602 || len(r.Result) != 0 {
			t.Errorf("%s calling %s: error %+v, result %s", c.token, c.tool, r.Error, r.Result)
		}
	}
}

func TestProtocolErrors(t *testing.T) {
	h := newTestServer(t)
	r := rpc(t, h, "good", `{"jsonrpc":"2.0","id":6,"method":"resources/list"}`)
	if r.Error == nil || r.Error.Code != -32601 || string(r.ID) != "6" {
		t.Errorf("unknown method: %+v id %s", r.Error, r.ID)
	}
	r = rpc(t, h, "good", `{"jsonrpc":`)
	if r.Error == nil || r.Error.Code != -32700 || string(r.ID) != "null" {
		t.Errorf("malformed JSON: %+v id %s", r.Error, r.ID)
	}
	r = rpc(t, h, "good", `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"echo","arguments":"`+strings.Repeat("x", 8<<20)+`"}}`)
	if r.Error == nil || r.Error.Code != -32600 {
		t.Errorf("a body over 8 MiB: %+v", r.Error)
	}
}

func TestAuthAndMethod(t *testing.T) {
	h := newTestServer(t)
	for _, c := range []struct {
		token string
		want  int
	}{{"", 401}, {"bad", 401}, {"gone", 404}} {
		res := post(t, h, c.token, `{"jsonrpc":"2.0","id":1,"method":"ping"}`)
		if res.Code != c.want {
			t.Errorf("token %q: status %d, want %d", c.token, res.Code, c.want)
		}
	}
	if res := post(t, h, "", `{}`); res.Header().Get("WWW-Authenticate") != "Bearer" {
		t.Errorf("401 without WWW-Authenticate: %v", res.Header())
	}
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer good")
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusMethodNotAllowed || res.Header().Get("Allow") != "POST" {
		t.Errorf("GET: status %d, Allow %q", res.Code, res.Header().Get("Allow"))
	}
}

func TestRegisterRefusesDuplicates(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("registering a tool name twice did not panic")
		}
	}()
	s := New()
	call := func(context.Context, Caller, json.RawMessage) (any, error) { return nil, nil }
	s.Register(Tool{Name: "a", Call: call}, Tool{Name: "a", Call: call})
}
