package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/badcodetv/bob/internal/access"
	"github.com/badcodetv/bob/internal/auth"
	"github.com/badcodetv/bob/internal/embed"
	"github.com/badcodetv/bob/internal/mcp"
	"github.com/badcodetv/bob/internal/store"
)

// memoryToolsApp wires a's MCP server with the memory tools (and workers, for a worker-attached
// chat) and returns two chat tokens for project "wolf": a plain human chat and a worker chat
// ("researcher"), plus a chat token for a second project "dev" to test cross-project isolation.
func memoryToolsApp(t *testing.T) (a *app, humanToken, workerToken, devToken string, clk *clock) {
	t.Helper()
	a, _, clk = newScheduleApp(t)
	if err := a.store.ReconcileProjects(t.Context(), []string{"wolf", "dev"}); err != nil {
		t.Fatal(err)
	}
	a.embed = embed.Fake{}
	a.publicURL = "https://bob.example"
	a.mcp = mcp.New()
	a.mcp.Register(a.workerTools()...)
	a.mcp.Register(a.memoryTools()...)

	human, err := a.store.CreateSession(t.Context(), "wolf", nil, "", "claude", "", "")
	if err != nil {
		t.Fatal(err)
	}
	userMessage(t, a, human, "kai@example.com")

	rid := researcherID[a]
	worker, err := a.store.CreateSession(t.Context(), "wolf", &rid, "researcher", "claude", "", "")
	if err != nil {
		t.Fatal(err)
	}
	userMessage(t, a, worker, "kai@example.com")

	dev, err := a.store.CreateSession(t.Context(), "dev", nil, "", "claude", "", "")
	if err != nil {
		t.Fatal(err)
	}
	userMessage(t, a, dev, "kai@example.com")

	return a, mcpToken(a.auth.Secret, human.ID), mcpToken(a.auth.Secret, worker.ID), mcpToken(a.auth.Secret, dev.ID), clk
}

func TestMemoryCreateAndGet(t *testing.T) {
	a, human, _, _, _ := memoryToolsApp(t)
	status, isErr, text := callTool(t, a, human, "memory_create",
		`{"content":"Ferries run hourly from the north dock.","labels":{"kind":"lesson"}}`)
	if status != http.StatusOK || isErr {
		t.Fatalf("create = %d isError=%v %s", status, isErr, text)
	}
	var created memoryRecord
	if err := json.Unmarshal([]byte(text), &created); err != nil {
		t.Fatal(err)
	}
	if created.Content != "Ferries run hourly from the north dock." || created.Labels["kind"] != "lesson" {
		t.Fatalf("created = %+v", created)
	}
	if created.ChatURL == "" || !strings.HasSuffix(created.ChatURL, "/#/p/wolf/s/"+created.CreatedBySession) {
		t.Fatalf("chat_url = %q", created.ChatURL)
	}
	if created.CreatedByWorker != "" {
		t.Fatalf("created_by_worker = %q, want \"\" for a plain chat", created.CreatedByWorker)
	}

	status, isErr, text = callTool(t, a, human, "memory_get", `{"id":"`+created.ID+`"}`)
	if status != http.StatusOK || isErr {
		t.Fatalf("get = %d isError=%v %s", status, isErr, text)
	}
	var got memoryRecord
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != created.ID || got.Content != created.Content {
		t.Fatalf("got = %+v", got)
	}
}

func TestMemoryCreatedByWorkerFromSession(t *testing.T) {
	a, _, workerTok, _, _ := memoryToolsApp(t)
	_, isErr, text := callTool(t, a, workerTok, "memory_create", `{"content":"researcher wrote this"}`)
	if isErr {
		t.Fatalf("create errored: %s", text)
	}
	var created memoryRecord
	if err := json.Unmarshal([]byte(text), &created); err != nil {
		t.Fatal(err)
	}
	if created.CreatedByWorker != "researcher" {
		t.Fatalf("created_by_worker = %q, want researcher", created.CreatedByWorker)
	}
}

func TestMemoryCreateRequiresContent(t *testing.T) {
	a, human, _, _, _ := memoryToolsApp(t)
	_, isErr, text := callTool(t, a, human, "memory_create", `{"content":"   "}`)
	if !isErr {
		t.Fatalf("blank content should error, got %s", text)
	}
}

func TestMemoryCreateBadLabel(t *testing.T) {
	a, human, _, _, _ := memoryToolsApp(t)
	_, isErr, text := callTool(t, a, human, "memory_create", `{"content":"x","labels":{"bad key":"x"}}`)
	if !isErr {
		t.Fatalf("bad label should error, got %s", text)
	}
}

// TestMemorySearchFindsKeywordAndVectorMatches is T21's acceptance criterion, exercised through
// the MCP tool: a memory matching only by keyword and one matching only by vector both appear.
func TestMemorySearchFindsKeywordAndVectorMatches(t *testing.T) {
	a, human, _, _, _ := memoryToolsApp(t)
	callTool(t, a, human, "memory_create", `{"content":"Ferries run hourly."}`)
	callTool(t, a, human, "memory_create", `{"content":"the the the zebra"}`)

	_, isErr, text := callTool(t, a, human, "memory_search", `{"query":"the ferry"}`)
	if isErr {
		t.Fatalf("search errored: %s", text)
	}
	var res struct {
		Results []memoryHit `json:"results"`
		Count   int         `json:"count"`
	}
	if err := json.Unmarshal([]byte(text), &res); err != nil {
		t.Fatal(err)
	}
	if res.Count != 2 {
		t.Fatalf("count = %d, want 2: %+v", res.Count, res.Results)
	}
	if res.Results[0].Snippet != "Ferries run hourly." {
		t.Fatalf("top hit = %+v, want the ferries memory first", res.Results[0])
	}
}

func TestMemorySearchRetractsHidesTarget(t *testing.T) {
	a, human, _, _, _ := memoryToolsApp(t)
	_, _, text := callTool(t, a, human, "memory_create", `{"content":"wrong fact","labels":{"name":"fact"}}`)
	var created memoryRecord
	json.Unmarshal([]byte(text), &created)

	_, isErr, text := callTool(t, a, human, "memory_search", `{"label_selector":"name=fact"}`)
	if isErr {
		t.Fatal(text)
	}
	var before struct {
		Results []memoryHit `json:"results"`
	}
	json.Unmarshal([]byte(text), &before)
	if len(before.Results) != 1 {
		t.Fatalf("before retraction = %+v", before.Results)
	}

	_, isErr, text = callTool(t, a, human, "memory_create", `{"content":"it was wrong","labels":{"retracts":"`+created.ID+`"}}`)
	if isErr {
		t.Fatal(text)
	}

	_, isErr, text = callTool(t, a, human, "memory_search", `{"label_selector":"name=fact"}`)
	if isErr {
		t.Fatal(text)
	}
	var after struct {
		Results []memoryHit `json:"results"`
	}
	json.Unmarshal([]byte(text), &after)
	if len(after.Results) != 0 {
		t.Fatalf("after retraction = %+v, want none (the retracted memory itself has no name label)", after.Results)
	}
}

func TestMemorySearchLatestPer(t *testing.T) {
	a, human, _, _, clk := memoryToolsApp(t)
	callTool(t, a, human, "memory_create", `{"content":"v1","labels":{"name":"x","kind":"status"}}`)
	clk.set(clk.now().Add(time.Millisecond))
	callTool(t, a, human, "memory_create", `{"content":"v2","labels":{"name":"x","kind":"status"}}`)
	clk.set(clk.now().Add(time.Millisecond))
	callTool(t, a, human, "memory_create", `{"content":"no name","labels":{"kind":"status"}}`)

	_, isErr, text := callTool(t, a, human, "memory_search", `{"label_selector":"kind=status","latest_per":"name"}`)
	if isErr {
		t.Fatal(text)
	}
	var res struct {
		Results []memoryHit `json:"results"`
	}
	json.Unmarshal([]byte(text), &res)
	if len(res.Results) != 1 || res.Results[0].Snippet != "v2" {
		t.Fatalf("latest_per = %+v, want just v2", res.Results)
	}
}

func TestMemorySearchCreatedByWorkerSelf(t *testing.T) {
	a, human, workerTok, _, _ := memoryToolsApp(t)
	callTool(t, a, workerTok, "memory_create", `{"content":"mine"}`)
	callTool(t, a, human, "memory_create", `{"content":"not mine"}`)

	_, isErr, text := callTool(t, a, workerTok, "memory_search", `{"created_by_worker":"self"}`)
	if isErr {
		t.Fatal(text)
	}
	var res struct {
		Results []memoryHit `json:"results"`
	}
	json.Unmarshal([]byte(text), &res)
	if len(res.Results) != 1 || res.Results[0].Snippet != "mine" {
		t.Fatalf("self search = %+v", res.Results)
	}

	// a plain human chat has no worker identity: "self" is refused, not empty
	_, isErr, text = callTool(t, a, human, "memory_search", `{"created_by_worker":"self"}`)
	if !isErr {
		t.Fatalf("human self search should error, got %s", text)
	}
}

func TestMemoryCurrentFoundFalse(t *testing.T) {
	a, human, _, _, _ := memoryToolsApp(t)
	_, isErr, text := callTool(t, a, human, "memory_current", `{"name":"nope"}`)
	if isErr {
		t.Fatal(text)
	}
	var out struct {
		Found bool `json:"found"`
	}
	json.Unmarshal([]byte(text), &out)
	if out.Found {
		t.Fatalf("found = true, want false")
	}
}

func TestMemoryCurrentReturnsNewest(t *testing.T) {
	a, human, _, _, clk := memoryToolsApp(t)
	callTool(t, a, human, "memory_create", `{"content":"v1","labels":{"name":"x"}}`)
	clk.set(clk.now().Add(time.Millisecond))
	_, isErr, text := callTool(t, a, human, "memory_create", `{"content":"v2","labels":{"name":"x"}}`)
	if isErr {
		t.Fatal(text)
	}

	_, isErr, text = callTool(t, a, human, "memory_current", `{"name":"x"}`)
	if isErr {
		t.Fatal(text)
	}
	var cur memoryRecord
	json.Unmarshal([]byte(text), &cur)
	if cur.Content != "v2" {
		t.Fatalf("current = %+v, want v2", cur)
	}
}

func TestMemoryIfCurrentConflict(t *testing.T) {
	a, human, _, _, clk := memoryToolsApp(t)
	_, _, text := callTool(t, a, human, "memory_create", `{"content":"v1","labels":{"name":"x"}}`)
	var v1 memoryRecord
	json.Unmarshal([]byte(text), &v1)
	// someone else writes first
	clk.set(clk.now().Add(time.Millisecond))
	callTool(t, a, human, "memory_create", `{"content":"v2","labels":{"name":"x"}}`)

	// the stale writer's compare-and-swap against v1 is refused
	_, isErr, text := callTool(t, a, human, "memory_create", `{"content":"v1-edited","labels":{"name":"x"},"if_current":"`+v1.ID+`"}`)
	if !isErr {
		t.Fatalf("stale if_current should error, got %s", text)
	}
}

// TestMemorySharedWithinProjectIsolatedAcrossProjects is T22's acceptance criterion: two chats of
// one project share memories; another project sees none.
func TestMemorySharedWithinProjectIsolatedAcrossProjects(t *testing.T) {
	a, human, workerTok, devTok, _ := memoryToolsApp(t)
	_, isErr, text := callTool(t, a, human, "memory_create", `{"content":"shared across wolf's chats"}`)
	if isErr {
		t.Fatal(text)
	}
	var created memoryRecord
	json.Unmarshal([]byte(text), &created)

	// the worker chat, same project, can read it back
	_, isErr, text = callTool(t, a, workerTok, "memory_get", `{"id":"`+created.ID+`"}`)
	if isErr {
		t.Fatalf("worker chat should see the memory: %s", text)
	}

	// dev, a different project, cannot
	_, isErr, text = callTool(t, a, devTok, "memory_get", `{"id":"`+created.ID+`"}`)
	if !isErr {
		t.Fatalf("dev project should not see wolf's memory, got %s", text)
	}
	_, isErr, text = callTool(t, a, devTok, "memory_search", `{}`)
	if isErr {
		t.Fatal(text)
	}
	var res struct {
		Results []memoryHit `json:"results"`
	}
	json.Unmarshal([]byte(text), &res)
	if len(res.Results) != 0 {
		t.Fatalf("dev search = %+v, want none", res.Results)
	}
}

func TestListMemoriesHTTP(t *testing.T) {
	a, human, _, _, clk := memoryToolsApp(t)
	callTool(t, a, human, "memory_create", `{"content":"one"}`)
	clk.set(clk.now().Add(time.Millisecond))
	callTool(t, a, human, "memory_create", `{"content":"two"}`)

	people := access.Map{"tester@example.com": {"wolf"}}
	a.auth, a.access = &auth.Auth{Secret: a.auth.Secret, Allowed: people.Allowed}, people
	a.projectOf = a.storeProjectOf
	rec := httptest.NewRecorder()
	a.auth.SetSession(rec, auth.User{Email: "tester@example.com"})
	req := httptest.NewRequest("GET", "/api/projects/wolf/memories?limit=20", nil)
	req.AddCookie(rec.Result().Cookies()[0])
	res := httptest.NewRecorder()
	a.routes().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("list memories = %d %s", res.Code, res.Body)
	}
	var out struct {
		Memories []store.MemoryHit `json:"memories"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Memories) != 2 {
		t.Fatalf("memories = %+v", out.Memories)
	}
	if out.Memories[0].Snippet != "two" {
		t.Fatalf("newest first: %+v", out.Memories)
	}
}

// Every memory tool's input schema must be valid JSON: an invalid one made tools/list answer with
// an empty body, which took down every tool for the chat, not just this one.
func TestMemoryToolSchemasAreValidJSON(t *testing.T) {
	a := &app{}
	for _, tool := range a.memoryTools() {
		if !json.Valid(tool.InputSchema) {
			t.Errorf("%s: input schema is not valid JSON: %s", tool.Name, tool.InputSchema)
		}
	}
}

func TestParseMemTime(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	for in, want := range map[string]time.Time{
		"":                     {},
		"7d":                   now.Add(-7 * 24 * time.Hour),
		"12h":                  now.Add(-12 * time.Hour),
		"2026-09-01T00:00:00Z": time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		"1790000000000":        time.UnixMilli(1790000000000),
	} {
		got, err := parseMemTime(in, now)
		if err != nil || !got.Equal(want) {
			t.Errorf("parseMemTime(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	if _, err := parseMemTime("yesterday", now); err == nil {
		t.Error("parseMemTime(yesterday) should fail")
	}
}
