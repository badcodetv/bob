package store

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// queueStore serves projects wolf and enc, with a worker in each; newSchedule adds a schedule.
func queueStore(t *testing.T) (st *Store, newSchedule func(project, name string) Schedule) {
	st = newTestStore(t)
	if err := st.ReconcileProjects(t.Context(), []string{"wolf", "enc"}); err != nil {
		t.Fatal(err)
	}
	workers := map[string]string{"wolf": insertWorker(t, st, "wolf", "researcher", "claude"), "enc": insertWorker(t, st, "enc", "writer", "claude")}
	return st, func(project, name string) Schedule {
		t.Helper()
		sch, err := st.CreateSchedule(t.Context(), Schedule{Project: project, Name: name, WorkerID: workers[project],
			Cron: "0 6 * * *", Timezone: "UTC", Message: "go", Enabled: true, KeepSessions: 5})
		if err != nil {
			t.Fatal(err)
		}
		return sch
	}
}

func startRun(t *testing.T, st *Store, sch Schedule) Run {
	t.Helper()
	run, err := st.StartRun(t.Context(), sch.ID, "cron", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return run
}

// A firing queues a run; a firing while the schedule has one queued or running is skipped.
func TestStartRunQueues(t *testing.T) {
	st, newSchedule := queueStore(t)
	sch := newSchedule("wolf", "daily")
	if run := startRun(t, st, sch); run.Status != "queued" || run.FinishedAt != nil {
		t.Fatalf("first firing: %+v, want queued", run)
	}
	skipped := startRun(t, st, sch)
	if skipped.Status != "skipped" || skipped.Detail != "previous run still queued or running" || skipped.FinishedAt == nil {
		t.Errorf("firing while queued: %+v", skipped)
	}
	if _, ok, err := st.NextQueuedRun(t.Context(), "wolf"); !ok || err != nil {
		t.Fatalf("dispatch: %v %v", ok, err)
	}
	if run := startRun(t, st, sch); run.Status != "skipped" {
		t.Errorf("firing while running: %+v", run)
	}
	if _, err := st.StartRun(t.Context(), "00000000-0000-0000-0000-000000000000", "cron", time.Now()); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown schedule: %v", err)
	}
}

// A project runs its oldest queued run only when none of its scheduled runs is running; other
// projects are not held up.
func TestNextQueuedRunOneAtATimePerProject(t *testing.T) {
	st, newSchedule := queueStore(t)
	ctx := t.Context()
	first, second, other := startRun(t, st, newSchedule("wolf", "a")), startRun(t, st, newSchedule("wolf", "b")), startRun(t, st, newSchedule("enc", "c"))

	st.Now = func() time.Time { return time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC) }
	got, ok, err := st.NextQueuedRun(ctx, "wolf")
	if err != nil || !ok || got.ID != first.ID || got.Status != "running" || !got.StartedAt.Equal(st.Now()) {
		t.Fatalf("first dispatch: %+v %v %v; want run %d running, started now", got, ok, err, first.ID)
	}
	if got, ok, err := st.NextQueuedRun(ctx, "wolf"); ok || err != nil {
		t.Fatalf("while wolf has a run going: %+v %v %v; want nothing", got, ok, err)
	}
	if got, ok, err := st.NextQueuedRun(ctx, "enc"); !ok || err != nil || got.ID != other.ID {
		t.Fatalf("enc: %+v %v %v; want its own run, concurrently", got, ok, err)
	}
	if err := st.FinishRun(ctx, first.ID, "ok", "", time.Now()); err != nil {
		t.Fatal(err)
	}
	if got, ok, err := st.NextQueuedRun(ctx, "wolf"); !ok || err != nil || got.ID != second.ID {
		t.Fatalf("after the first finished: %+v %v %v; want run %d", got, ok, err, second.ID)
	}
	if err := st.FinishRun(ctx, second.ID, "ok", "", time.Now()); err != nil {
		t.Fatal(err)
	}
	if got, ok, err := st.NextQueuedRun(ctx, "wolf"); ok || err != nil {
		t.Errorf("queue empty: %+v %v %v", got, ok, err)
	}
}

// Two dispatchers at once (a run finishing as a firing lands) never start two runs of a project.
func TestNextQueuedRunConcurrentCallers(t *testing.T) {
	st, newSchedule := queueStore(t)
	for i, name := range []string{"one", "two", "three", "four", "five"} {
		// Round i: a single queued run, then (from round 1) two queued runs of different schedules.
		a := newSchedule("wolf", name+"-a")
		startRun(t, st, a)
		if i > 0 {
			startRun(t, st, newSchedule("wolf", name+"-b"))
		}
		var wg sync.WaitGroup
		results := make(chan Run, 2)
		for range 2 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				run, ok, err := st.NextQueuedRun(t.Context(), "wolf")
				if err != nil {
					t.Error(err)
				}
				if ok {
					results <- run
				}
			}()
		}
		wg.Wait()
		close(results)
		var got []Run
		for r := range results {
			got = append(got, r)
		}
		if len(got) != 1 {
			t.Fatalf("round %d: %d callers got a run (%+v), want exactly one", i, len(got), got)
		}
		// Finish everything so the next round starts from an idle project.
		if _, err := st.db.Exec(t.Context(), `UPDATE schedule_runs SET status = 'ok', finished_at = now() WHERE status IN ('queued', 'running')`); err != nil {
			t.Fatal(err)
		}
	}
}

// A restart fails the runs it interrupted; queued runs wait for the next dispatch.
func TestFailInterruptedRunsKeepsQueued(t *testing.T) {
	st, newSchedule := queueStore(t)
	running, queued := startRun(t, st, newSchedule("wolf", "a")), startRun(t, st, newSchedule("wolf", "b"))
	if _, ok, err := st.NextQueuedRun(t.Context(), "wolf"); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if err := st.FailInterruptedRuns(t.Context(), time.Now()); err != nil {
		t.Fatal(err)
	}
	status := func(id int64) string {
		var s string
		if err := st.db.QueryRow(t.Context(), `SELECT status FROM schedule_runs WHERE id = $1`, id).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	if got := status(running.ID); got != "failed" {
		t.Errorf("interrupted run: %s, want failed", got)
	}
	if got := status(queued.ID); got != "queued" {
		t.Errorf("queued run: %s, want still queued", got)
	}
	if got, ok, err := st.NextQueuedRun(t.Context(), "wolf"); !ok || err != nil || got.ID != queued.ID {
		t.Errorf("after the restart: %+v %v %v; want the queued run", got, ok, err)
	}
}
