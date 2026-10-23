package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/badcodetv/bob/internal/auth"
	"github.com/badcodetv/bob/internal/mcp"
	"github.com/badcodetv/bob/internal/store"
)

// chatTools returns chat_list, chat_read and chat_search, reading the project's chats, for an
// agent inside Bob and for a client outside it (a project MCP key); and chat_create and chat_send,
// which start a turn, for an outside client only: an agent starting other chats could start
// itself again, without end.
func (a *app) chatTools() []mcp.Tool {
	return []mcp.Tool{
		{
			Name:    "chat_list",
			ForKeys: true,
			Description: "Lists this project's chats, most recently active first: each one's id, title (its " +
				"first message), worker, how many messages people sent, when it was last active, and its link.",
			InputSchema: chatListSchema,
			Call:        a.chatList,
		},
		{
			Name:    "chat_read",
			ForKeys: true,
			Description: "Reads one of this project's chats as a transcript: people's messages and the agent's " +
				"replies, oldest first. include_tools adds the tools the agent called, shortened. A long chat " +
				"comes back in parts: pass next_after as after to read on.",
			InputSchema: chatReadSchema,
			Call:        a.chatRead,
		},
		{
			Name:    "chat_search",
			ForKeys: true,
			Description: "Searches the messages of this project's chats (people's and the agent's replies, not " +
				"tool output) by keyword, best match first. Query syntax as a web search: words, \"a phrase\", " +
				"-excluded, or. Read a hit's chat with chat_read.",
			InputSchema: chatSearchSchema,
			Call:        a.chatSearch,
		},
		{
			Name:      "chat_create",
			ForKeys:   true,
			Available: mcp.Caller.Outside,
			Description: "Starts a new chat in this project and sends it message, as a person would in the " +
				"web app: on a worker (it runs with the worker's engine, prompt and tools), or a plain chat " +
				"on an engine. The agent's turn runs in Bob; this waits up to wait_seconds (default 120) for " +
				"it and returns its reply, or status \"running\" — then read on with chat_read. " + chatTurnNote,
			InputSchema: chatCreateSchema,
			Call:        a.chatCreate,
		},
		{
			Name:      "chat_send",
			ForKeys:   true,
			Available: mcp.Caller.Outside,
			Description: "Sends message to one of this project's chats and waits, like chat_create, for the " +
				"agent's reply. Refused while the chat is still running a turn. " + chatTurnNote,
			InputSchema: chatSendSchema,
			Call:        a.chatSend,
		},
	}
}

const chatTurnNote = "The message is recorded as sent by this MCP key (and the client's X-Bob-Client " +
	"label); people see it in the web app like any other."

var chatListSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "limit": {"type": "integer", "description": "How many chats, default 20, at most 100."},
    "worker": {"type": "string", "description": "Only this worker's chats; \"\" for all."}
  }
}`)

var chatReadSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "chat_id": {"type": "string", "description": "The chat's id (chat_list, chat_search)."},
    "after": {"type": "integer", "description": "Start after this event id (a previous read's next_after)."},
    "include_tools": {"type": "boolean", "description": "Also show the tools the agent called and their results, shortened."}
  },
  "required": ["chat_id"]
}`)

var chatSearchSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "query": {"type": "string"},
    "limit": {"type": "integer", "description": "How many messages, default 10, at most 50."}
  },
  "required": ["query"]
}`)

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

var chatCreateSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "message": {"type": "string", "description": "The first message."},
    "worker": {"type": "string", "description": "The worker to run (worker_list); leave out for a plain chat."},
    "engine": {"type": "string", "enum": ["claude", "codex"], "description": "A plain chat's engine; a worker chat uses the worker's."},
    "model": {"type": "string", "description": "Overrides the worker's model; \"\" for its own."},
    "effort": {"type": "string", "description": "Overrides the worker's effort; \"\" for its own."},
    "wait_seconds": {"type": "integer", "description": "How long to wait for the reply: default 120, at most 600, 0 to return at once."}
  },
  "required": ["message"]
}`)

var chatSendSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "chat_id": {"type": "string"},
    "message": {"type": "string"},
    "wait_seconds": {"type": "integer", "description": "How long to wait for the reply: default 120, at most 600, 0 to return at once."}
  },
  "required": ["chat_id", "message"]
}`)

const (
	chatWaitDefault = 120 * time.Second
	chatWaitMax     = 600 * time.Second
	chatReadChars   = 40000 // a chat_read result holds about this much text, then stops at a message
	chatToolChars   = 300   // a tool call's arguments or result, when include_tools
	chatTitleChars  = 200
)

func (a *app) chatList(ctx context.Context, c mcp.Caller, args json.RawMessage) (any, error) {
	var in struct {
		Limit  int    `json:"limit"`
		Worker string `json:"worker"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, err
		}
	}
	if in.Limit <= 0 {
		in.Limit = 20
	}
	in.Limit = min(in.Limit, 100)
	sessions, err := a.store.Sessions(ctx, c.Project)
	if err != nil {
		return nil, err
	}
	type chat struct {
		ID           string     `json:"id"`
		Title        string     `json:"title"`
		Worker       string     `json:"worker,omitempty"`
		Engine       string     `json:"engine"`
		Schedule     string     `json:"schedule,omitempty"`
		Messages     int        `json:"messages"`
		CreatedAt    time.Time  `json:"created_at"`
		LastActiveAt *time.Time `json:"last_active_at"`
		URL          string     `json:"url"`
	}
	out := []chat{}
	for _, s := range sessions {
		if in.Worker != "" && s.Worker != in.Worker {
			continue
		}
		out = append(out, chat{ID: s.ID, Title: shorten(s.Title, chatTitleChars), Worker: s.Worker, Engine: s.Engine,
			Schedule: s.Schedule, Messages: s.Messages, CreatedAt: s.CreatedAt, LastActiveAt: s.LastActiveAt,
			URL: a.chatURL(c.Project, s.ID)})
		if len(out) == in.Limit {
			break
		}
	}
	return map[string]any{"chats": out}, nil
}

// chatMessage is one line of a chat_read transcript.
type chatMessage struct {
	EventID   int64     `json:"event_id"`
	Role      string    `json:"role"` // user, assistant, tool, tool_result, error
	From      string    `json:"from,omitempty"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"created_at"`
}

func (a *app) chatRead(ctx context.Context, c mcp.Caller, args json.RawMessage) (any, error) {
	var in struct {
		ChatID       string `json:"chat_id"`
		After        int64  `json:"after"`
		IncludeTools bool   `json:"include_tools"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, err
	}
	sess, err := a.projectSession(ctx, c, in.ChatID)
	if err != nil {
		return nil, err
	}
	events, err := a.store.Events(ctx, sess.ID, in.After)
	if err != nil {
		return nil, err
	}
	messages, size, next := []chatMessage{}, 0, int64(0)
	for _, e := range events {
		lines := eventMessages(e, in.IncludeTools)
		if len(lines) == 0 {
			continue
		}
		if size > chatReadChars {
			next = e.ID - 1 // the next read starts at this event
			break
		}
		for _, m := range lines {
			size += len(m.Text)
		}
		messages = append(messages, lines...)
	}
	out := map[string]any{"chat_id": sess.ID, "worker": sess.Worker, "engine": sess.Engine, "created_at": sess.CreatedAt,
		"url": a.chatURL(c.Project, sess.ID), "running": a.turnRunning(sess.ID), "messages": messages}
	if next != 0 {
		out["next_after"] = next
	}
	return out, nil
}

// eventMessages is what an event says as transcript lines, following the web app's engine
// converters (web/src/engines): people's messages, replies, failures, and with tools, the calls
// and their results. Most events (streaming, bookkeeping, sub-agents) say nothing.
func eventMessages(e store.Event, tools bool) []chatMessage {
	var p map[string]any
	if json.Unmarshal(e.Payload, &p) != nil {
		return nil
	}
	var out []chatMessage
	say := func(role, text string) {
		if strings.TrimSpace(text) != "" {
			out = append(out, chatMessage{EventID: e.ID, Role: role, Text: text, CreatedAt: e.CreatedAt})
		}
	}
	tool := func(name string, args any) {
		if tools {
			say("tool", name+" "+shorten(compactJSON(args), chatToolChars))
		}
	}
	result := func(text string) {
		if tools {
			say("tool_result", shorten(text, chatToolChars))
		}
	}
	switch e.Kind {
	case "bob.user_message":
		say("user", str(p["text"]))
		if len(out) == 1 {
			out[0].From = str(p["user_email"])
		}
	case "bob.turn_failed":
		say("error", str(p["error"]))
	case "assistant": // claude
		if str(p["parent_tool_use_id"]) != "" {
			return nil
		}
		for _, b := range blocks(jsonAt(p, "message", "content")) {
			switch str(b["type"]) {
			case "text":
				say("assistant", str(b["text"]))
			case "tool_use":
				tool(str(b["name"]), b["input"])
			}
		}
	case "user": // claude: tool results
		if str(p["parent_tool_use_id"]) != "" {
			return nil
		}
		for _, b := range blocks(jsonAt(p, "message", "content")) {
			if str(b["type"]) == "tool_result" {
				result(contentText(b["content"]))
			}
		}
	case "item/completed": // codex app-server
		item, _ := jsonAt(p, "params", "item").(map[string]any)
		switch str(item["type"]) {
		case "agentMessage", "plan":
			say("assistant", str(item["text"]))
		case "commandExecution":
			tool("command_execution", item["command"])
			result(str(item["aggregatedOutput"]))
		case "mcpToolCall":
			tool(str(item["server"])+"."+str(item["tool"]), item["arguments"])
			result(contentText(jsonAt(item, "result", "content")))
		case "fileChange":
			tool("file_change", item["changes"])
		case "webSearch":
			tool("web_search", item["query"])
		}
	case "item.completed": // codex exec, older chats
		item, _ := p["item"].(map[string]any)
		switch str(item["type"]) {
		case "agent_message":
			say("assistant", str(item["text"]))
		case "command_execution":
			tool("command_execution", item["command"])
			result(str(item["aggregated_output"]))
		case "mcp_tool_call":
			tool(str(item["server"])+"."+str(item["tool"]), item["arguments"])
			result(contentText(jsonAt(item, "result", "content")))
		case "file_change":
			tool("file_change", item["changes"])
		case "web_search":
			tool("web_search", item["query"])
		case "error":
			say("error", str(item["message"]))
		}
	case "turn/completed":
		if str(jsonAt(p, "params", "turn", "status")) == "failed" {
			say("error", str(jsonAt(p, "params", "turn", "error", "message")))
		}
	}
	return out
}

func (a *app) chatCreate(ctx context.Context, c mcp.Caller, args json.RawMessage) (any, error) {
	var in struct {
		Message, Worker, Engine, Model, Effort string
		WaitSeconds                            *int `json:"wait_seconds"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.Message) == "" {
		return nil, errors.New("message is required")
	}
	wait, err := chatWait(in.WaitSeconds)
	if err != nil {
		return nil, err
	}
	var sess store.Session
	if in.Worker == "" {
		if in.Engine != "claude" && in.Engine != "codex" {
			return nil, errors.New("a plain chat needs engine: claude or codex (or name a worker)")
		}
		if err := settingsError(in.Engine, in.Model, in.Effort); err != nil {
			return nil, err
		}
		sess, err = a.store.CreateSession(ctx, c.Project, nil, "", in.Engine, in.Model, in.Effort)
	} else {
		wk, werr := a.store.Worker(ctx, c.Project, in.Worker)
		if errors.Is(werr, store.ErrNotFound) {
			return nil, fmt.Errorf("no worker named %q in this project (worker_list)", in.Worker)
		}
		if werr != nil {
			return nil, werr
		}
		if err := settingsError(wk.Engine, in.Model, in.Effort); err != nil {
			return nil, err
		}
		sess, err = a.store.CreateSession(ctx, c.Project, &wk.ID, wk.Name, wk.Engine, in.Model, in.Effort)
	}
	if err != nil {
		return nil, err
	}
	return a.chatTurn(ctx, c, sess, in.Message, wait)
}

func (a *app) chatSend(ctx context.Context, c mcp.Caller, args json.RawMessage) (any, error) {
	var in struct {
		ChatID      string `json:"chat_id"`
		Message     string `json:"message"`
		WaitSeconds *int   `json:"wait_seconds"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.Message) == "" {
		return nil, errors.New("message is required")
	}
	wait, err := chatWait(in.WaitSeconds)
	if err != nil {
		return nil, err
	}
	sess, err := a.projectSession(ctx, c, in.ChatID)
	if err != nil {
		return nil, err
	}
	return a.chatTurn(ctx, c, sess, in.Message, wait)
}

func chatWait(seconds *int) (time.Duration, error) {
	switch {
	case seconds == nil:
		return chatWaitDefault, nil
	case *seconds < 0 || time.Duration(*seconds)*time.Second > chatWaitMax:
		return 0, fmt.Errorf("wait_seconds: 0 to %d", int(chatWaitMax.Seconds()))
	}
	return time.Duration(*seconds) * time.Second, nil
}

// chatTurn sends message to sess as the caller and runs the turn in the background, as the web
// app does; it waits up to wait for the turn to end and returns what the agent replied.
func (a *app) chatTurn(ctx context.Context, c mcp.Caller, sess store.Session, message string, wait time.Duration) (any, error) {
	userEvent, run, err := a.startTurn(ctx, sess, auth.User{Email: c.User, Name: c.User}, message)
	if errors.Is(err, errBusy) {
		return nil, errors.New("this chat is still running a turn: wait for it (chat_read shows running) and send again")
	}
	if err != nil {
		return nil, err
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = run()
	}()
	out := map[string]any{"chat_id": sess.ID, "url": a.chatURL(c.Project, sess.ID), "message_event_id": userEvent.ID}
	// The turn runs on whether or not anyone waits: a caller that gives up still has the chat.
	select {
	case <-done:
	case <-time.After(wait):
		out["status"] = "running"
		return out, nil
	case <-ctx.Done():
		out["status"] = "running"
		return out, nil
	}
	events, err := a.store.Events(ctx, sess.ID, userEvent.ID)
	if err != nil {
		return nil, err
	}
	status, reply := "done", []string{}
	for _, e := range events {
		for _, m := range eventMessages(e, false) {
			if m.Role == "error" {
				status = "failed"
			}
			reply = append(reply, m.Text)
		}
	}
	out["status"], out["reply"] = status, strings.Join(reply, "\n\n")
	return out, nil
}

// projectSession is the caller's project's chat id, or an error a model can act on.
func (a *app) projectSession(ctx context.Context, c mcp.Caller, id string) (store.Session, error) {
	if !uuidPattern.MatchString(id) {
		return store.Session{}, errors.New("chat_id must be a chat's id, from chat_list or chat_search")
	}
	sess, err := a.store.Session(ctx, id)
	if errors.Is(err, store.ErrNotFound) || err == nil && sess.Project != c.Project {
		return store.Session{}, fmt.Errorf("no chat %s in project %s", id, c.Project)
	}
	return sess, err
}

func (a *app) turnRunning(sessionID string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	_, ok := a.turns[sessionID]
	return ok
}

func (a *app) chatSearch(ctx context.Context, c mcp.Caller, args json.RawMessage) (any, error) {
	var in struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.Query) == "" {
		return nil, errors.New("query is required")
	}
	if in.Limit <= 0 {
		in.Limit = 10
	}
	in.Limit = min(in.Limit, 50)
	hits, err := a.store.SearchChats(ctx, c.Project, in.Query, in.Limit)
	if err != nil {
		return nil, err
	}
	type hit struct {
		store.ChatHit
		URL string `json:"url"`
	}
	out := make([]hit, len(hits))
	for i, h := range hits {
		out[i] = hit{h, a.chatURL(c.Project, h.SessionID)}
	}
	return map[string]any{"hits": out}, nil
}

// jsonAt walks nested JSON objects; nil where a step is missing.
func jsonAt(v any, keys ...string) any {
	for _, k := range keys {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[k]
	}
	return v
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

// blocks is a content array's objects (a Claude message's content may also be a plain string).
func blocks(v any) []map[string]any {
	list, _ := v.([]any)
	var out []map[string]any
	for _, b := range list {
		if m, ok := b.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// contentText is a tool result's content as text: a string, or the text of its text blocks.
func contentText(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	var parts []string
	for _, b := range blocks(v) {
		if str(b["type"]) == "text" {
			parts = append(parts, str(b["text"]))
		} else {
			parts = append(parts, "["+str(b["type"])+"]")
		}
	}
	return strings.Join(parts, "\n")
}

func compactJSON(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// shorten cuts s to about n bytes, at a rune boundary, marking the cut.
func shorten(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}
