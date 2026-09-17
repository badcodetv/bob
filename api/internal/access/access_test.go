package access

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	m, err := Parse([]byte(`{"Kai@Example.com": ["*"], "tester@example.com": ["wolf"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !m.Admin("kai@example.com") || m.Admin("tester@example.com") {
		t.Errorf("admin: %v", m)
	}
	if !m.Member("tester@example.com", "wolf") || m.Member("tester@example.com", "enc") || m.Member("nobody@example.com", "wolf") {
		t.Errorf("member: %v", m)
	}
	if !m.Member("kai@example.com", "anything") || !m.Allowed("tester@example.com") || m.Allowed("nobody@example.com") {
		t.Errorf("allowed: %v", m)
	}

	old, err := Parse([]byte(`{"users": {"kai@example.com": ["*"]}, "projects": {"enc": {"connections": {}}}}`))
	if err != nil || len(old) != 1 || !old.Admin("kai@example.com") {
		t.Errorf("agent-bob form: %v, %v", old, err)
	}

	for in, want := range map[string]string{
		`not json`:                  "not a JSON object",
		`{}`:                        "lists nobody",
		`{"kai@example.com": "*"}`:  "list of project names",
		`{"kai@example.com": [1]}`:  "project name",
		`{"kai@example.com": [""]}`: "project name",
		`{"kai": ["*"]}`:            "not an email",
		`{"users": []}`:             `"users" is not`,
	} {
		if _, err := Parse([]byte(in)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", in, err, want)
		}
	}
}

func TestLoad(t *testing.T) {
	file := filepath.Join(t.TempDir(), "map.json")
	os.WriteFile(file, []byte(`{"file@example.com": ["wolf"]}`), 0o600)
	env := func(vars map[string]string) func(string) string { return func(k string) string { return vars[k] } }

	m, dep, err := Load(env(map[string]string{"BOB_PROJECT_MAP": `{"inline@example.com": ["*"]}`, "BOB_PROJECT_MAP_FILE": file}))
	if err != nil || dep || !m.Allowed("inline@example.com") || m.Allowed("file@example.com") {
		t.Errorf("inline should win: %v %v %v", m, dep, err)
	}
	m, dep, err = Load(env(map[string]string{"BOB_PROJECT_MAP_FILE": file, "BOB_ALLOWED_EMAILS": "old@example.com"}))
	if err != nil || dep || !m.Member("file@example.com", "wolf") {
		t.Errorf("file: %v %v %v", m, dep, err)
	}
	m, dep, err = Load(env(map[string]string{"BOB_ALLOWED_EMAILS": " Old@example.com , two@example.com"}))
	if err != nil || !dep || !m.Admin("old@example.com") || !m.Admin("two@example.com") {
		t.Errorf("transition: %v %v %v", m, dep, err)
	}
	if _, _, err = Load(env(nil)); err == nil {
		t.Error("no map at all should fail")
	}
	if _, _, err = Load(env(map[string]string{"BOB_PROJECT_MAP": `{"x@example.com": [2]}`})); err == nil || !strings.Contains(err.Error(), "BOB_PROJECT_MAP") {
		t.Errorf("bad inline map: %v", err)
	}
	if _, _, err = Load(env(map[string]string{"BOB_PROJECT_MAP_FILE": "/nope.json"})); err == nil {
		t.Error("missing file should fail")
	}
}
