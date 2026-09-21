package main

import (
	"errors"
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

// TestAbsentProject checks what happens to a project the projects file stops listing: it is not
// served and its schedules do not fire, but nothing of it is deleted — re-listing it brings its
// chats back. Projects are deployment configuration; their history is not.
func TestAbsentProject(t *testing.T) {
	st := testStore(t)
	ctx := t.Context()
	wolf := store.Project{Name: "wolf", RepoURL: "https://example.invalid/wolf", RepoRef: "main"}
	demo := store.Project{Name: "demo", RepoURL: "https://example.invalid/demo", RepoRef: "main"}
	if err := st.ReconcileProjects(ctx, []store.Project{wolf, demo}); err != nil {
		t.Fatal(err)
	}
	sess, err := st.CreateSession(ctx, "demo", "w", "claude", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateSchedule(ctx, store.Schedule{Project: "demo", Name: "daily", Worker: "w", Cron: "0 6 * * *", Timezone: "UTC", Message: "go", Enabled: true, KeepSessions: 5}); err != nil {
		t.Fatal(err)
	}

	// demo leaves the file.
	if err := st.ReconcileProjects(ctx, []store.Project{wolf}); err != nil {
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
	if err := st.ReconcileProjects(ctx, []store.Project{wolf, demo}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Project(ctx, "demo"); err != nil {
		t.Errorf("re-listing a project should serve it again, got %v", err)
	}
	if list, err := st.Schedules(ctx, ""); err != nil || len(list) != 1 {
		t.Errorf("its schedules should fire again, got %v, %v", list, err)
	}
}
