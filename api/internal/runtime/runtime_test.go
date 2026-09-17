package runtime

import (
	"slices"
	"testing"

	"github.com/badcodetv/bob/internal/store"
)

func TestSpecEnvironment(t *testing.T) {
	m := &Manager{cfg: Config{DefaultImage: "bob-runtime:dev", PassEnv: map[string]string{"GITHUB_TOKEN": "shared", "FRED_API_KEY": "fred"}}}
	spec := m.spec(store.Project{Name: "wolf", RepoURL: "https://github.com/x/wolf", RepoRef: "main"},
		map[string]string{"GITHUB_TOKEN": "wolf-only", "BOB_REPO_URL": "https://evil.example/repo"})
	want := []string{"BOB_REPO_REF=main", "BOB_REPO_SUBFOLDER=", "BOB_REPO_URL=https://github.com/x/wolf", "FRED_API_KEY=fred", "GITHUB_TOKEN=wolf-only"}
	if !slices.Equal(spec.Env, want) {
		t.Errorf("env = %v, want %v", spec.Env, want)
	}
	if spec.Image != "bob-runtime:dev" || spec.Name != "bob-project-wolf" {
		t.Errorf("spec: %+v", spec)
	}
}
