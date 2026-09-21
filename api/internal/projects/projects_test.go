package projects

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "projects.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoad(t *testing.T) {
	ps, err := Load(write(t, `
projects:
  - name: wolf
    repo: https://github.com/badcodetv/wolf
    ref: main
    config_dir: bob
  - name: demo
    repo: https://github.com/badcodetv/demo
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 2 {
		t.Fatalf("got %d projects", len(ps))
	}
	if ps[0].Name != "wolf" || ps[0].Subfolder != "bob" {
		t.Errorf("wolf: %+v", ps[0])
	}
	if ps[1].RepoRef != "main" {
		t.Errorf("a project with no ref should default to main, got %q", ps[1].RepoRef)
	}
}

func TestLoadRefuses(t *testing.T) {
	for _, tt := range []struct{ name, body, want string }{
		{"no projects", "projects: []", "lists no projects"},
		{"bad name", "projects:\n  - name: Wolf\n    repo: x", "must match"},
		{"duplicate", "projects:\n  - name: a\n    repo: x\n  - name: a\n    repo: y", "listed twice"},
		{"no repo", "projects:\n  - name: a", "has no repo"},
		{"misspelled key", "projects:\n  - name: a\n    repo: x\n    files_root: site", "files_root"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(write(t, tt.body))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want it to mention %q", err, tt.want)
			}
		})
	}
	if _, err := Load(filepath.Join(t.TempDir(), "missing.yaml")); err == nil {
		t.Error("a missing file should be an error")
	}
}

// A deployed projects.yaml uses keys the API does not read but the compose generator does.
// KnownFields(true) makes an unnamed key fatal at boot, so this pins every key
// scripts/compose-projects.mjs accepts. Bob crash-looped on `pass` on 2026-09-21 because the
// struct named six of the ten.
func TestLoadAcceptsEveryGeneratorKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "projects.yaml")
	if err := os.WriteFile(path, []byte(`projects:
  - name: wolf
    repo: https://github.com/badcodetv/wolf
    ref: main
    config_dir: bob
    pass: [CLAUDE_CODE_OAUTH_TOKEN, GITHUB_TOKEN, FRED_API_KEY]
    image: example/runtime:tag
    repo_mount: .
    mem_limit: 8g
    pids_limit: 4096
    stop_grace_period: 10m
`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("a deployed projects.yaml must load: %v", err)
	}
	if len(got) != 1 || got[0].Name != "wolf" || got[0].RepoRef != "main" || got[0].Subfolder != "bob" {
		t.Errorf("wrong project: %+v", got)
	}
}
