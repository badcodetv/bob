// Package projects reads the list of projects Bob serves. It is deployment configuration, not
// something the API creates: each project also has a container declared in the same deploy's
// compose file, and Bob only ever talks to it over HTTP.
package projects

import (
	"bytes"
	"fmt"
	"os"
	"regexp"

	"github.com/badcodetv/bob/internal/store"
	"gopkg.in/yaml.v3"
)

// name is the same shape the projects table enforces (001_init.sql). A project's name is also its
// container name and its volume name, so it can never change.
var name = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,40}$`)

type file struct {
	Projects []struct {
		Name      string `yaml:"name"`
		Repo      string `yaml:"repo"`
		Ref       string `yaml:"ref"`
		ConfigDir string `yaml:"config_dir"`
		// Image is what the project's compose service runs; Bob keeps it only to show it.
		Image string `yaml:"image"`
		// RepoMount is local development only: a host path bind-mounted at /seed in the
		// project's container, used with repo "file:///seed".
		RepoMount string `yaml:"repo_mount"`
	} `yaml:"projects"`
}

// Load reads the file at path. It fails rather than guessing: a bad name, a duplicate, or a
// project with no repository stops Bob at boot, where it is obvious, instead of at the first turn.
func Load(path string) ([]store.Project, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var f file
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true) // a misspelled key is a mistake, not something to ignore
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(f.Projects) == 0 {
		return nil, fmt.Errorf("%s lists no projects", path)
	}
	out := make([]store.Project, 0, len(f.Projects))
	seen := map[string]bool{}
	for i, p := range f.Projects {
		switch {
		case !name.MatchString(p.Name):
			return nil, fmt.Errorf("%s: project %d: name %q must match %s", path, i+1, p.Name, name)
		case seen[p.Name]:
			return nil, fmt.Errorf("%s: project %q is listed twice", path, p.Name)
		case p.Repo == "":
			return nil, fmt.Errorf("%s: project %q has no repo", path, p.Name)
		}
		seen[p.Name] = true
		ref := p.Ref
		if ref == "" {
			ref = "main"
		}
		out = append(out, store.Project{
			Name: p.Name, RepoURL: p.Repo, RepoRef: ref, Subfolder: p.ConfigDir,
			Image: p.Image, RepoMount: p.RepoMount,
		})
	}
	return out, nil
}
