package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/badcodetv/bob/internal/auth"
	"github.com/badcodetv/bob/internal/schedule"
	"github.com/badcodetv/bob/internal/store"
)

// scheduleLoop fires due schedules once a minute until ctx ends. Runs left "running" by a
// previous process are marked failed first, so they don't block their schedules forever; runs it
// left queued start on the first tick.
func (a *app) scheduleLoop(ctx context.Context) {
	if err := a.store.FailInterruptedRuns(ctx, a.now()); err != nil {
		log.Printf("schedules: %v", err)
	}
	for {
		a.tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(time.Now().Truncate(time.Minute).Add(time.Minute + time.Second))):
		}
	}
}

// tick starts every enabled schedule whose cron has fired since it last fired (or was created).
// Several missed firings run once; a firing missed by more than schedule.CatchUp is recorded as
// skipped instead. Nothing fires while schedules are paused. Every project's queue is dispatched
// first, paused or not: a queued run was already accepted (a restart, or a missed dispatch, can
// leave one waiting), just as Run now works while paused.
func (a *app) tick(ctx context.Context) {
	if projects, err := a.store.Projects(ctx); err != nil {
		log.Printf("schedules: %v", err)
	} else {
		for _, p := range projects {
			a.dispatch(ctx, p.Name)
		}
	}
	if paused, err := a.store.SchedulesPaused(ctx); err != nil || paused {
		if err != nil {
			log.Printf("schedules: %v", err)
		}
		return
	}
	list, err := a.store.Schedules(ctx, "")
	if err != nil {
		log.Printf("schedules: %v", err)
		return
	}
	now := a.now()
	for _, sch := range list {
		spec, err := schedule.Parse(sch.Cron, sch.Timezone)
		if err != nil {
			log.Printf("schedule %s/%s: %v", sch.Project, sch.Name, err)
			continue
		}
		last, err := a.store.LastCronRun(ctx, sch.ID)
		if err != nil {
			log.Printf("schedule %s/%s: %v", sch.Project, sch.Name, err)
			continue
		}
		// Firings are counted from the latest of: the last cron firing, creation, and when it was
		// last turned on or retimed — so turning a schedule back on never runs what it missed while off.
		for _, t := range []time.Time{sch.CreatedAt, sch.ChangedAt} {
			if t.After(last) {
				last = t
			}
		}
		due, ok := spec.Due(last, now)
		if !ok {
			continue
		}
		if now.Sub(due) > schedule.CatchUp {
			detail := fmt.Sprintf("missed the firing at %s: more than %v ago (Bob was not running, or schedules were paused)", due.UTC().Format(time.RFC3339), schedule.CatchUp)
			if _, err := a.store.SkipRun(ctx, sch.ID, "cron", detail, now); err != nil {
				log.Printf("schedule %s/%s: %v", sch.Project, sch.Name, err)
			}
			continue
		}
		if _, err := a.fire(ctx, sch, "cron"); err != nil {
			log.Printf("schedule %s/%s: %v", sch.Project, sch.Name, err)
		}
	}
}

// fire records a queued run and dispatches the schedule's project — or records the firing as
// skipped when the schedule already has a run queued or running. The run is returned as recorded.
func (a *app) fire(ctx context.Context, sch store.Schedule, trigger string) (store.Run, error) {
	run, err := a.store.StartRun(ctx, sch.ID, trigger, a.now())
	if err != nil || run.Status == "skipped" {
		return run, err
	}
	a.dispatch(ctx, sch.Project)
	return run, nil
}

// dispatch starts the project's oldest queued run in the background, if the project has no
// scheduled run going (store.NextQueuedRun), so a project runs its scheduled runs one at a time.
// When that run finishes it dispatches again, for the next one in the queue. People's chats
// never pass through here.
func (a *app) dispatch(ctx context.Context, project string) {
	run, ok, err := a.store.NextQueuedRun(ctx, project)
	if err != nil {
		log.Printf("schedules: dispatching %s: %v", project, err)
	}
	if !ok {
		return
	}
	ctx = context.WithoutCancel(ctx)
	a.scheduled.Add(1)
	go func() {
		defer a.scheduled.Done()
		if sch, err := a.store.Schedule(ctx, run.ScheduleID); err != nil {
			if err := a.store.FinishRun(ctx, run.ID, "failed", fmt.Sprintf("schedule: %v", err), a.now()); err != nil {
				log.Printf("schedule run %d: %v", run.ID, err)
			}
		} else {
			a.execute(ctx, sch, run)
		}
		a.dispatch(ctx, project)
	}()
}

// execute is one run: start a session on the schedule's worker with its message, wait for the
// turn, then prune old sessions.
func (a *app) execute(ctx context.Context, sch store.Schedule, run store.Run) {
	defer a.hold(sch.Project)()
	status, detail := "failed", ""
	defer func() {
		if err := a.store.FinishRun(ctx, run.ID, status, detail, a.now()); err != nil {
			log.Printf("schedule %s/%s run %d: %v", sch.Project, sch.Name, run.ID, err)
		}
	}()
	worker, err := a.store.WorkerByID(ctx, sch.WorkerID)
	if err != nil {
		detail = fmt.Sprintf("worker %q: %v", sch.Worker, err)
		return
	}
	sess, err := a.store.CreateSession(ctx, sch.Project, &sch.WorkerID, worker.Name, worker.Engine, "", "")
	if err == nil {
		err = a.store.StartScheduledSession(ctx, run.ID, sch.ID, sess.ID)
	}
	if err != nil {
		detail = err.Error()
		return
	}
	_, turn, err := a.startTurn(ctx, sess, auth.User{Email: "schedule:" + sch.ID, Name: sch.Name}, sch.Message)
	if err == nil {
		err = turn()
	}
	if err != nil {
		detail = err.Error()
	} else {
		status = "ok"
	}

	expired, err := a.store.ExpiredScheduledSessions(ctx, sch.ID, sch.KeepSessions)
	if err != nil {
		log.Printf("schedule %s/%s: %v", sch.Project, sch.Name, err)
	}
	for _, id := range expired {
		if old, err := a.store.Session(ctx, id); err == nil {
			if err := a.removeSession(ctx, old); err != nil {
				log.Printf("schedule %s/%s: deleting old session %s: %v", sch.Project, sch.Name, id, err)
			}
		}
	}
}

// scheduleView is a schedule with when it fires next and how its last run went.
type scheduleView struct {
	store.Schedule
	NextAt  *time.Time `json:"next_at"`
	LastRun *store.Run `json:"last_run"`
}

func (a *app) listSchedules(w http.ResponseWriter, r *http.Request) {
	list, err := a.store.Schedules(r.Context(), r.PathValue("project"))
	if err != nil {
		reply(w, nil, err)
		return
	}
	paused, err := a.store.SchedulesPaused(r.Context())
	if err != nil {
		reply(w, nil, err)
		return
	}
	views := []scheduleView{}
	for _, sch := range list {
		v := scheduleView{Schedule: sch}
		if spec, err := schedule.Parse(sch.Cron, sch.Timezone); err == nil && sch.Enabled {
			next := spec.Next(a.now())
			v.NextAt = &next
		}
		if runs, err := a.store.Runs(r.Context(), sch.ID, 1); err == nil && len(runs) == 1 {
			v.LastRun = &runs[0]
		}
		views = append(views, v)
	}
	reply(w, map[string]any{"schedules": views, "paused": paused}, nil)
}

// scheduleBody is a schedule as the API accepts it; absent fields keep their value on PATCH. The
// worker is named, and resolved to its id by createSchedule/updateSchedule (400 if unknown).
type scheduleBody struct {
	Name         *string `json:"name"`
	Worker       *string `json:"worker"`
	Cron         *string `json:"cron"`
	Timezone     *string `json:"timezone"`
	Message      *string `json:"message"`
	Enabled      *bool   `json:"enabled"`
	KeepSessions *int    `json:"keep_sessions"`
}

func (b scheduleBody) apply(x *store.Schedule) {
	set := func(dst *string, src *string) {
		if src != nil {
			*dst = strings.TrimSpace(*src)
		}
	}
	set(&x.Name, b.Name)
	set(&x.Cron, b.Cron)
	set(&x.Timezone, b.Timezone)
	if b.Message != nil {
		x.Message = *b.Message
	}
	if b.Enabled != nil {
		x.Enabled = *b.Enabled
	}
	if b.KeepSessions != nil {
		x.KeepSessions = *b.KeepSessions
	}
}

var scheduleName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,40}$`)

func validSchedule(x store.Schedule) error {
	switch {
	case !scheduleName.MatchString(x.Name):
		return errors.New("name: lower-case letters, digits and -, starting with a letter or digit")
	case x.WorkerID == "":
		return errors.New("worker_id is required")
	case strings.TrimSpace(x.Message) == "":
		return errors.New("message is required")
	case x.KeepSessions < 1:
		return errors.New("keep_sessions must be at least 1")
	}
	_, err := schedule.Parse(x.Cron, x.Timezone)
	return err
}

func (a *app) createSchedule(w http.ResponseWriter, r *http.Request) {
	var body scheduleBody
	if !decode(w, r, &body) {
		return
	}
	project := r.PathValue("project")
	x := store.Schedule{Project: project, Timezone: "UTC", Enabled: true, KeepSessions: 30}
	body.apply(&x)
	if body.Worker != nil {
		id, ok := a.findWorkerID(w, r, project, *body.Worker)
		if !ok {
			return
		}
		x.WorkerID = id
	}
	if err := validSchedule(x); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	x, err := a.store.CreateSchedule(r.Context(), x)
	reply(w, x, err)
}

func (a *app) updateSchedule(w http.ResponseWriter, r *http.Request) {
	var body scheduleBody
	if !decode(w, r, &body) {
		return
	}
	x, err := a.store.Schedule(r.Context(), r.PathValue("schedule"))
	if err != nil {
		reply(w, nil, err)
		return
	}
	before := x
	body.apply(&x)
	if body.Worker != nil {
		id, ok := a.findWorkerID(w, r, x.Project, *body.Worker)
		if !ok {
			return
		}
		x.WorkerID = id
	}
	if err := validSchedule(x); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if (x.Enabled && !before.Enabled) || x.Cron != before.Cron || x.Timezone != before.Timezone {
		x.ChangedAt = a.now()
	}
	x, err = a.store.UpdateSchedule(r.Context(), x)
	reply(w, x, err)
}

func (a *app) deleteSchedule(w http.ResponseWriter, r *http.Request) {
	reply(w, map[string]bool{"ok": true}, a.store.DeleteSchedule(r.Context(), r.PathValue("schedule")))
}

// runScheduleNow queues a run of a schedule, whether or not it is enabled or schedules are
// paused: a person asked for it. It starts when the project has no other scheduled run going,
// and is skipped if the schedule already has a run queued or running.
func (a *app) runScheduleNow(w http.ResponseWriter, r *http.Request) {
	sch, err := a.store.Schedule(r.Context(), r.PathValue("schedule"))
	if err != nil {
		reply(w, nil, err)
		return
	}
	run, err := a.fire(context.WithoutCancel(r.Context()), sch, "manual")
	if err == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
	}
	reply(w, run, err)
}

func (a *app) listRuns(w http.ResponseWriter, r *http.Request) {
	runs, err := a.store.Runs(r.Context(), r.PathValue("schedule"), 50)
	reply(w, map[string]any{"runs": runs}, err)
}

func (a *app) getSettings(w http.ResponseWriter, r *http.Request) {
	paused, err := a.store.SchedulesPaused(r.Context())
	reply(w, map[string]bool{"schedules_paused": paused}, err)
}

func (a *app) updateSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SchedulesPaused *bool `json:"schedules_paused"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.SchedulesPaused != nil {
		if err := a.store.SetSchedulesPaused(r.Context(), *body.SchedulesPaused); err != nil {
			reply(w, nil, err)
			return
		}
	}
	a.getSettings(w, r)
}
