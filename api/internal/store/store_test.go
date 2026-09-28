package store

import (
	"context"
	"errors"
	"fmt"
	neturl "net/url"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// newTestStore opens a fresh database on the Postgres named by BOB_TEST_DATABASE_URL (a server
// where the user may create databases, e.g. the shared local one), dropped when the test ends.
// It is testStore in api/cmd/bob/schedules_test.go: test helpers cannot cross packages.
func newTestStore(t *testing.T) *Store {
	url := os.Getenv("BOB_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("BOB_TEST_DATABASE_URL not set (e.g. postgres://bob:bob@127.0.0.1:5433/bob)")
	}
	admin, err := pgx.Connect(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("bob_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec(t.Context(), "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	u, err := neturl.Parse(url)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	check, err := pgx.Connect(t.Context(), u.String())
	if err != nil {
		t.Fatal(err)
	}
	var db string
	check.QueryRow(t.Context(), "SELECT current_database()").Scan(&db)
	check.Close(t.Context())
	if db != name {
		t.Fatalf("connected to %q, want the throwaway %q", db, name)
	}
	st, err := Open(t.Context(), u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		st.Close()
		admin.Exec(context.Background(), "DROP DATABASE "+name+" WITH (FORCE)")
		admin.Close(context.Background())
	})
	return st
}

// insertWorker adds a worker with raw SQL and returns its id. The store's own worker functions
// come later; tests of sessions and schedules only need the row to point at. The same helper is
// in api/cmd/bob/schedules_test.go.
func insertWorker(t *testing.T, st *Store, project, name, engine string) (id string) {
	t.Helper()
	err := st.db.QueryRow(t.Context(), `INSERT INTO workers (project, name, engine, created_by, updated_by)
		VALUES ($1, $2, $3, 'test', 'test') RETURNING id`, project, name, engine).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// A fresh database is exactly the baseline: one migration, and the tables it creates.
func TestFreshDatabaseIsTheBaseline(t *testing.T) {
	st := newTestStore(t)
	rows, err := st.db.Query(t.Context(), `SELECT name FROM schema_migrations ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	applied, err := collect(rows, func(r pgx.Row) (string, error) { var n string; return n, r.Scan(&n) })
	wantMigrations := []string{"001_baseline.sql", "002_worker_labels.sql"}
	if err != nil || !reflect.DeepEqual(applied, wantMigrations) {
		t.Errorf("migrations = %v, %v; want %v", applied, err, wantMigrations)
	}
	rows, err = st.db.Query(t.Context(), `SELECT table_name FROM information_schema.tables
		WHERE table_schema = 'public' AND table_name <> 'schema_migrations' ORDER BY table_name`)
	if err != nil {
		t.Fatal(err)
	}
	tables, err := collect(rows, func(r pgx.Row) (string, error) { var n string; return n, r.Scan(&n) })
	want := fmt.Sprint([]string{"events", "project_prompt_versions", "projects", "schedule_runs", "schedules",
		"sessions", "settings", "worker_versions", "workers"})
	if err != nil || fmt.Sprint(tables) != want {
		t.Errorf("tables = %v, %v; want %s", tables, err, want)
	}
}

// ReconcileProjects takes names only: listed ones are served, the rest are marked absent and kept,
// and a project's prompt survives being reconciled — it is edited in Bob, not in the deploy.
func TestReconcileProjectsByNames(t *testing.T) {
	st := newTestStore(t)
	ctx := t.Context()
	if err := st.ReconcileProjects(ctx, []string{"wolf", "enc"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(ctx, `UPDATE projects SET prompt = 'be brief' WHERE name = 'enc'`); err != nil {
		t.Fatal(err)
	}

	if err := st.ReconcileProjects(ctx, []string{"wolf"}); err != nil {
		t.Fatal(err)
	}
	if ps, err := st.Projects(ctx); err != nil || len(ps) != 1 || ps[0].Name != "wolf" {
		t.Errorf("projects = %v, %v; want just wolf", ps, err)
	}
	if _, err := st.Project(ctx, "enc"); !errors.Is(err, ErrNotFound) {
		t.Errorf("an absent project should not be found, got %v", err)
	}
	if absent, err := st.AbsentProjects(ctx); err != nil || len(absent) != 1 || absent[0] != "enc" {
		t.Errorf("absent = %v, %v; want [enc]", absent, err)
	}

	if err := st.ReconcileProjects(ctx, []string{"wolf", "enc"}); err != nil {
		t.Fatal(err)
	}
	p, err := st.Project(ctx, "enc")
	if err != nil || p.Prompt != "be brief" {
		t.Errorf("enc = %+v, %v; want it back with its prompt", p, err)
	}
	if err := st.ReconcileProjects(ctx, []string{"Not A Name"}); err == nil {
		t.Error("a name the table's check rejects should fail the reconcile")
	}
}

// Deleting a worker keeps its chats, which lose the link (and say so on the list), and deletes
// its schedules. Its version history has no foreign key, so it outlives the worker.
func TestDeletingAWorkerCascades(t *testing.T) {
	st := newTestStore(t)
	ctx := t.Context()
	if err := st.ReconcileProjects(ctx, []string{"wolf"}); err != nil {
		t.Fatal(err)
	}
	id := insertWorker(t, st, "wolf", "researcher", "claude")
	sess, err := st.CreateSession(ctx, "wolf", &id, "researcher", "claude", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if sess.WorkerID == nil || *sess.WorkerID != id {
		t.Fatalf("session worker_id = %v, want %s", sess.WorkerID, id)
	}
	if _, err := st.CreateSession(ctx, "wolf", nil, "", "codex", "", ""); err != nil { // a plain chat
		t.Fatal(err)
	}
	sch, err := st.CreateSchedule(ctx, Schedule{Project: "wolf", Name: "daily", WorkerID: id, Cron: "0 6 * * *",
		Timezone: "UTC", Message: "go", Enabled: true, KeepSessions: 5})
	if err != nil {
		t.Fatal(err)
	}
	if sch.Worker != "researcher" {
		t.Errorf("schedule worker = %q, want the worker's name joined in", sch.Worker)
	}
	if _, err := st.db.Exec(ctx, `INSERT INTO worker_versions (worker_id, project, name, action, snapshot, why, changed_by)
		VALUES ($1, 'wolf', 'researcher', 'create', '{}', 'test', 'test')`, id); err != nil {
		t.Fatal(err)
	}

	if _, err := st.db.Exec(ctx, `DELETE FROM workers WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	got, err := st.Session(ctx, sess.ID)
	if err != nil || got.WorkerID != nil || got.Worker != "researcher" {
		t.Errorf("session after its worker went = %+v, %v; want kept, worker_id null, name kept", got, err)
	}
	list, err := st.Sessions(ctx, "wolf")
	if err != nil || len(list) != 2 {
		t.Fatalf("sessions = %v, %v", list, err)
	}
	for _, s := range list {
		if want := s.ID == sess.ID; s.WorkerRemoved != want {
			t.Errorf("session %s (worker %q) worker_removed = %v, want %v", s.ID, s.Worker, s.WorkerRemoved, want)
		}
	}
	if _, err := st.Schedule(ctx, sch.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("a deleted worker's schedule should be gone, got %v", err)
	}
	var versions int
	if err := st.db.QueryRow(ctx, `SELECT count(*) FROM worker_versions WHERE worker_id = $1`, id).Scan(&versions); err != nil || versions != 1 {
		t.Errorf("versions after delete = %d, %v; want 1", versions, err)
	}
}
