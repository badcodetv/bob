package main

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/badcodetv/bob/internal/access"
	"github.com/badcodetv/bob/internal/auth"
	"github.com/badcodetv/bob/internal/broker"
	"github.com/badcodetv/bob/internal/secrets"
	"github.com/badcodetv/bob/internal/store"
)

type recordingContainers struct {
	fakeContainers
	recreated []string
}

func (c *recordingContainers) Recreate(_ context.Context, project string) error {
	c.recreated = append(c.recreated, project)
	return nil
}

func TestSecrets(t *testing.T) {
	st := testStore(t)
	st.CreateProject(t.Context(), store.Project{Name: "wolf"})
	box, _ := secrets.NewBox(base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")))
	people := access.Map{"admin@example.com": {"*"}}
	containers := &recordingContainers{}
	a := &app{
		auth: &auth.Auth{Secret: []byte("0123456789abcdef"), Allowed: people.Allowed}, access: people,
		store: st, broker: broker.New(), runtime: containers, secrets: box, turns: map[string]context.CancelFunc{},
	}
	a.projectOf = a.storeProjectOf
	rec := httptest.NewRecorder()
	a.auth.SetSession(rec, auth.User{Email: "admin@example.com"})
	cookie := rec.Result().Cookies()[0]
	do := func(method, path, body string) (int, string) {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		a.routes().ServeHTTP(rec, req)
		b, _ := io.ReadAll(rec.Body)
		return rec.Code, string(b)
	}
	const value = "fred-4f1c9e-very-secret"

	if code, body := do("PUT", "/api/projects/wolf/secrets/FRED_API_KEY", `{"value":"`+value+`"}`); code != 200 || !strings.Contains(body, `"applied":true`) {
		t.Fatalf("set: %d %s", code, body)
	}
	if len(containers.recreated) != 1 {
		t.Errorf("container not recreated: %v", containers.recreated)
	}
	for _, path := range []string{"/api/projects/wolf/secrets", "/api/projects/wolf", "/api/projects"} {
		if code, body := do("GET", path, ""); code != 200 || strings.Contains(body, value) || strings.Contains(body, base64.StdEncoding.EncodeToString([]byte(value))) {
			t.Errorf("%s: %d, and must not hold the value: %s", path, code, body)
		}
	}
	if _, body := do("GET", "/api/projects/wolf/secrets", ""); !strings.Contains(body, `"name":"FRED_API_KEY"`) || !strings.Contains(body, `"updated_by":"admin@example.com"`) {
		t.Errorf("list: %s", body)
	}
	sealed, _ := st.SealedSecrets(t.Context(), "wolf")
	if len(sealed) != 1 || strings.Contains(string(sealed[0].Ciphertext), value) {
		t.Errorf("stored in the clear: %+v", sealed)
	}
	if got, err := a.projectSecrets(t.Context(), "wolf"); err != nil || got["FRED_API_KEY"] != value {
		t.Errorf("for the container: %v %v", got, err)
	}

	for path, body := range map[string]string{
		"/api/projects/wolf/secrets/lower":          `{"value":"x"}`,
		"/api/projects/wolf/secrets/BOB_USER_EMAIL": `{"value":"x"}`,
		"/api/projects/wolf/secrets/PATH":           `{"value":"x"}`,
		"/api/projects/wolf/secrets/EMPTY":          `{"value":""}`,
		"/api/projects/wolf/secrets/HUGE":           `{"value":"` + strings.Repeat("x", maxSecretBytes+1) + `"}`,
	} {
		if code, _ := do("PUT", path, body); code != 400 {
			t.Errorf("PUT %s = %d, want 400", path, code)
		}
	}
	if code, _ := do("PUT", "/api/projects/nope/secrets/X", `{"value":"x"}`); code != 404 {
		t.Errorf("unknown project: %d", code)
	}

	// While a turn runs in the project, the change waits instead of cutting it off.
	sess, _ := st.CreateSession(t.Context(), "wolf", "researcher", "claude", "", "")
	a.turns[sess.ID] = func() {}
	if _, body := do("PUT", "/api/projects/wolf/secrets/OTHER", `{"value":"y"}`); !strings.Contains(body, `"applied":false`) || len(containers.recreated) != 1 {
		t.Errorf("busy project: %s, recreated %v", body, containers.recreated)
	}
	delete(a.turns, sess.ID)

	if code, _ := do("DELETE", "/api/projects/wolf/secrets/FRED_API_KEY", ""); code != 200 {
		t.Errorf("delete: %d", code)
	}
	if code, _ := do("DELETE", "/api/projects/wolf/secrets/FRED_API_KEY", ""); code != 404 {
		t.Errorf("delete again: %d", code)
	}

	// With the key unset, setting is refused and a project that has secrets will not start.
	a.secrets = nil
	if code, _ := do("PUT", "/api/projects/wolf/secrets/X", `{"value":"x"}`); code != http.StatusServiceUnavailable {
		t.Errorf("no key: %d", code)
	}
	if _, err := a.projectSecrets(t.Context(), "wolf"); err == nil || strings.Contains(err.Error(), "y") && strings.Contains(err.Error(), "value") {
		t.Errorf("no key, with a secret stored: %v", err)
	}
}
