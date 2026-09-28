package runtime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/badcodetv/bob/internal/store"
)

// TestEnsureReachesTheProjectsContainer checks Bob addresses a project by its container name,
// sends the project's own token, and waits for health rather than assuming.
func TestEnsureReachesTheProjectsContainer(t *testing.T) {
	var asked, auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = r.URL.Path
		user, pass, _ := r.BasicAuth()
		auth = user + ":" + pass
	}))
	t.Cleanup(srv.Close)

	m := NewManager(Config{Tokens: map[string]string{"wolf": "wolf-token", "demo": "demo-token"},
		Hosts: map[string]string{"wolf": strings.TrimPrefix(srv.URL, "http://")}})
	base, err := m.Ensure(t.Context(), store.Project{Name: "wolf"})
	if err != nil {
		t.Fatal(err)
	}
	if asked != "/health" {
		t.Errorf("asked for %q, want /health", asked)
	}
	if want := "bob:wolf-token"; auth != want {
		t.Errorf("auth = %q, want %q", auth, want)
	}
	if !strings.Contains(base, "wolf-token") {
		t.Error("the base URL should carry the project's token")
	}
}

// A container that is not running is a deploy problem, and the error must say so — Bob has no
// socket and cannot start anything.
func TestEnsureSaysWhichContainerIsMissing(t *testing.T) {
	m := NewManager(Config{Tokens: map[string]string{"wolf": "wolf-token"}, Wait: 50 * time.Millisecond,
		Hosts: map[string]string{"wolf": "127.0.0.1:1"}})
	_, err := m.Ensure(context.Background(), store.Project{Name: "wolf"})
	if err == nil || !strings.Contains(err.Error(), "bob-project-wolf") {
		t.Errorf("err = %v, want it to name the container", err)
	}
}

// A project with no token is a deploy mistake Bob's boot should have caught; Ensure refuses
// rather than calling the container without a password.
func TestEnsureNeedsTheProjectsToken(t *testing.T) {
	m := NewManager(Config{Tokens: map[string]string{"wolf": "wolf-token"}, Wait: 50 * time.Millisecond})
	_, err := m.Ensure(context.Background(), store.Project{Name: "demo"})
	if err == nil || !strings.Contains(err.Error(), "BOB_RUNTIME_TOKEN_DEMO") {
		t.Errorf("err = %v, want it to name the missing variable", err)
	}
}

// A project's token variable is its name upper-cased with - as _, because a shell variable name
// cannot hold a -.
func TestTokenVar(t *testing.T) {
	for name, want := range map[string]string{
		"enc":            "BOB_RUNTIME_TOKEN_ENC",
		"marketing-team": "BOB_RUNTIME_TOKEN_MARKETING_TEAM",
		"a1-b-2":         "BOB_RUNTIME_TOKEN_A1_B_2",
	} {
		if got := TokenVar(name); got != want {
			t.Errorf("TokenVar(%q) = %q, want %q", name, got, want)
		}
	}
}

// The runtime refuses a turn with no system_prompt, so it is sent even when empty; so is
// mcp_token, which the runtime's TurnRequest names as always present.
func TestTurnRequestAlwaysSendsThePrompt(t *testing.T) {
	b, err := json.Marshal(TurnRequest{SessionID: "s", Engine: "claude", Text: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	var sent map[string]any
	json.Unmarshal(b, &sent)
	for _, key := range []string{"session_id", "engine", "system_prompt", "mcp_token", "text"} {
		if _, ok := sent[key]; !ok {
			t.Errorf("%s is missing from %s", key, b)
		}
	}
	for _, key := range []string{"tools", "model", "effort", "resume"} {
		if _, ok := sent[key]; ok {
			t.Errorf("%s should be left out when empty: %s", key, b)
		}
	}
}
