package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/badcodetv/bob/internal/embed"
	"github.com/badcodetv/bob/internal/mcp"
	"github.com/badcodetv/bob/internal/store"
)

const wolfKey = "wolf-key-0123456789abcdef0123456789abcdef"

// chatToolsApp serves every tool main.go registers, with an MCP key for project wolf, and a wolf
// chat holding a short Claude conversation and a Codex one, plus a dev chat (another project).
func chatToolsApp(t *testing.T) (a *app, claudeChat, codexChat, devChat store.Session) {
	t.Helper()
	a, _, _ = newScheduleApp(t)
	if err := a.store.ReconcileProjects(t.Context(), []string{"wolf", "dev"}); err != nil {
		t.Fatal(err)
	}
	a.embed = embed.Fake{}
	a.publicURL = "https://bob.example"
	a.mcpKeys = map[string]string{"wolf": wolfKey}
	a.mcp = mcp.New()
	a.mcp.Register(bobWhoami)
	a.mcp.Register(a.workerTools()...)
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
	want := "bob_whoami worker_list schedule_list memory_create memory_search memory_get memory_current chat_list chat_read chat_search"
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

// A memory written with a key belongs to the key's project and no chat.
func TestMemoryCreateWithKey(t *testing.T) {
	a, _, _, _ := chatToolsApp(t)
	status, isErr, text := callTool(t, a, wolfKey, "memory_create", `{"content":"Designs live in the website artifact.","labels":{"kind":"note"}}`)
	if status != 200 || isErr {
		t.Fatalf("memory_create = %d %v %s", status, isErr, text)
	}
	var m memoryRecord
	json.Unmarshal([]byte(text), &m)
	if m.CreatedBySession != "" || m.ChatURL != "" {
		t.Errorf("memory = %+v", m)
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
