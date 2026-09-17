package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	neturl "net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/badcodetv/bob/internal/broker"
	"github.com/badcodetv/bob/internal/store"
	"github.com/jackc/pgx/v5"
)

// testStore opens a fresh database on the Postgres named by BOB_TEST_DATABASE_URL (a server
// where the user may create databases, e.g. docker compose's), dropped when the test ends.
func testStore(t *testing.T) *store.Store {
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
	st, err := store.Open(t.Context(), u.String())
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

// fakeRuntime is a project container's runtime server: it counts git syncs, can fail them, and
// holds each turn until released when hold is set.
type fakeRuntime struct {
	mu        sync.Mutex
	syncs     int
	syncError string
	workers   []string
	turnError string
	hold      chan struct{}
	started   chan string // session ids, as turns start
	removed   []string
}

func (f *fakeRuntime) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.URL.Path == "/health":
	case r.URL.Path == "/workers" || r.URL.Path == "/sync":
		if r.URL.Path == "/sync" {
			f.syncs++
		}
		ws := []map[string]string{}
		for _, name := range f.workers {
			ws = append(ws, map[string]string{"name": name, "engine": "claude"})
		}
		sync := map[string]any{"ok": f.syncError == "", "error": f.syncError, "commit": "abc"}
		json.NewEncoder(w).Encode(map[string]any{"sync": sync, "workers": ws})
	case r.URL.Path == "/turns":
		var body struct {
			SessionID string `json:"session_id"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		hold, turnError := f.hold, f.turnError
		if f.started != nil {
			f.started <- body.SessionID
		}
		f.mu.Unlock()
		if hold != nil {
			<-hold
		}
		fmt.Fprintf(w, `{"engine":"claude","event":{"type":"system","subtype":"init","session_id":"h-%s"}}`+"\n", body.SessionID)
		fmt.Fprintf(w, `{"done":true,"harness_session_id":"h-%s","error":%q}`+"\n", body.SessionID, turnError)
		f.mu.Lock()
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/sessions/"):
		f.removed = append(f.removed, strings.TrimPrefix(r.URL.Path, "/sessions/"))
		w.Write([]byte(`{"ok":true}`))
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeRuntime) count() (syncs int, removed []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.syncs, append([]string(nil), f.removed...)
}

type fakeContainers struct{ base string }

func (c fakeContainers) Ensure(context.Context, store.Project) (string, error) { return c.base, nil }
func (c fakeContainers) Recreate(context.Context, string) error                { return nil }
func (c fakeContainers) Destroy(context.Context, string) error                 { return nil }

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time  { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) set(t time.Time) { c.mu.Lock(); c.t = t; c.mu.Unlock() }

func newScheduleApp(t *testing.T) (*app, *fakeRuntime, *clock) {
	st := testStore(t)
	rt := &fakeRuntime{workers: []string{"researcher"}}
	srv := httptest.NewServer(rt)
	t.Cleanup(srv.Close)
	c := &clock{t: time.Now()}
	a := &app{store: st, broker: broker.New(), runtime: fakeContainers{srv.URL}, now: c.now, turns: map[string]context.CancelFunc{}}
	if _, err := st.CreateProject(t.Context(), store.Project{Name: "wolf"}); err != nil {
		t.Fatal(err)
	}
	return a, rt, c
}

func runs(t *testing.T, a *app, sch store.Schedule) []store.Run {
	rs, err := a.store.Runs(t.Context(), sch.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	for i, j := 0, len(rs)-1; i < j; i, j = i+1, j-1 { // oldest first
		rs[i], rs[j] = rs[j], rs[i]
	}
	return rs
}

func statuses(rs []store.Run) string {
	var out []string
	for _, r := range rs {
		out = append(out, r.Trigger+":"+r.Status)
	}
	return strings.Join(out, " ")
}

func create(t *testing.T, a *app, x store.Schedule) store.Schedule {
	x.Project, x.Worker, x.Message, x.Enabled = "wolf", "researcher", "do the research", true
	if x.Timezone == "" {
		x.Timezone = "UTC"
	}
	if x.KeepSessions == 0 {
		x.KeepSessions = 30
	}
	x, err := a.store.CreateSchedule(t.Context(), x)
	if err != nil {
		t.Fatal(err)
	}
	return x
}

func TestScheduleNoOverlapAndPostRunSync(t *testing.T) {
	a, rt, c := newScheduleApp(t)
	rt.hold, rt.started = make(chan struct{}), make(chan string, 4)
	sch := create(t, a, store.Schedule{Name: "every-15", Cron: "*/15 * * * *"})
	firing := sch.CreatedAt.Truncate(15 * time.Minute).Add(15 * time.Minute)

	c.set(firing.Add(10 * time.Second))
	a.tick(t.Context())
	sessionID := <-rt.started // the turn is running, held by the fake runtime

	c.set(firing.Add(15*time.Minute + 5*time.Second))
	a.tick(t.Context())
	if got := statuses(runs(t, a, sch)); got != "cron:running cron:skipped" {
		t.Fatalf("second firing during the first run: %s", got)
	}
	if r := runs(t, a, sch)[1]; r.Detail != "previous run still running" {
		t.Errorf("skip detail = %q", r.Detail)
	}
	if run, err := a.fire(t.Context(), sch, "manual"); err != nil || run.Status != "skipped" {
		t.Errorf("manual run during a run: %+v, %v", run, err)
	}
	if syncs, _ := rt.count(); syncs != 1 {
		t.Errorf("syncs before the turn finished = %d, want 1", syncs)
	}

	close(rt.hold)
	a.scheduled.Wait()
	rs := runs(t, a, sch)
	if got := statuses(rs); got != "cron:ok cron:skipped manual:skipped" {
		t.Fatalf("after the run: %s", got)
	}
	if rs[0].SessionID == nil || *rs[0].SessionID != sessionID || rs[0].FinishedAt == nil {
		t.Errorf("finished run: %+v", rs[0])
	}
	if syncs, _ := rt.count(); syncs != 2 {
		t.Errorf("syncs = %d, want 2 (before and after the run)", syncs)
	}
	sess, _ := a.store.Session(t.Context(), sessionID)
	if sess.Worker != "researcher" || sess.HarnessSessionID != "h-"+sessionID {
		t.Errorf("scheduled session: %+v", sess)
	}
	events, _ := a.store.Events(t.Context(), sessionID, 0)
	if len(events) == 0 || events[0].Kind != "bob.user_message" || events[len(events)-1].Kind != "bob.turn_done" {
		t.Errorf("events: %v", events)
	}

	// Nothing more is due at the same minute, and the next firing runs again.
	a.tick(t.Context())
	c.set(firing.Add(30*time.Minute + 5*time.Second))
	rt.hold = nil
	a.tick(t.Context())
	a.scheduled.Wait()
	if got := statuses(runs(t, a, sch)); got != "cron:ok cron:skipped manual:skipped cron:ok" {
		t.Errorf("third firing: %s", got)
	}
}

func TestScheduleMissedFirings(t *testing.T) {
	a, _, c := newScheduleApp(t)
	recent := create(t, a, store.Schedule{Name: "hourly", Cron: "0 * * * *"})
	created := recent.CreatedAt

	// Bob was down for three hours: three hourly firings missed, and one run makes up for them.
	c.set(created.Add(3*time.Hour + 30*time.Minute))
	a.tick(t.Context())
	a.scheduled.Wait()
	a.tick(t.Context())
	a.scheduled.Wait()
	if got := statuses(runs(t, a, recent)); got != "cron:ok" {
		t.Errorf("catch-up within 6 hours: %s", got)
	}

	// A daily firing missed by 8 hours is recorded as skipped, not run, and only once.
	firing := created.Add(22 * time.Hour).Truncate(time.Minute)
	daily := create(t, a, store.Schedule{Name: "daily", Cron: fmt.Sprintf("%d %d * * *", firing.UTC().Minute(), firing.UTC().Hour())})
	c.set(firing.Add(8 * time.Hour))
	a.tick(t.Context())
	a.tick(t.Context())
	a.scheduled.Wait()
	rs := runs(t, a, daily)
	if got := statuses(rs); got != "cron:skipped" || !strings.Contains(rs[0].Detail, "missed the firing") {
		t.Errorf("missed by 8 hours: %s %q", got, rs[0].Detail)
	}

	// Paused: nothing fires, and nothing is recorded.
	before := statuses(runs(t, a, recent))
	a.store.SetSchedulesPaused(t.Context(), true)
	c.set(c.now().Add(time.Hour))
	a.tick(t.Context())
	a.scheduled.Wait()
	if got := statuses(runs(t, a, recent)); got != before {
		t.Errorf("while paused: %s", got)
	}
}

func TestScheduleFailuresAndPruning(t *testing.T) {
	a, rt, _ := newScheduleApp(t)
	sch := create(t, a, store.Schedule{Name: "manual", Cron: "0 6 * * *", KeepSessions: 1})
	fire := func() store.Run {
		t.Helper()
		_, err := a.fire(t.Context(), sch, "manual")
		if err != nil {
			t.Fatal(err)
		}
		a.scheduled.Wait()
		rs := runs(t, a, sch)
		return rs[len(rs)-1]
	}

	rt.syncError = "fatal: repository not found"
	if r := fire(); r.Status != "failed" || r.SessionID != nil || r.Detail != "git sync failed: fatal: repository not found" {
		t.Errorf("sync failure: %+v", r)
	}
	rt.syncError, rt.workers = "", []string{"someone-else"}
	if r := fire(); r.Status != "failed" || r.Detail != `worker "researcher" not found in git` {
		t.Errorf("missing worker: %+v", r)
	}
	rt.workers, rt.turnError = []string{"researcher"}, "model overloaded"
	failed := fire()
	if failed.Status != "failed" || failed.Detail != "model overloaded" || failed.SessionID == nil {
		t.Errorf("turn failure: %+v", failed)
	}

	rt.turnError = ""
	ok := fire()
	if ok.Status != "ok" {
		t.Errorf("ok run: %+v", ok)
	}
	// keep_sessions 1: the failed run's session was deleted with its worktree, the newest kept.
	if _, err := a.store.Session(t.Context(), *failed.SessionID); err != store.ErrNotFound {
		t.Errorf("old session still there: %v", err)
	}
	if _, removed := rt.count(); len(removed) != 1 || removed[0] != *failed.SessionID {
		t.Errorf("worktrees removed: %v", removed)
	}
	if _, err := a.store.Session(t.Context(), *ok.SessionID); err != nil {
		t.Errorf("newest session: %v", err)
	}
	if rs := runs(t, a, sch); rs[2].SessionID != nil {
		t.Errorf("a pruned session's run should lose its link: %+v", rs[2])
	}
}

func TestScheduledSessionsAreNamed(t *testing.T) {
	a, _, _ := newScheduleApp(t)
	sch := create(t, a, store.Schedule{Name: "daily-research", Cron: "0 6 * * *"})
	if _, err := a.fire(t.Context(), sch, "manual"); err != nil {
		t.Fatal(err)
	}
	a.scheduled.Wait()
	a.store.CreateSession(t.Context(), "wolf", "researcher", "claude", "", "")
	list, err := a.store.Sessions(t.Context(), "wolf")
	if err != nil || len(list) != 2 {
		t.Fatalf("sessions: %v %v", list, err)
	}
	names := map[string]bool{list[0].Schedule: true, list[1].Schedule: true}
	if !names["daily-research"] || !names[""] {
		t.Errorf("schedule names on the list: %q, %q", list[0].Schedule, list[1].Schedule)
	}
}
