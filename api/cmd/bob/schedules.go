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
	"github.com/badcodetv/bob/internal/runtime"
	"github.com/badcodetv/bob/internal/schedule"
	"github.com/badcodetv/bob/internal/store"
)

// scheduleLoop fires due schedules once a minute until ctx ends. Runs left "running" by a
// previous process are marked failed first, so they don't block their schedules forever.
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
// skipped instead. Nothing fires while schedules are paused.
func (a *app) tick(ctx context.Context) {
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
		if last.IsZero() {
			last = sch.CreatedAt
		}
		due, ok := spec.Due(last, now)
		if !ok {
			continue
		}
		if now.Sub(due) > schedule.CatchUp {
			detail := fmt.Sprintf("missed the firing at %s: Bob was not running within %v of it", due.UTC().Format(time.RFC3339), schedule.CatchUp)
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

// fire records a run and executes it in the background — or records it as skipped when the
// schedule's previous run is still going.
func (a *app) fire(ctx context.Context, sch store.Schedule, trigger string) (store.Run, error) {
	run, err := a.store.StartRun(ctx, sch.ID, trigger, a.now())
	if err != nil || run.Status == "skipped" {
		return run, err
	}
	a.scheduled.Add(1)
	go func() {
		defer a.scheduled.Done()
		a.execute(context.WithoutCancel(ctx), sch, run)
	}()
	return run, nil
}

// execute is one run: pull git, start a session on the worker with the schedule's message, wait
// for the turn, pull git again so anything the run pushed is visible, then prune old sessions.
func (a *app) execute(ctx context.Context, sch store.Schedule, run store.Run) {
	status, detail := "failed", ""
	defer func() {
		if err := a.store.FinishRun(ctx, run.ID, status, detail, a.now()); err != nil {
			log.Printf("schedule %s/%s run %d: %v", sch.Project, sch.Name, run.ID, err)
		}
	}()
	p, err := a.store.Project(ctx, sch.Project)
	if err != nil {
		detail = err.Error()
		return
	}
	base, err := a.runtime.Ensure(ctx, p)
	if err != nil {
		detail = err.Error()
		return
	}
	list, err := runtime.Workers(ctx, base, true)
	if err == nil && !list.Sync.OK {
		err = errors.New(list.Sync.Error)
	}
	if err != nil {
		detail = "git sync failed: " + err.Error()
		return
	}
	var worker *runtime.Worker
	for i := range list.Workers {
		if list.Workers[i].Name == sch.Worker {
			worker = &list.Workers[i]
		}
	}
	if worker == nil {
		detail = fmt.Sprintf("worker %q not found in git", sch.Worker)
		if list.Error != "" {
			detail += " (" + list.Error + ")"
		}
		return
	}
	sess, err := a.store.CreateSession(ctx, sch.Project, worker.Name, worker.Engine, "", "")
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

	if after, err := runtime.Workers(ctx, base, true); err != nil || !after.Sync.OK {
		if err == nil {
			err = errors.New(after.Sync.Error)
		}
		detail = strings.TrimPrefix(detail+"; git sync after the run failed: "+err.Error(), "; ")
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

// scheduleBody is a schedule as the API accepts it; absent fields keep their value on PATCH.
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
	set(&x.Worker, b.Worker)
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
	case x.Worker == "":
		return errors.New("worker is required")
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
	x := store.Schedule{Project: r.PathValue("project"), Timezone: "UTC", Enabled: true, KeepSessions: 30}
	body.apply(&x)
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
	body.apply(&x)
	if err := validSchedule(x); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	x, err = a.store.UpdateSchedule(r.Context(), x)
	reply(w, x, err)
}

func (a *app) deleteSchedule(w http.ResponseWriter, r *http.Request) {
	reply(w, map[string]bool{"ok": true}, a.store.DeleteSchedule(r.Context(), r.PathValue("schedule")))
}

// runScheduleNow runs a schedule once, now, whether or not it is enabled or schedules are
// paused: a person asked for it. It is skipped if the previous run is still going.
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
