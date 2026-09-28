package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/badcodetv/bob/internal/store"
)

func TestParseHosts(t *testing.T) {
	got, err := parseHosts("wolf=127.0.0.1:8081, demo=127.0.0.1:8082")
	if err != nil || len(got) != 2 || got["wolf"] != "127.0.0.1:8081" || got["demo"] != "127.0.0.1:8082" {
		t.Errorf("parseHosts = %v, %v", got, err)
	}
	if got, err := parseHosts(""); err != nil || got != nil {
		t.Errorf("empty = %v, %v; want nil, nil", got, err)
	}
	for _, in := range []string{"wolf", "=127.0.0.1:8081", "wolf="} {
		if _, err := parseHosts(in); err == nil {
			t.Errorf("parseHosts(%q) should fail", in)
		}
	}
}

// projectsFromEnv reads BOB_PROJECTS and each project's token, and fails naming the exact
// variable to set — a boot failure is only useful if it says what to fix.
func TestProjectsFromEnv(t *testing.T) {
	token := strings.Repeat("x", 32)
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

	names, tokens, err := projectsFromEnv(env(map[string]string{
		"BOB_PROJECTS":                     " enc, marketing-team ",
		"BOB_RUNTIME_TOKEN_ENC":            token,
		"BOB_RUNTIME_TOKEN_MARKETING_TEAM": token + "y",
	}))
	if err != nil || strings.Join(names, ",") != "enc,marketing-team" ||
		tokens["enc"] != token || tokens["marketing-team"] != token+"y" {
		t.Errorf("projectsFromEnv = %v, %v, %v", names, tokens, err)
	}

	for _, c := range []struct {
		env  map[string]string
		want string // in the error
	}{
		{map[string]string{}, "BOB_PROJECTS"},
		{map[string]string{"BOB_PROJECTS": " , "}, "BOB_PROJECTS"},
		{map[string]string{"BOB_PROJECTS": "enc"}, "BOB_RUNTIME_TOKEN_ENC"},
		{map[string]string{"BOB_PROJECTS": "my-proj", "BOB_RUNTIME_TOKEN_MY_PROJ": "short"}, "BOB_RUNTIME_TOKEN_MY_PROJ"},
		{map[string]string{"BOB_PROJECTS": "Enc", "BOB_RUNTIME_TOKEN_ENC": token}, `"Enc"`},
		{map[string]string{"BOB_PROJECTS": "enc,enc", "BOB_RUNTIME_TOKEN_ENC": token}, "twice"},
	} {
		if _, _, err := projectsFromEnv(env(c.env)); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("projectsFromEnv(%v) error = %v, want one naming %s", c.env, err, c.want)
		}
	}
}

// driveClientsFromEnv builds a Drive client only for a project with a BOB_DRIVE_TOKEN_<NAME>, and
// fails at boot (naming both required variables) if the OAuth client that minted it is missing —
// no project has a Drive token today, so the zero-token case must be a no-op.
func TestDriveClientsFromEnv(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	ctx := context.Background()

	clients, err := driveClientsFromEnv(ctx, env(map[string]string{}), []string{"enc", "marketing"})
	if err != nil || len(clients) != 0 {
		t.Errorf("no tokens set: clients = %v, err = %v; want none, no error", clients, err)
	}

	clients, err = driveClientsFromEnv(ctx, env(map[string]string{
		"BOB_DRIVE_CLIENT_ID":     "id",
		"BOB_DRIVE_CLIENT_SECRET": "secret",
		"BOB_DRIVE_TOKEN_ENC":     "refresh-token",
	}), []string{"enc", "marketing"})
	if err != nil {
		t.Fatalf("driveClientsFromEnv: %v", err)
	}
	if _, ok := clients["enc"]; !ok {
		t.Errorf("clients = %v, want enc", clients)
	}
	if _, ok := clients["marketing"]; ok {
		t.Errorf("clients = %v, marketing has no token and should have no client", clients)
	}

	if _, err := driveClientsFromEnv(ctx, env(map[string]string{
		"BOB_DRIVE_TOKEN_ENC": "refresh-token",
	}), []string{"enc"}); err == nil || !strings.Contains(err.Error(), "BOB_DRIVE_CLIENT_ID") {
		t.Errorf("missing client id/secret: err = %v, want it to name BOB_DRIVE_CLIENT_ID", err)
	}
}

func TestPublicURL(t *testing.T) {
	env := func(v string) func(string) string {
		return func(k string) string {
			if k == "BOB_PUBLIC_URL" {
				return v
			}
			return ""
		}
	}
	if got, err := publicURL(env("https://bob.box.badcode.tv")); err != nil || got != "https://bob.box.badcode.tv" {
		t.Errorf("publicURL = %q, %v", got, err)
	}
	for _, v := range []string{"", "http://localhost:8080/", "bob.box.badcode.tv"} {
		if _, err := publicURL(env(v)); err == nil || !strings.Contains(err.Error(), "BOB_PUBLIC_URL") {
			t.Errorf("publicURL(%q) error = %v, want one naming BOB_PUBLIC_URL", v, err)
		}
	}
}

// TestAbsentProject checks what happens to a project BOB_PROJECTS stops listing: it is not
// served and its schedules do not fire, but nothing of it is deleted — re-listing it brings its
// chats back. Projects are deployment configuration; their history is not.
func TestAbsentProject(t *testing.T) {
	st := testStore(t)
	ctx := t.Context()
	if err := st.ReconcileProjects(ctx, []string{"wolf", "demo"}); err != nil {
		t.Fatal(err)
	}
	w := insertWorker(t, st, "demo", "w", "claude")
	sess, err := st.CreateSession(ctx, "demo", &w, "w", "claude", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateSchedule(ctx, store.Schedule{Project: "demo", Name: "daily", WorkerID: w, Cron: "0 6 * * *", Timezone: "UTC", Message: "go", Enabled: true, KeepSessions: 5}); err != nil {
		t.Fatal(err)
	}

	// demo leaves the file.
	if err := st.ReconcileProjects(ctx, []string{"wolf"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Project(ctx, "demo"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("an absent project should not be found, got %v", err)
	}
	if ps, err := st.Projects(ctx); err != nil || len(ps) != 1 || ps[0].Name != "wolf" {
		t.Errorf("projects = %v, %v; want just wolf", ps, err)
	}
	if absent, err := st.AbsentProjects(ctx); err != nil || len(absent) != 1 || absent[0] != "demo" {
		t.Errorf("absent = %v, %v; want [demo]", absent, err)
	}
	if list, err := st.Schedules(ctx, ""); err != nil || len(list) != 0 {
		t.Errorf("an absent project's schedules must not fire, got %v, %v", list, err)
	}
	if _, err := st.Session(ctx, sess.ID); err != nil {
		t.Errorf("an absent project's chats must be kept, got %v", err)
	}

	// and comes back, with its history.
	if err := st.ReconcileProjects(ctx, []string{"wolf", "demo"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Project(ctx, "demo"); err != nil {
		t.Errorf("re-listing a project should serve it again, got %v", err)
	}
	if list, err := st.Schedules(ctx, ""); err != nil || len(list) != 1 {
		t.Errorf("its schedules should fire again, got %v, %v", list, err)
	}
}
