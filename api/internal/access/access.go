// Package access decides who may sign in to Bob and which projects each person may use.
//
// The map is JSON, email → project names, where "*" means admin: every project, plus creating,
// changing and deleting projects and schedules.
//
//	{"kai@example.com": ["*"], "tester@example.com": ["wolf"]}
package access

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

type Map map[string][]string

// Parse reads a map. agent-bob's form, {"users": {…}, "projects": {…}}, is accepted too: only
// its users are read.
func Parse(data []byte) (Map, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("not a JSON object: %w", err)
	}
	if users, ok := raw["users"]; ok {
		raw = nil
		if err := json.Unmarshal(users, &raw); err != nil {
			return nil, fmt.Errorf(`"users" is not a JSON object: %w`, err)
		}
	}
	m := Map{}
	for email, v := range raw {
		var list []any
		if err := json.Unmarshal(v, &list); err != nil {
			return nil, fmt.Errorf("%s: want a list of project names", email)
		}
		key := strings.ToLower(strings.TrimSpace(email))
		if !strings.Contains(key, "@") {
			return nil, fmt.Errorf("%q is not an email address", email)
		}
		for _, p := range list {
			s, ok := p.(string)
			if !ok || s == "" {
				return nil, fmt.Errorf("%s: every entry must be a project name or \"*\", got %v", email, p)
			}
			m[key] = append(m[key], s)
		}
		if _, ok := m[key]; !ok {
			m[key] = []string{}
		}
	}
	if len(m) == 0 {
		return nil, errors.New("the map lists nobody")
	}
	return m, nil
}

// Load builds the map from the environment: BOB_PROJECT_MAP (inline JSON) wins over
// BOB_PROJECT_MAP_FILE; with neither, every email in the deprecated BOB_ALLOWED_EMAILS is an
// admin, and deprecated reports that.
func Load(getenv func(string) string) (m Map, deprecated bool, err error) {
	if inline := getenv("BOB_PROJECT_MAP"); inline != "" {
		m, err = Parse([]byte(inline))
		if err != nil {
			return nil, false, fmt.Errorf("BOB_PROJECT_MAP: %w", err)
		}
		return m, false, nil
	}
	if path := getenv("BOB_PROJECT_MAP_FILE"); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, false, fmt.Errorf("BOB_PROJECT_MAP_FILE: %w", err)
		}
		if m, err = Parse(data); err != nil {
			return nil, false, fmt.Errorf("BOB_PROJECT_MAP_FILE %s: %w", path, err)
		}
		return m, false, nil
	}
	m = Map{}
	for _, e := range strings.Split(getenv("BOB_ALLOWED_EMAILS"), ",") {
		if e = strings.ToLower(strings.TrimSpace(e)); e != "" {
			m[e] = []string{"*"}
		}
	}
	if len(m) == 0 {
		return nil, false, errors.New("BOB_PROJECT_MAP (or BOB_PROJECT_MAP_FILE) is required")
	}
	return m, true, nil
}

// Allowed reports whether email may sign in at all.
func (m Map) Allowed(email string) bool {
	_, ok := m[email]
	return ok
}

// Admin reports whether email may see every project and change projects and schedules.
func (m Map) Admin(email string) bool {
	for _, p := range m[email] {
		if p == "*" {
			return true
		}
	}
	return false
}

// Member reports whether email may use project: chat in it, view its files, run its schedules.
func (m Map) Member(email, project string) bool {
	for _, p := range m[email] {
		if p == "*" || p == project {
			return true
		}
	}
	return false
}
