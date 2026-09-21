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

	m := NewManager(Config{TokenKey: []byte("key"), Hosts: map[string]string{"wolf": strings.TrimPrefix(srv.URL, "http://")}})
	base, err := m.Ensure(t.Context(), store.Project{Name: "wolf"})
	if err != nil {
		t.Fatal(err)
	}
	if asked != "/health" {
		t.Errorf("asked for %q, want /health", asked)
	}
	if want := "bob:" + Token([]byte("key"), "wolf"); auth != want {
		t.Errorf("auth = %q, want %q", auth, want)
	}
	if !strings.Contains(base, Token([]byte("key"), "wolf")) {
		t.Error("the base URL should carry the project's token")
	}
}

// A container that is not running is a deploy problem, and the error must say so — Bob has no
// socket and cannot start anything.
func TestEnsureSaysWhichContainerIsMissing(t *testing.T) {
	m := NewManager(Config{TokenKey: []byte("key"), Wait: 50 * time.Millisecond,
		Hosts: map[string]string{"wolf": "127.0.0.1:1"}})
	_, err := m.Ensure(context.Background(), store.Project{Name: "wolf"})
	if err == nil || !strings.Contains(err.Error(), "bob-project-wolf") {
		t.Errorf("err = %v, want it to name the container", err)
	}
}

// Each project's token differs, so an agent in one container cannot drive another's runtime.
func TestTokenIsPerProject(t *testing.T) {
	if Token([]byte("key"), "wolf") == Token([]byte("key"), "demo") {
		t.Error("two projects share a token")
	}
	if Token([]byte("a"), "wolf") == Token([]byte("b"), "wolf") {
		t.Error("the key does not change the token")
	}
}
