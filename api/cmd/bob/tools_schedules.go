package main

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/badcodetv/bob/internal/mcp"
	"github.com/badcodetv/bob/internal/schedule"
	"github.com/badcodetv/bob/internal/store"
)

// scheduleTools returns schedule_create, schedule_update, schedule_list and schedule_delete: an
// agent managing its own project's schedules, addressed by name (not the store's uuid). They
// share validSchedule and scheduleBody.apply (schedules.go, T3) with the HTTP handlers; only the
// argument shape, the by-name lookup and the tool-error wrapping differ. Not admin-gated
// (Decision 13): any chat in a project can create and change its own schedules.
func (a *app) scheduleTools() []mcp.Tool {
	return []mcp.Tool{
		{
			Name: "schedule_create",
			Description: "Creates a schedule in this project: a cron-timed job that starts a new session " +
				"on a worker with a message. " + scheduleNote,
			InputSchema: scheduleCreateSchema,
			Call: func(ctx context.Context, c mcp.Caller, args json.RawMessage) (any, error) {
				in, err := decodeScheduleArgs(args)
				if err != nil {
					return nil, err
				}
				if in.Worker == nil || *in.Worker == "" {
					return nil, errors.New("worker is required")
				}
				id, err := a.findWorkerIDForTool(ctx, c.Project, *in.Worker)
				if err != nil {
					return nil, err
				}
				x := store.Schedule{Project: c.Project, Timezone: "UTC", Enabled: true, KeepSessions: 30}
				in.body().apply(&x)
				x.WorkerID = id
				if err := validSchedule(x); err != nil {
					return nil, err
				}
				created, err := a.store.CreateSchedule(ctx, x)
				if errors.Is(err, store.ErrConflict) {
					return nil, errors.New("a schedule named " + in.Name + " already exists in this project")
				}
				if err != nil {
					return nil, err
				}
				return a.scheduleToolView(ctx, created)
			},
		},
		{
			Name: "schedule_update",
			Description: "Changes an existing schedule. Fields left out keep their current value " +
				"(unlike worker_update). " + scheduleNote,
			InputSchema: scheduleUpdateSchema,
			Call: func(ctx context.Context, c mcp.Caller, args json.RawMessage) (any, error) {
				in, err := decodeScheduleArgs(args)
				if err != nil {
					return nil, err
				}
				x, err := a.store.ScheduleByName(ctx, c.Project, in.Name)
				if errors.Is(err, store.ErrNotFound) {
					return nil, errors.New("no schedule named " + in.Name + " in this project")
				}
				if err != nil {
					return nil, err
				}
				before := x
				in.body().apply(&x)
				if in.Worker != nil {
					id, err := a.findWorkerIDForTool(ctx, c.Project, *in.Worker)
					if err != nil {
						return nil, err
					}
					x.WorkerID = id
				}
				if err := validSchedule(x); err != nil {
					return nil, err
				}
				if (x.Enabled && !before.Enabled) || x.Cron != before.Cron || x.Timezone != before.Timezone {
					x.ChangedAt = a.now()
				}
				updated, err := a.store.UpdateSchedule(ctx, x)
				if err != nil {
					return nil, err
				}
				return a.scheduleToolView(ctx, updated)
			},
		},
		{
			Name:        "schedule_list",
			Description: "Lists this project's schedules, with when each next fires and how its last run went.",
			Call: func(ctx context.Context, c mcp.Caller, args json.RawMessage) (any, error) {
				list, err := a.store.Schedules(ctx, c.Project)
				if err != nil {
					return nil, err
				}
				out := make([]scheduleView, len(list))
				for i, sch := range list {
					v, err := a.scheduleToolView(ctx, sch)
					if err != nil {
						return nil, err
					}
					out[i] = v
				}
				return out, nil
			},
		},
		{
			Name:        "schedule_delete",
			Description: "Deletes a schedule from this project.",
			InputSchema: scheduleDeleteSchema,
			Call: func(ctx context.Context, c mcp.Caller, args json.RawMessage) (any, error) {
				var in struct {
					Name string `json:"name"`
				}
				if err := json.Unmarshal(args, &in); err != nil {
					return nil, err
				}
				x, err := a.store.ScheduleByName(ctx, c.Project, in.Name)
				if errors.Is(err, store.ErrNotFound) {
					return nil, errors.New("no schedule named " + in.Name + " in this project")
				}
				if err != nil {
					return nil, err
				}
				err = a.store.DeleteSchedule(ctx, x.ID)
				return map[string]bool{"ok": err == nil}, err
			},
		},
	}
}

// scheduleNote is appended to schedule_create's and schedule_update's descriptions.
const scheduleNote = "cron is a standard 5-field cron expression; timezone is an IANA name " +
	"(default UTC). message is the first message sent to the new session each time it fires. " +
	"Turning a schedule back on, or changing its cron or timezone, never runs what it missed " +
	"while off."

var scheduleCreateSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"name": {"type": "string", "description": "lower-case letters, digits and -, starting with a letter or digit; permanent"},
		"worker": {"type": "string", "description": "the worker to run"},
		"cron": {"type": "string", "description": "5-field cron expression"},
		"timezone": {"type": "string", "description": "IANA timezone name; default UTC"},
		"message": {"type": "string", "description": "sent to the worker as the new session's first message"},
		"enabled": {"type": "boolean", "description": "default true"},
		"keep_sessions": {"type": "integer", "description": "how many of this schedule's sessions to keep; older ones are deleted. default 30"}
	},
	"required": ["name", "worker", "cron", "message"]
}`)

var scheduleUpdateSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"name": {"type": "string", "description": "the schedule to update; cannot be changed"},
		"worker": {"type": "string"},
		"cron": {"type": "string", "description": "5-field cron expression"},
		"timezone": {"type": "string", "description": "IANA timezone name"},
		"message": {"type": "string"},
		"enabled": {"type": "boolean"},
		"keep_sessions": {"type": "integer"}
	},
	"required": ["name"]
}`)

var scheduleDeleteSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"name": {"type": "string"}
	},
	"required": ["name"]
}`)

// scheduleToolArgs is schedule_create's and schedule_update's arguments: pointers so
// schedule_update can tell "left out" from a zero value, as scheduleBody does for HTTP.
type scheduleToolArgs struct {
	Name         string  `json:"name"`
	Worker       *string `json:"worker"`
	Cron         *string `json:"cron"`
	Timezone     *string `json:"timezone"`
	Message      *string `json:"message"`
	Enabled      *bool   `json:"enabled"`
	KeepSessions *int    `json:"keep_sessions"`
}

func decodeScheduleArgs(args json.RawMessage) (scheduleToolArgs, error) {
	var in scheduleToolArgs
	err := json.Unmarshal(args, &in)
	return in, err
}

func (in scheduleToolArgs) body() scheduleBody {
	return scheduleBody{Name: &in.Name, Cron: in.Cron, Timezone: in.Timezone, Message: in.Message,
		Enabled: in.Enabled, KeepSessions: in.KeepSessions}
}

// findWorkerIDForTool resolves a worker name to its id within project, as a tool error (not an
// HTTP one) when it does not exist.
func (a *app) findWorkerIDForTool(ctx context.Context, project, name string) (string, error) {
	w, err := a.store.Worker(ctx, project, name)
	if errors.Is(err, store.ErrNotFound) {
		return "", errors.New("no worker named " + name + " in this project")
	}
	return w.ID, err
}

// scheduleToolView is a schedule tool result: the schedule plus next_at, as the Schedules page shows it.
func (a *app) scheduleToolView(ctx context.Context, sch store.Schedule) (scheduleView, error) {
	v := scheduleView{Schedule: sch}
	if sch.Enabled {
		spec, err := schedule.Parse(sch.Cron, sch.Timezone)
		if err != nil {
			return v, err
		}
		next := spec.Next(a.now())
		v.NextAt = &next
	}
	runs, err := a.store.Runs(ctx, sch.ID, 1)
	if err != nil {
		return v, err
	}
	if len(runs) == 1 {
		v.LastRun = &runs[0]
	}
	return v, nil
}
