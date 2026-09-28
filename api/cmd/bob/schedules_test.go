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

	"github.com/badcodetv/bob/internal/access"
	"github.com/badcodetv/bob/internal/auth"
	"github.com/badcodetv/bob/internal/broker"
	"github.com/badcodetv/bob/internal/runtime"
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
	testDatabases[st] = u.String()
	t.Cleanup(func() {
		delete(testDatabases, st)
		st.Close()
		admin.Exec(context.Background(), "DROP DATABASE "+name+" WITH (FORCE)")
		admin.Close(context.Background())
	})
	return st
}

// testDatabases is each test store's connection string, for insertWorker's raw SQL: the store
// keeps its pool to itself.
var testDatabases = map[*store.Store]string{}

// insertWorker adds a worker with raw SQL and returns its id. Bob has no worker routes or store
// writes yet; sessions and schedules only need the row to point at. The same helper is in
// api/internal/store/store_test.go (test helpers cannot cross packages).
func insertWorker(t *testing.T, st *store.Store, project, name, engine string) (id string) {
	t.Helper()
	err := testDB(t, st).QueryRow(t.Context(), `INSERT INTO workers (project, name, engine, created_by, updated_by)
		VALUES ($1, $2, $3, 'test', 'test') RETURNING id`, project, name, engine).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// execSQL runs raw SQL against a test store's database: for what Bob cannot yet change through
// its own routes, such as a worker's prompt and settings.
func execSQL(t *testing.T, st *store.Store, sql string, args ...any) {
	t.Helper()
	if _, err := testDB(t, st).Exec(t.Context(), sql, args...); err != nil {
		t.Fatal(err)
	}
}

func testDB(t *testing.T, st *store.Store) *pgx.Conn {
	t.Helper()
	db, err := pgx.Connect(t.Context(), testDatabases[st])
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close(context.Background()) })
	return db
}

// fakeRuntime is a project container's runtime server: it records each turn it is sent, can
// fail them, and holds each turn until released when hold is set. Anything else it is asked for
// is recorded as unexpected — the runtime has no other routes Bob should call.
type fakeRuntime struct {
	mu         sync.Mutex
	turnError  string
	hold       chan struct{}
	started    chan string // session ids, as turns start
	turns      []runtime.TurnRequest
	unexpected []string
}

func (f *fakeRuntime) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.URL.Path == "/health":
	case r.Method == http.MethodPost && r.URL.Path == "/turns":
		var body runtime.TurnRequest
		json.NewDecoder(r.Body).Decode(&body)
		f.turns = append(f.turns, body)
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
	default:
		f.unexpected = append(f.unexpected, r.Method+" "+r.URL.Path)
		http.NotFound(w, r)
	}
}

// sent returns the turns the runtime has been sent, and any other requests it was sent.
func (f *fakeRuntime) sent() (turns []runtime.TurnRequest, unexpected []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]runtime.TurnRequest(nil), f.turns...), append([]string(nil), f.unexpected...)
}

type fakeContainers struct{ base string }

func (c fakeContainers) Ensure(context.Context, store.Project) (string, error) { return c.base, nil }
func (c fakeContainers) Recreate(context.Context, string) error                { return nil }
func (c fakeContainers) Destroy(context.Context, string) error                 { return nil }
func (c fakeContainers) Revive(string)                                         {}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time  { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) set(t time.Time) { c.mu.Lock(); c.t = t; c.mu.Unlock() }

// newScheduleApp serves project wolf, with one worker, "researcher". researcherID is its id.
func newScheduleApp(t *testing.T) (*app, *fakeRuntime, *clock) {
	st := testStore(t)
	rt := &fakeRuntime{}
	srv := httptest.NewServer(rt)
	t.Cleanup(srv.Close)
	c := &clock{t: time.Now()}
	a := &app{auth: &auth.Auth{Secret: []byte("0123456789abcdef")}, store: st, broker: broker.New(), runtime: fakeContainers{srv.URL}, now: c.now, turns: map[string]context.CancelFunc{}}
	if err := st.ReconcileProjects(t.Context(), []string{"wolf"}); err != nil {
		t.Fatal(err)
	}
	researcherID[a] = insertWorker(t, st, "wolf", "researcher", "claude")
	return a, rt, c
}

// researcherID is the id of each schedule app's "researcher" worker.
var researcherID = map[*app]string{}

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
	x.Project, x.WorkerID, x.Message, x.Enabled = "wolf", researcherID[a], "do the research", true
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

func TestScheduleNoOverlap(t *testing.T) {
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

	close(rt.hold)
	a.scheduled.Wait()
	rs := runs(t, a, sch)
	if got := statuses(rs); got != "cron:ok cron:skipped manual:skipped" {
		t.Fatalf("after the run: %s", got)
	}
	if rs[0].SessionID == nil || *rs[0].SessionID != sessionID || rs[0].FinishedAt == nil {
		t.Errorf("finished run: %+v", rs[0])
	}
	if turns, unexpected := rt.sent(); len(turns) != 1 || len(unexpected) != 0 {
		t.Errorf("runtime was sent %d turn(s) and %v, want one turn and nothing else", len(turns), unexpected)
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

	rt.turnError = "model overloaded"
	failed := fire()
	if failed.Status != "failed" || failed.Detail != "model overloaded" || failed.SessionID == nil {
		t.Errorf("turn failure: %+v", failed)
	}

	rt.turnError = ""
	ok := fire()
	if ok.Status != "ok" {
		t.Errorf("ok run: %+v", ok)
	}
	// keep_sessions 1: the failed run's session was deleted, the newest kept. Deleting a session
	// is Bob's alone: the runtime keeps nothing per session.
	if _, err := a.store.Session(t.Context(), *failed.SessionID); err != store.ErrNotFound {
		t.Errorf("old session still there: %v", err)
	}
	if _, unexpected := rt.sent(); len(unexpected) != 0 {
		t.Errorf("runtime was asked for %v", unexpected)
	}
	if _, err := a.store.Session(t.Context(), *ok.SessionID); err != nil {
		t.Errorf("newest session: %v", err)
	}
	if rs := runs(t, a, sch); rs[0].SessionID != nil {
		t.Errorf("a pruned session's run should lose its link: %+v", rs[0])
	}
}

func TestScheduledSessionsAreNamed(t *testing.T) {
	a, _, _ := newScheduleApp(t)
	sch := create(t, a, store.Schedule{Name: "daily-research", Cron: "0 6 * * *"})
	if _, err := a.fire(t.Context(), sch, "manual"); err != nil {
		t.Fatal(err)
	}
	a.scheduled.Wait()
	id := researcherID[a]
	a.store.CreateSession(t.Context(), "wolf", &id, "researcher", "claude", "", "")
	list, err := a.store.Sessions(t.Context(), "wolf")
	if err != nil || len(list) != 2 {
		t.Fatalf("sessions: %v %v", list, err)
	}
	names := map[string]bool{list[0].Schedule: true, list[1].Schedule: true}
	if !names["daily-research"] || !names[""] {
		t.Errorf("schedule names on the list: %q, %q", list[0].Schedule, list[1].Schedule)
	}
}

func TestTurningAScheduleBackOnDoesNotCatchUp(t *testing.T) {
	a, _, c := newScheduleApp(t)
	sch := create(t, a, store.Schedule{Name: "hourly-off", Cron: "0 * * * *"})
	sch.Enabled = false
	sch, _ = a.store.UpdateSchedule(t.Context(), sch)

	// Off for three hours, turned back on through the API at +3h30: the 3:00 firing is not run.
	c.set(sch.CreatedAt.Add(3*time.Hour + 30*time.Minute))
	people := access.Map{"admin@example.com": {"*"}}
	a.auth, a.access = &auth.Auth{Secret: []byte("0123456789abcdef"), Allowed: people.Allowed}, people
	a.projectOf = a.storeProjectOf
	rec := httptest.NewRecorder()
	a.auth.SetSession(rec, auth.User{Email: "admin@example.com"})
	req := httptest.NewRequest("PATCH", "/api/schedules/"+sch.ID, strings.NewReader(`{"enabled":true}`))
	req.AddCookie(rec.Result().Cookies()[0])
	res := httptest.NewRecorder()
	a.routes().ServeHTTP(res, req)
	if res.Code != 200 {
		t.Fatalf("enable: %d %s", res.Code, res.Body)
	}
	a.tick(t.Context())
	a.scheduled.Wait()
	if got := statuses(runs(t, a, sch)); got != "" {
		t.Errorf("turned back on: %s, want nothing until the next firing", got)
	}
	// The next hour fires as usual.
	// (From the hour after it was turned back on: counting from the creation hour went backwards
	// in time whenever the test ran after half past the hour.)
	c.set(sch.CreatedAt.Add(3*time.Hour + 30*time.Minute).Truncate(time.Hour).Add(time.Hour + time.Minute))
	a.tick(t.Context())
	a.scheduled.Wait()
	if got := statuses(runs(t, a, sch)); got != "cron:ok" {
		t.Errorf("next firing: %s", got)
	}
}
