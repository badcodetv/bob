package runtime

import (
	"context"
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
