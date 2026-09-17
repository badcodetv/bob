package runtime

import (
	"context"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/badcodetv/bob/internal/store"
)

func TestSpecEnvironment(t *testing.T) {
	m := &Manager{cfg: Config{DefaultImage: "bob-runtime:dev", PassEnv: map[string]string{"GITHUB_TOKEN": "shared", "FRED_API_KEY": "fred"}}}
	spec := m.spec(store.Project{Name: "wolf", RepoURL: "https://github.com/x/wolf", RepoRef: "main"},
		map[string]string{"GITHUB_TOKEN": "wolf-only", "BOB_REPO_URL": "https://evil.example/repo", "BOB_RUNTIME_TOKEN": "guess"})
	want := []string{"BOB_REPO_REF=main", "BOB_REPO_SUBFOLDER=", "BOB_REPO_URL=https://github.com/x/wolf", "BOB_RUNTIME_TOKEN=" + Token(nil, "wolf"), "FRED_API_KEY=fred", "GITHUB_TOKEN=wolf-only"}
	if !slices.Equal(spec.Env, want) {
		t.Errorf("env = %v, want %v", spec.Env, want)
	}
	if spec.Image != "bob-runtime:dev" || spec.Name != "bob-project-wolf" {
		t.Errorf("spec: %+v", spec)
	}
}

func TestSpecFingerprint(t *testing.T) {
	p := store.Project{Name: "wolf", RepoURL: "https://github.com/x/wolf", RepoRef: "main"}
	base := Config{DefaultImage: "runtime:1", TokenKey: []byte("k"), PassEnv: map[string]string{"GITHUB_TOKEN": "a"}, Memory: 8 << 30, NanoCPUs: 4e9, PidsLimit: 2048}
	label := func(c Config) string { return (&Manager{cfg: c}).spec(p, nil).Labels[SpecLabel] }
	first := label(base)
	if first == "" || first != label(base) {
		t.Fatal("the fingerprint must be stable")
	}
	if len(first) != 64 {
		t.Errorf("unexpected label %q", first)
	}
	changed := []func(c *Config){
		func(c *Config) { c.DefaultImage = "runtime:2" },
		func(c *Config) { c.PassEnv = map[string]string{"GITHUB_TOKEN": "rotated"} },
		func(c *Config) { c.Memory = 4 << 30 },
		func(c *Config) { c.TokenKey = []byte("other") },
	}
	for i, change := range changed {
		c := base
		change(&c)
		if label(c) == first {
			t.Errorf("change %d did not change the fingerprint", i)
		}
	}
	s := (&Manager{cfg: base}).spec(p, nil)
	if s.Memory != 8<<30 || s.NanoCPUs != 4e9 || s.PidsLimit != 2048 {
		t.Errorf("limits not applied: %+v", s)
	}
}

func TestTokenAndErrors(t *testing.T) {
	a, b := Token([]byte("k"), "wolf"), Token([]byte("k"), "enc")
	if a == b || a == Token([]byte("other"), "wolf") || len(a) != 64 {
		t.Errorf("tokens must differ by project and key: %s %s", a, b)
	}
	base := (&url.URL{Scheme: "http", User: url.UserPassword("bob", a), Host: "127.0.0.1:1"}).String()
	_, err := Workers(context.Background(), base, false)
	if err == nil || strings.Contains(err.Error(), a) {
		t.Errorf("an error must not show the token: %v", err)
	}
}

func TestDestroyedProjectsStayDown(t *testing.T) {
	m := &Manager{destroyed: map[string]bool{"wolf": true}}
	if _, err := m.Ensure(context.Background(), store.Project{Name: "wolf"}); err == nil || !strings.Contains(err.Error(), "deleted") {
		t.Fatalf("Ensure after Destroy: %v", err)
	}
	m.Revive("wolf")
	if m.destroyed["wolf"] {
		t.Error("Revive did not clear the mark")
	}
}
