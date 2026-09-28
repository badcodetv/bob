package main

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/badcodetv/bob/internal/mcp"
	"github.com/badcodetv/bob/internal/store"
)

// scheduleToolsApp wires a's MCP server with the worker and schedule tools and returns a chat
// token to call them with, for project "wolf" (with a pre-existing "researcher" worker, from
// newScheduleApp) and a second project "dev" to test cross-project isolation.
func scheduleToolsApp(t *testing.T) (a *app, token string, c *clock) {
	t.Helper()
	a, _, c = newScheduleApp(t)
	if err := a.store.ReconcileProjects(t.Context(), []string{"wolf", "dev"}); err != nil {
		t.Fatal(err)
	}
	a.mcp = mcp.New()
	a.mcp.Register(a.workerTools()...)
	a.mcp.Register(a.scheduleTools()...)
	sess, err := a.store.CreateSession(t.Context(), "wolf", nil, "", "claude", "", "")
	if err != nil {
		t.Fatal(err)
	}
	userMessage(t, a, sess, "kai@example.com")
	return a, mcpToken(a.auth.Secret, sess.ID), c
}

func TestScheduleCreateShowsNextAt(t *testing.T) {
	a, token, _ := scheduleToolsApp(t)
	status, isErr, text := callTool(t, a, token, "schedule_create",
		`{"name":"daily","worker":"researcher","cron":"0 9 * * *","message":"go"}`)
	if status != http.StatusOK || isErr {
		t.Fatalf("create = %d isError=%v %s", status, isErr, text)
	}
	var created scheduleView
	if err := json.Unmarshal([]byte(text), &created); err != nil {
		t.Fatal(err)
	}
	if created.Name != "daily" || created.Worker != "researcher" || created.Cron != "0 9 * * *" || !created.Enabled {
		t.Fatalf("created = %+v", created)
	}
	if created.NextAt == nil {
		t.Fatalf("created.NextAt = nil, want set")
	}
	if created.Timezone != "UTC" || created.KeepSessions != 30 {
		t.Fatalf("defaults: timezone=%q keep_sessions=%d", created.Timezone, created.KeepSessions)
	}

	// shows on the project's schedules
	list, err := a.store.Schedules(t.Context(), "wolf")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Name != "daily" {
		t.Fatalf("store schedules = %+v", list)
	}
}

func TestScheduleCreateDuplicateName(t *testing.T) {
	a, token, _ := scheduleToolsApp(t)
	callTool(t, a, token, "schedule_create", `{"name":"daily","worker":"researcher","cron":"0 9 * * *","message":"go"}`)
	status, isErr, text := callTool(t, a, token, "schedule_create",
		`{"name":"daily","worker":"researcher","cron":"0 10 * * *","message":"go again"}`)
	if status != http.StatusOK || !isErr {
		t.Fatalf("duplicate create = %d isError=%v %s", status, isErr, text)
	}
}

func TestScheduleCreateUnknownWorker(t *testing.T) {
	a, token, _ := scheduleToolsApp(t)
	status, isErr, text := callTool(t, a, token, "schedule_create",
		`{"name":"daily","worker":"nope","cron":"0 9 * * *","message":"go"}`)
	if status != http.StatusOK || !isErr {
		t.Fatalf("unknown worker create = %d isError=%v %s", status, isErr, text)
	}
}

func TestScheduleCreateBadCron(t *testing.T) {
	a, token, _ := scheduleToolsApp(t)
	status, isErr, text := callTool(t, a, token, "schedule_create",
		`{"name":"daily","worker":"researcher","cron":"nonsense","message":"go"}`)
	if status != http.StatusOK || !isErr {
		t.Fatalf("bad cron create = %d isError=%v %s", status, isErr, text)
	}
}

func TestScheduleUpdateKeepsFieldsLeftOut(t *testing.T) {
	a, token, _ := scheduleToolsApp(t)
	callTool(t, a, token, "schedule_create",
		`{"name":"daily","worker":"researcher","cron":"0 9 * * *","timezone":"America/New_York","message":"go","keep_sessions":5}`)

	status, isErr, text := callTool(t, a, token, "schedule_update", `{"name":"daily","message":"go now"}`)
	if status != http.StatusOK || isErr {
		t.Fatalf("update = %d isError=%v %s", status, isErr, text)
	}
	var updated scheduleView
	if err := json.Unmarshal([]byte(text), &updated); err != nil {
		t.Fatal(err)
	}
	if updated.Message != "go now" {
		t.Fatalf("message = %q, want %q", updated.Message, "go now")
	}
	if updated.Cron != "0 9 * * *" || updated.Timezone != "America/New_York" || updated.KeepSessions != 5 {
		t.Fatalf("unchanged fields not kept: %+v", updated)
	}
}

func TestScheduleUpdateRetimingSetsChangedAt(t *testing.T) {
	a, token, clk := scheduleToolsApp(t)
	_, _, text := callTool(t, a, token, "schedule_create",
		`{"name":"daily","worker":"researcher","cron":"0 9 * * *","message":"go"}`)
	var created scheduleView
	json.Unmarshal([]byte(text), &created)
	clk.set(created.ChangedAt.Add(time.Hour))

	status, isErr, text2 := callTool(t, a, token, "schedule_update", `{"name":"daily","cron":"0 10 * * *"}`)
	if status != http.StatusOK || isErr {
		t.Fatalf("update = %d isError=%v %s", status, isErr, text2)
	}
	var updated scheduleView
	json.Unmarshal([]byte(text2), &updated)
	if !updated.ChangedAt.After(created.ChangedAt) {
		t.Fatalf("changed_at not bumped: created=%v updated=%v", created.ChangedAt, updated.ChangedAt)
	}
}

func TestScheduleUpdateUnknownName(t *testing.T) {
	a, token, _ := scheduleToolsApp(t)
	status, isErr, text := callTool(t, a, token, "schedule_update", `{"name":"nope","message":"go"}`)
	if status != http.StatusOK || !isErr {
		t.Fatalf("update unknown = %d isError=%v %s", status, isErr, text)
	}
}

func TestScheduleListAndDelete(t *testing.T) {
	a, token, _ := scheduleToolsApp(t)
	callTool(t, a, token, "schedule_create", `{"name":"daily","worker":"researcher","cron":"0 9 * * *","message":"go"}`)

	status, isErr, text := callTool(t, a, token, "schedule_list", "")
	if status != http.StatusOK || isErr {
		t.Fatalf("list = %d isError=%v %s", status, isErr, text)
	}
	var list []scheduleView
	if err := json.Unmarshal([]byte(text), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Name != "daily" {
		t.Fatalf("list = %+v", list)
	}

	status, isErr, text = callTool(t, a, token, "schedule_delete", `{"name":"daily"}`)
	if status != http.StatusOK || isErr {
		t.Fatalf("delete = %d isError=%v %s", status, isErr, text)
	}
	if _, err := a.store.ScheduleByName(t.Context(), "wolf", "daily"); err != store.ErrNotFound {
		t.Fatalf("after delete: %v, want ErrNotFound", err)
	}
}

func TestScheduleDeleteUnknownName(t *testing.T) {
	a, token, _ := scheduleToolsApp(t)
	status, isErr, text := callTool(t, a, token, "schedule_delete", `{"name":"nope"}`)
	if status != http.StatusOK || !isErr {
		t.Fatalf("delete unknown = %d isError=%v %s", status, isErr, text)
	}
}

// cross-project isolation: a chat in dev cannot see, update or delete wolf's schedule.
func TestScheduleToolsAreProjectScoped(t *testing.T) {
	a, wolfToken, _ := scheduleToolsApp(t)
	callTool(t, a, wolfToken, "schedule_create", `{"name":"daily","worker":"researcher","cron":"0 9 * * *","message":"go"}`)

	devSess, err := a.store.CreateSession(t.Context(), "dev", nil, "", "claude", "", "")
	if err != nil {
		t.Fatal(err)
	}
	userMessage(t, a, devSess, "kai@example.com")
	devToken := mcpToken(a.auth.Secret, devSess.ID)

	status, isErr, text := callTool(t, a, devToken, "schedule_list", "")
	if status != http.StatusOK || isErr {
		t.Fatalf("dev list = %d isError=%v %s", status, isErr, text)
	}
	var list []scheduleView
	json.Unmarshal([]byte(text), &list)
	if len(list) != 0 {
		t.Fatalf("dev sees wolf's schedule: %+v", list)
	}

	status, isErr, text = callTool(t, a, devToken, "schedule_update", `{"name":"daily","message":"hijacked"}`)
	if status != http.StatusOK || !isErr {
		t.Fatalf("dev update wolf's schedule = %d isError=%v %s", status, isErr, text)
	}
	status, isErr, text = callTool(t, a, devToken, "schedule_delete", `{"name":"daily"}`)
	if status != http.StatusOK || !isErr {
		t.Fatalf("dev delete wolf's schedule = %d isError=%v %s", status, isErr, text)
	}

	sch, err := a.store.ScheduleByName(t.Context(), "wolf", "daily")
	if err != nil {
		t.Fatalf("wolf's schedule was touched: %v", err)
	}
	if sch.Message != "go" {
		t.Fatalf("wolf's schedule message = %q, want unchanged %q", sch.Message, "go")
	}
}
