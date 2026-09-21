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
    files_root: site
  - name: demo
    repo: https://github.com/badcodetv/demo
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 2 {
		t.Fatalf("got %d projects", len(ps))
	}
	if ps[0].Name != "wolf" || ps[0].Subfolder != "bob" || ps[0].FilesRoot != "site" {
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
		{"misspelled key", "projects:\n  - name: a\n    repo: x\n    branch: main", "branch"},
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
