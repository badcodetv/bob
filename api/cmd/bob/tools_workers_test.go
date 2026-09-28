package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/badcodetv/bob/internal/mcp"
	"github.com/badcodetv/bob/internal/store"
)

// callTool calls a worker tool over POST /mcp with the given token and JSON arguments (raw JSON,
// e.g. `{"name":"poet"}`; "" for no arguments), and returns the HTTP status, whether the tool
// result was flagged isError, and its text content.
func callTool(t *testing.T, a *app, token, name, argsJSON string) (status int, isError bool, text string) {
	t.Helper()
	params := `{"name":"` + name + `"`
	if argsJSON != "" {
		params += `,"arguments":` + argsJSON
	}
	params += `}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":`+params+`}`))
	req.Host = "api:8070"
	req.Header.Set("Authorization", "Bearer "+token)
	res := httptest.NewRecorder()
	a.routes().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		return res.Code, false, res.Body.String()
	}
	var r struct {
		Result struct {
			IsError bool
			Content []struct{ Text string }
		}
	}
	if err := json.Unmarshal(res.Body.Bytes(), &r); err != nil {
		t.Fatalf("decoding response: %v: %s", err, res.Body)
	}
	if len(r.Result.Content) != 1 {
		t.Fatalf("content = %+v", r.Result.Content)
	}
	return res.Code, r.Result.IsError, r.Result.Content[0].Text
}

// workerToolsApp wires a's MCP server with the worker tools and returns a chat token to call
// them with, for a project named "wolf" (with a pre-existing "researcher" worker, from
// newScheduleApp) and a second project "dev" to test cross-project isolation.
func workerToolsApp(t *testing.T) (a *app, token string) {
	t.Helper()
	a, _, _ = newScheduleApp(t)
	if err := a.store.ReconcileProjects(t.Context(), []string{"wolf", "dev"}); err != nil {
		t.Fatal(err)
	}
	a.mcp = mcp.New()
	a.mcp.Register(a.workerTools()...)
	sess, err := a.store.CreateSession(t.Context(), "wolf", nil, "", "claude", "", "")
	if err != nil {
		t.Fatal(err)
	}
	userMessage(t, a, sess, "kai@example.com")
	return a, mcpToken(a.auth.Secret, sess.ID)
}

func TestWorkerCreateRecordsAVersion(t *testing.T) {
	a, token := workerToolsApp(t)
	status, isErr, text := callTool(t, a, token, "worker_create",
		`{"name":"poet","engine":"claude","prompt":"write haiku","why":"new worker"}`)
	if status != http.StatusOK || isErr {
		t.Fatalf("create = %d isError=%v %s", status, isErr, text)
	}
	var created store.Worker
	if err := json.Unmarshal([]byte(text), &created); err != nil || created.Name != "poet" || created.Prompt != "write haiku" {
		t.Fatalf("created = %+v (%v)", created, err)
	}

	versions, err := a.store.WorkerVersions(t.Context(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 1 || versions[0].Action != "create" || versions[0].ChangedBy != "kai@example.com" || versions[0].Why != "new worker" {
		t.Fatalf("versions = %+v", versions)
	}
}

func TestWorkerCreateDuplicateIsAToolError(t *testing.T) {
	a, token := workerToolsApp(t)
	status, isErr, _ := callTool(t, a, token, "worker_create",
		`{"name":"researcher","engine":"claude","prompt":"research","why":"new"}`)
	if status != http.StatusOK || !isErr {
		t.Fatalf("duplicate create = %d isError=%v", status, isErr)
	}
}

func TestWorkerCreateInvalidEngineOrEffort(t *testing.T) {
	a, token := workerToolsApp(t)
	status, isErr, _ := callTool(t, a, token, "worker_create",
		`{"name":"bad","engine":"gpt","prompt":"x","why":"new"}`)
	if status != http.StatusOK || !isErr {
		t.Fatalf("bad engine = %d isError=%v", status, isErr)
	}
	status, isErr, _ = callTool(t, a, token, "worker_create",
		`{"name":"bad","engine":"claude","effort":"nope","prompt":"x","why":"new"}`)
	if status != http.StatusOK || !isErr {
		t.Fatalf("bad effort = %d isError=%v", status, isErr)
	}
}

func TestWorkerCreateMissingWhyIsAToolError(t *testing.T) {
	a, token := workerToolsApp(t)
	status, isErr, _ := callTool(t, a, token, "worker_create",
		`{"name":"poet","engine":"claude","prompt":"write haiku"}`)
	if status != http.StatusOK || !isErr {
		t.Fatalf("missing why = %d isError=%v", status, isErr)
	}
}

func TestWorkerUpdate(t *testing.T) {
	a, token := workerToolsApp(t)
	status, isErr, text := callTool(t, a, token, "worker_update",
		`{"name":"researcher","engine":"claude","model":"opus","effort":"high","prompt":"research harder","why":"tune it"}`)
	if status != http.StatusOK || isErr {
		t.Fatalf("update = %d isError=%v %s", status, isErr, text)
	}
	var updated store.Worker
	if err := json.Unmarshal([]byte(text), &updated); err != nil || updated.Prompt != "research harder" || updated.Effort != "high" {
		t.Fatalf("updated = %+v (%v)", updated, err)
	}
	versions, err := a.store.WorkerVersions(t.Context(), updated.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 1 || versions[0].Action != "update" || versions[0].Why != "tune it" {
		t.Fatalf("versions = %+v", versions)
	}
}

func TestWorkerList(t *testing.T) {
	a, token := workerToolsApp(t)
	if _, isErr, _ := callTool(t, a, token, "worker_create",
		`{"name":"poet","engine":"claude","prompt":"write haiku","why":"new"}`); isErr {
		t.Fatal("setup create failed")
	}

	status, isErr, text := callTool(t, a, token, "worker_list", "")
	if status != http.StatusOK || isErr {
		t.Fatalf("list = %d isError=%v %s", status, isErr, text)
	}
	if strings.Contains(text, "write haiku") {
		t.Errorf("list without name should not include prompts: %s", text)
	}
	if !strings.Contains(text, `"name":"poet"`) || !strings.Contains(text, `"name":"researcher"`) {
		t.Errorf("list = %s", text)
	}

	status, isErr, text = callTool(t, a, token, "worker_list", `{"name":"poet"}`)
	if status != http.StatusOK || isErr {
		t.Fatalf("list by name = %d isError=%v %s", status, isErr, text)
	}
	if !strings.Contains(text, `"prompt":"write haiku"`) {
		t.Errorf("list by name should include the prompt: %s", text)
	}
}

func TestWorkerCreateWithLabelsAndListByLabelSelector(t *testing.T) {
	a, token := workerToolsApp(t)
	if _, isErr, text := callTool(t, a, token, "worker_create",
		`{"name":"scout","engine":"claude","prompt":"scout","labels":{"kind":"hypothesis"},"why":"new"}`); isErr {
		t.Fatalf("setup create failed: %s", text)
	}
	if _, isErr, text := callTool(t, a, token, "worker_create",
		`{"name":"scraper","engine":"claude","prompt":"scrape","labels":{"kind":"scraper"},"why":"new"}`); isErr {
		t.Fatalf("setup create failed: %s", text)
	}

	status, isErr, text := callTool(t, a, token, "worker_list", `{"label_selector":"kind=hypothesis"}`)
	if status != http.StatusOK || isErr {
		t.Fatalf("list by selector = %d isError=%v %s", status, isErr, text)
	}
	if !strings.Contains(text, `"name":"scout"`) {
		t.Errorf("list by selector missing scout: %s", text)
	}
	if strings.Contains(text, `"name":"scraper"`) || strings.Contains(text, `"name":"researcher"`) {
		t.Errorf("list by selector should only return matching workers: %s", text)
	}
}

func TestWorkerListInvalidSelectorIsAToolError(t *testing.T) {
	a, token := workerToolsApp(t)
	status, isErr, text := callTool(t, a, token, "worker_list", `{"label_selector":"kind in (a"}`)
	if status != http.StatusOK || !isErr {
		t.Fatalf("invalid selector = %d isError=%v %s", status, isErr, text)
	}
	if text == "" {
		t.Error("expected a readable error message")
	}
}

func TestWorkerUpdateLabels(t *testing.T) {
	a, token := workerToolsApp(t)
	status, isErr, text := callTool(t, a, token, "worker_update",
		`{"name":"researcher","engine":"claude","prompt":"research","labels":{"kind":"hypothesis"},"why":"add labels"}`)
	if status != http.StatusOK || isErr {
		t.Fatalf("update = %d isError=%v %s", status, isErr, text)
	}
	var updated store.Worker
	if err := json.Unmarshal([]byte(text), &updated); err != nil || updated.Labels["kind"] != "hypothesis" {
		t.Fatalf("updated = %+v (%v)", updated, err)
	}
}

func TestWorkerCreateInvalidLabelIsAToolError(t *testing.T) {
	a, token := workerToolsApp(t)
	status, isErr, text := callTool(t, a, token, "worker_create",
		`{"name":"bad","engine":"claude","prompt":"x","labels":{"bob.reserved":"v"},"why":"new"}`)
	if status != http.StatusOK || !isErr {
		t.Fatalf("reserved label = %d isError=%v %s", status, isErr, text)
	}
}

func TestWorkerDelete(t *testing.T) {
	a, token := workerToolsApp(t)
	if _, isErr, _ := callTool(t, a, token, "worker_create",
		`{"name":"poet","engine":"claude","prompt":"write haiku","why":"new"}`); isErr {
		t.Fatal("setup create failed")
	}

	status, isErr, _ := callTool(t, a, token, "worker_delete", `{"name":"poet"}`)
	if status != http.StatusOK || !isErr {
		t.Fatalf("delete without why = %d isError=%v, want a tool error", status, isErr)
	}

	status, isErr, text := callTool(t, a, token, "worker_delete", `{"name":"poet","why":"no longer needed"}`)
	if status != http.StatusOK || isErr {
		t.Fatalf("delete = %d isError=%v %s", status, isErr, text)
	}
	if _, err := a.store.Worker(t.Context(), "wolf", "poet"); err != store.ErrNotFound {
		t.Errorf("worker still exists: %v", err)
	}
}

func TestWorkerToolsAreScopedToTheCallersProject(t *testing.T) {
	a, token := workerToolsApp(t)
	if _, err := a.store.CreateWorker(t.Context(), store.Worker{Project: "dev", Name: "other", Engine: "claude", Prompt: "x"}, "kai@example.com", "seed"); err != nil {
		t.Fatal(err)
	}

	status, isErr, text := callTool(t, a, token, "worker_list", `{"name":"other"}`)
	if status != http.StatusOK || !isErr {
		t.Fatalf("another project's worker by name = %d isError=%v %s", status, isErr, text)
	}

	status, isErr, text = callTool(t, a, token, "worker_list", "")
	if status != http.StatusOK || isErr || strings.Contains(text, `"other"`) {
		t.Fatalf("another project's worker showed up in the list: %d isError=%v %s", status, isErr, text)
	}

	status, isErr, _ = callTool(t, a, token, "worker_delete", `{"name":"other","why":"nope"}`)
	if status != http.StatusOK || !isErr {
		t.Fatalf("delete another project's worker = %d isError=%v, want a tool error", status, isErr)
	}
	if _, err := a.store.Worker(t.Context(), "dev", "other"); err != nil {
		t.Errorf("worker was deleted despite being in another project: %v", err)
	}
}
