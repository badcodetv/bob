package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/badcodetv/bob/internal/embed"
	"github.com/badcodetv/bob/internal/mcp"
	"github.com/badcodetv/bob/internal/store"
)

const wolfKey = "wolf-key-0123456789abcdef0123456789abcdef"

// chatRuntime is each chat tools app's fake project runtime.
var chatRuntime = map[*app]*fakeRuntime{}

// chatToolsApp serves every tool main.go registers, with an MCP key for project wolf, and a wolf
// chat holding a short Claude conversation and a Codex one, plus a dev chat (another project).
func chatToolsApp(t *testing.T) (a *app, claudeChat, codexChat, devChat store.Session) {
	t.Helper()
	var rt *fakeRuntime
	a, rt, _ = newScheduleApp(t)
	chatRuntime[a] = rt
	if err := a.store.ReconcileProjects(t.Context(), []string{"wolf", "dev"}); err != nil {
		t.Fatal(err)
	}
	a.embed = embed.Fake{}
	a.publicURL = "https://bob.example"
	a.mcpKeys = map[string]string{"wolf": wolfKey}
	a.mcp = mcp.New()
	a.mcp.Register(bobWhoami)
	a.mcp.Register(a.workerTools()...)
	a.mcp.Register(a.projectPromptTools()...)
	a.mcp.Register(a.scheduleTools()...)
	a.mcp.Register(a.driveTools()...)
	a.mcp.Register(a.memoryTools()...)
	a.mcp.Register(a.chatTools()...)
	a.mcp.Register(a.attentionTools()...)

	add := func(s store.Session, kind, payload string) {
		t.Helper()
		if _, err := a.store.AppendEvent(t.Context(), store.Event{SessionID: s.ID, Engine: s.Engine, Kind: kind, Payload: json.RawMessage(payload)}); err != nil {
			t.Fatal(err)
		}
	}
	var err error
	if claudeChat, err = a.store.CreateSession(t.Context(), "wolf", nil, "", "claude", "", ""); err != nil {
		t.Fatal(err)
	}
	add(claudeChat, "bob.user_message", `{"text":"Sketch the homepage hero for the website","user_email":"kai@example.com"}`)
	add(claudeChat, "stream_event", `{"type":"stream_event","event":{}}`)
	add(claudeChat, "assistant", `{"message":{"content":[{"type":"text","text":"The hero leads with a ferry photograph."},{"type":"tool_use","id":"t1","name":"Read","input":{"file_path":"/project/work/site/index.html"}}]}}`)
	add(claudeChat, "user", `{"message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"<html>…"}]}}`)
	add(claudeChat, "assistant", `{"parent_tool_use_id":"t9","message":{"content":[{"type":"text","text":"a sub-agent's aside"}]}}`)
	add(claudeChat, "bob.turn_done", `{}`)

	if codexChat, err = a.store.CreateSession(t.Context(), "wolf", nil, "", "codex", "", ""); err != nil {
		t.Fatal(err)
	}
	add(codexChat, "bob.user_message", `{"text":"What colours did we settle on?","user_email":"richard@example.com"}`)
	add(codexChat, "item/completed", `{"params":{"item":{"type":"agentMessage","text":"Navy and a warm orange for the website."}}}`)
	add(codexChat, "item.completed", `{"item":{"type":"agent_message","text":"Older format: the website font is Inter."}}`)

	if devChat, err = a.store.CreateSession(t.Context(), "dev", nil, "", "claude", "", ""); err != nil {
		t.Fatal(err)
	}
	add(devChat, "bob.user_message", `{"text":"A secret website plan in another project","user_email":"kai@example.com"}`)
	return a, claudeChat, codexChat, devChat
}

func TestMCPKeyCaller(t *testing.T) {
	a, _, _, _ := chatToolsApp(t)
	outside := whoami
	outside.ForKeys = true
	a.mcp.Register(outside)
	code, c := callWhoami(t, a, wolfKey)
	if want := (mcp.Caller{Project: "wolf", User: "key:wolf", APIBase: "https://bob.example"}); code != 200 || c != want {
		t.Errorf("key caller: %d %+v\nwant %+v", code, c, want)
	}
	for name, bad := range map[string]string{"one char off": wolfKey[:len(wolfKey)-1] + "x", "prefix": wolfKey[:32], "longer": wolfKey + "x"} {
		if code, _ := callWhoami(t, a, bad); code != http.StatusUnauthorized {
			t.Errorf("%s: status %d, want 401", name, code)
		}
	}
}

// An outside caller sees reading tools and memory, never the ones that change workers or
// schedules or need a chat of its own.
func TestMCPKeyToolList(t *testing.T) {
	a, _, _, _ := chatToolsApp(t)
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Header.Set("Authorization", "Bearer "+wolfKey)
	res := httptest.NewRecorder()
	a.routes().ServeHTTP(res, req)
	var r struct {
		Result struct{ Tools []struct{ Name string } }
	}
	if err := json.Unmarshal(res.Body.Bytes(), &r); err != nil {
		t.Fatalf("%v: %s", err, res.Body)
	}
	var names []string
	for _, tool := range r.Result.Tools {
		names = append(names, tool.Name)
	}
	want := "bob_whoami worker_create worker_update worker_list worker_delete project_prompt_get project_prompt_set " +
		"schedule_create schedule_update schedule_list schedule_delete memory_create memory_search memory_get memory_current " +
		"chat_list chat_read chat_search chat_create chat_send"
	if got := strings.Join(names, " "); got != want {
		t.Errorf("tools = %s\nwant    %s", got, want)
	}
}

func TestChatList(t *testing.T) {
	a, claudeChat, codexChat, _ := chatToolsApp(t)
	status, isErr, text := callTool(t, a, wolfKey, "chat_list", `{}`)
	if status != 200 || isErr {
		t.Fatalf("chat_list = %d %v %s", status, isErr, text)
	}
	var got struct {
		Chats []struct{ ID, Title, URL string }
	}
	json.Unmarshal([]byte(text), &got)
	if len(got.Chats) != 2 || got.Chats[0].ID != codexChat.ID || got.Chats[1].ID != claudeChat.ID {
		t.Fatalf("chats = %+v", got.Chats)
	}
	if got.Chats[1].Title != "Sketch the homepage hero for the website" || got.Chats[1].URL != "https://bob.example/#/p/wolf/s/"+claudeChat.ID {
		t.Errorf("claude chat = %+v", got.Chats[1])
	}
}

func TestChatRead(t *testing.T) {
	a, claudeChat, codexChat, devChat := chatToolsApp(t)
	read := func(args string) []chatMessage {
		t.Helper()
		status, isErr, text := callTool(t, a, wolfKey, "chat_read", args)
		if status != 200 || isErr {
			t.Fatalf("chat_read %s = %d %v %s", args, status, isErr, text)
		}
		var got struct{ Messages []chatMessage }
		json.Unmarshal([]byte(text), &got)
		return got.Messages
	}
	lines := func(ms []chatMessage) string {
		var out []string
		for _, m := range ms {
			out = append(out, m.Role+": "+m.Text)
		}
		return strings.Join(out, "\n")
	}

	msgs := read(`{"chat_id":"` + claudeChat.ID + `"}`)
	if got, want := lines(msgs), "user: Sketch the homepage hero for the website\nassistant: The hero leads with a ferry photograph."; got != want {
		t.Errorf("claude chat:\n%s\nwant\n%s", got, want)
	}
	if msgs[0].From != "kai@example.com" {
		t.Errorf("from = %q", msgs[0].From)
	}
	got := lines(read(`{"chat_id":"` + claudeChat.ID + `","include_tools":true}`))
	if want := "user: Sketch the homepage hero for the website\nassistant: The hero leads with a ferry photograph.\n" +
		`tool: Read {"file_path":"/project/work/site/index.html"}` + "\ntool_result: <html>…"; got != want {
		t.Errorf("with tools:\n%s\nwant\n%s", got, want)
	}
	if got, want := lines(read(`{"chat_id":"`+codexChat.ID+`"}`)),
		"user: What colours did we settle on?\nassistant: Navy and a warm orange for the website.\nassistant: Older format: the website font is Inter."; got != want {
		t.Errorf("codex chat:\n%s\nwant\n%s", got, want)
	}

	for _, id := range []string{devChat.ID, "not-a-uuid", "0b2f7c1e-8a4d-4f0b-9c3e-1d2a3b4c5d6e"} {
		if _, isErr, text := callTool(t, a, wolfKey, "chat_read", `{"chat_id":"`+id+`"}`); !isErr {
			t.Errorf("chat_read %s: read %s", id, text)
		}
	}
}

func TestChatReadPages(t *testing.T) {
	a, claudeChat, _, _ := chatToolsApp(t)
	long := strings.Repeat("word ", chatReadChars/5+10)
	payload, _ := json.Marshal(map[string]string{"text": long})
	for range 3 {
		if _, err := a.store.AppendEvent(t.Context(), store.Event{SessionID: claudeChat.ID, Engine: "claude", Kind: "bob.user_message", Payload: payload}); err != nil {
			t.Fatal(err)
		}
	}
	var after int64
	total := 0
	for page := 0; ; page++ {
		if page > 5 {
			t.Fatal("never ends")
		}
		_, _, text := callTool(t, a, wolfKey, "chat_read", `{"chat_id":"`+claudeChat.ID+`","after":`+jsonInt(after)+`}`)
		var got struct {
			Messages  []chatMessage
			NextAfter int64 `json:"next_after"`
		}
		json.Unmarshal([]byte(text), &got)
		total += len(got.Messages)
		if got.NextAfter == 0 {
			break
		}
		after = got.NextAfter
	}
	if total != 5 { // the two short messages and the three long ones, each once
		t.Errorf("read %d messages over all pages, want 5", total)
	}
}

func jsonInt(n int64) string { b, _ := json.Marshal(n); return string(b) }

func TestChatSearch(t *testing.T) {
	a, claudeChat, codexChat, _ := chatToolsApp(t)
	status, isErr, text := callTool(t, a, wolfKey, "chat_search", `{"query":"website"}`)
	if status != 200 || isErr {
		t.Fatalf("chat_search = %d %v %s", status, isErr, text)
	}
	var got struct{ Hits []store.ChatHit }
	json.Unmarshal([]byte(text), &got)
	chats := map[string]int{}
	for _, h := range got.Hits {
		chats[h.SessionID]++
		if !strings.Contains(strings.ToLower(h.Snippet), "**website**") {
			t.Errorf("snippet = %q", h.Snippet)
		}
	}
	// the claude chat's question, the codex chat's two replies; not dev's chat, not the sub-agent
	if len(got.Hits) != 3 || chats[claudeChat.ID] != 1 || chats[codexChat.ID] != 2 {
		t.Errorf("hits = %+v", got.Hits)
	}

	_, _, text = callTool(t, a, wolfKey, "chat_search", `{"query":"ferry photograph"}`)
	json.Unmarshal([]byte(text), &got)
	if len(got.Hits) != 1 || got.Hits[0].Role != "assistant" || got.Hits[0].SessionID != claudeChat.ID {
		t.Errorf("ferry hits = %+v", got.Hits)
	}
	if _, isErr, _ := callTool(t, a, wolfKey, "chat_search", `{"query":" "}`); !isErr {
		t.Error("empty query was accepted")
	}
}

func TestClientLabel(t *testing.T) {
	for in, want := range map[string]string{
		"":                       "",
		" kai/enc-website ":      "kai/enc-website",
		"bob-website-design":     "bob-website-design",
		"x\ny":                   "",
		"(paren)":                "",
		strings.Repeat("a", 61):  "",
		"jack@badcode.tv laptop": "jack@badcode.tv laptop",
	} {
		if got := clientLabel(in); got != want {
			t.Errorf("clientLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

// A memory written with a key belongs to the key's project and no chat, and says it came from the
// key, with the client's label when it sends one.
func TestMemoryCreateWithKey(t *testing.T) {
	a, _, _, _ := chatToolsApp(t)
	status, isErr, text := callTool(t, a, wolfKey, "memory_create", `{"content":"Designs live in the website artifact.","labels":{"kind":"note"}}`)
	if status != 200 || isErr {
		t.Fatalf("memory_create = %d %v %s", status, isErr, text)
	}
	var m memoryRecord
	json.Unmarshal([]byte(text), &m)
	if m.CreatedBySession != "" || m.ChatURL != "" || m.CreatedBy != "key:wolf" {
		t.Errorf("memory = %+v", m)
	}

	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"memory_create","arguments":{"content":"Labelled."}}}`))
	req.Header.Set("Authorization", "Bearer "+wolfKey)
	req.Header.Set("X-Bob-Client", "bob-website-design")
	res := httptest.NewRecorder()
	a.routes().ServeHTTP(res, req)
	if !strings.Contains(res.Body.String(), `\"created_by\":\"key:wolf (bob-website-design)\"`) {
		t.Errorf("labelled memory_create = %s", res.Body)
	}
	if _, err := a.store.Memory(t.Context(), "wolf", m.ID); err != nil {
		t.Errorf("not in wolf: %v", err)
	}
}

func TestMCPKeysFromEnv(t *testing.T) {
	key := strings.Repeat("k", 32)
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	keys, err := mcpKeysFromEnv(env(map[string]string{"BOB_MCP_KEY_MY_PROJ": key}), []string{"enc", "my-proj"})
	if err != nil || len(keys) != 1 || keys["my-proj"] != key {
		t.Errorf("keys = %v, %v", keys, err)
	}
	if _, err := mcpKeysFromEnv(env(map[string]string{"BOB_MCP_KEY_ENC": "short"}), []string{"enc"}); err == nil || !strings.Contains(err.Error(), "BOB_MCP_KEY_ENC") {
		t.Errorf("short key: %v", err)
	}
	if _, err := mcpKeysFromEnv(env(map[string]string{"BOB_MCP_KEY_ENC": key, "BOB_MCP_KEY_WOLF": key}), []string{"enc", "wolf"}); err == nil || !strings.Contains(err.Error(), "own key") {
		t.Errorf("shared key: %v", err)
	}
}

// An outside caller can start a chat on a worker and get the agent's reply back, and send the
// chat another message; the messages are recorded as sent by the key.
func TestChatCreateAndSend(t *testing.T) {
	a, _, _, _ := chatToolsApp(t)
	rt := chatRuntime[a]
	rt.reply = "The hero should lead with the ferry."
	status, isErr, text := callTool(t, a, wolfKey, "chat_create", `{"worker":"researcher","message":"What leads the hero?"}`)
	if status != 200 || isErr {
		t.Fatalf("chat_create = %d %v %s", status, isErr, text)
	}
	var got struct {
		ChatID             string `json:"chat_id"`
		Status, Reply, URL string
	}
	json.Unmarshal([]byte(text), &got)
	if got.Status != "done" || got.Reply != "The hero should lead with the ferry." || got.URL != "https://bob.example/#/p/wolf/s/"+got.ChatID {
		t.Fatalf("chat_create = %s", text)
	}
	sess, err := a.store.Session(t.Context(), got.ChatID)
	if err != nil || sess.Project != "wolf" || sess.Worker != "researcher" {
		t.Fatalf("session = %+v, %v", sess, err)
	}
	if who, _ := a.store.LastUserEmail(t.Context(), sess.ID); who != "key:wolf" {
		t.Errorf("sent by %q", who)
	}

	rt.reply = "Navy."
	_, isErr, text = callTool(t, a, wolfKey, "chat_send", `{"chat_id":"`+got.ChatID+`","message":"And the colour?"}`)
	json.Unmarshal([]byte(text), &got)
	if isErr || got.Status != "done" || got.Reply != "Navy." {
		t.Fatalf("chat_send = %s", text)
	}
	turns, _ := rt.sent()
	if len(turns) != 2 || turns[1].Text != "And the colour?" || turns[1].Resume != "h-"+got.ChatID || turns[1].UserEmail != "key:wolf" {
		t.Errorf("turns = %+v", turns)
	}
}

func TestChatCreateRefusals(t *testing.T) {
	a, _, _, devChat := chatToolsApp(t)
	for args, want := range map[string]string{
		`{"message":"hi"}`:                                       "engine",
		`{"message":"hi","worker":"nobody"}`:                     "no worker",
		`{"message":" ","engine":"claude"}`:                      "message",
		`{"message":"hi","engine":"claude","effort":"huge"}`:     "effort",
		`{"message":"hi","engine":"claude","wait_seconds":9999}`: "wait_seconds",
	} {
		if _, isErr, text := callTool(t, a, wolfKey, "chat_create", args); !isErr || !strings.Contains(text, want) {
			t.Errorf("chat_create %s = %v %s, want an error about %s", args, isErr, text, want)
		}
	}
	if _, isErr, _ := callTool(t, a, wolfKey, "chat_send", `{"chat_id":"`+devChat.ID+`","message":"hi"}`); !isErr {
		t.Error("chat_send reached another project's chat")
	}
}

// A turn still running answers status running at once with wait_seconds 0, and a second message
// to the same chat is refused until it ends.
func TestChatSendWhileRunning(t *testing.T) {
	a, claudeChat, _, _ := chatToolsApp(t)
	rt := chatRuntime[a]
	rt.hold = make(chan struct{})
	rt.started = make(chan string, 1)
	_, isErr, text := callTool(t, a, wolfKey, "chat_send", `{"chat_id":"`+claudeChat.ID+`","message":"long job","wait_seconds":0}`)
	if isErr || !strings.Contains(text, `"status":"running"`) {
		t.Fatalf("chat_send = %s", text)
	}
	<-rt.started
	if _, isErr, text := callTool(t, a, wolfKey, "chat_send", `{"chat_id":"`+claudeChat.ID+`","message":"again"}`); !isErr || !strings.Contains(text, "still running") {
		t.Errorf("second chat_send = %v %s", isErr, text)
	}
	if _, _, text := callTool(t, a, wolfKey, "chat_read", `{"chat_id":"`+claudeChat.ID+`"}`); !strings.Contains(text, `"running":true`) {
		t.Errorf("chat_read while running = %s", text)
	}
	close(rt.hold)
	deadline := time.Now().Add(5 * time.Second)
	for a.turnRunning(claudeChat.ID) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if a.turnRunning(claudeChat.ID) {
		t.Fatal("turn never ended")
	}
}

// Agents inside Bob do not get chat_create or chat_send: a chat starting chats could loop.
func TestChatCreateIsOutsideOnly(t *testing.T) {
	a, claudeChat, _, _ := chatToolsApp(t)
	tok := mcpToken(a.auth.Secret, claudeChat.ID)
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Header.Set("Authorization", "Bearer "+tok)
	res := httptest.NewRecorder()
	a.routes().ServeHTTP(res, req)
	if strings.Contains(res.Body.String(), "chat_create") || strings.Contains(res.Body.String(), "chat_send") || !strings.Contains(res.Body.String(), "chat_read") {
		t.Errorf("a chat's tools = %s", res.Body)
	}
}

// The project prompt is read and replaced with a key, and the change is in its history as the key's.
func TestProjectPromptWithKey(t *testing.T) {
	a, _, _, _ := chatToolsApp(t)
	if _, isErr, text := callTool(t, a, wolfKey, "project_prompt_set", `{"prompt":"Be brief."}`); !isErr || !strings.Contains(text, "why") {
		t.Errorf("no why = %v %s", isErr, text)
	}
	if _, isErr, text := callTool(t, a, wolfKey, "project_prompt_set", `{"prompt":"Be brief.","why":"tone"}`); isErr {
		t.Fatalf("set = %s", text)
	}
	_, isErr, text := callTool(t, a, wolfKey, "project_prompt_get", "")
	var got struct {
		Prompt   string
		Versions []struct {
			Prompt, Why string
			By          string `json:"created_by"`
		}
	}
	json.Unmarshal([]byte(text), &got)
	if isErr || got.Prompt != "Be brief." || len(got.Versions) == 0 || got.Versions[0].Why != "tone" {
		t.Errorf("get = %s", text)
	}
	if !strings.Contains(text, "key:wolf") {
		t.Errorf("history does not name the key: %s", text)
	}
}

// Workers are created with a key, and their history says so.
func TestWorkerCreateWithKey(t *testing.T) {
	a, _, _, _ := chatToolsApp(t)
	_, isErr, text := callTool(t, a, wolfKey, "worker_create", `{"name":"designer","engine":"claude","prompt":"Design.","why":"website work"}`)
	if isErr {
		t.Fatalf("worker_create = %s", text)
	}
	wk, err := a.store.Worker(t.Context(), "wolf", "designer")
	if err != nil {
		t.Fatal(err)
	}
	versions, err := a.store.WorkerVersions(t.Context(), wk.ID)
	if err != nil || len(versions) != 1 || !strings.Contains(fmt.Sprintf("%+v", versions[0]), "key:wolf") {
		t.Errorf("versions = %+v, %v", versions, err)
	}
}
