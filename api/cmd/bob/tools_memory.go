package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/badcodetv/bob/internal/labels"
	"github.com/badcodetv/bob/internal/mcp"
	"github.com/badcodetv/bob/internal/store"
)

// memoryTools returns memory_create, memory_search, memory_get and memory_current: a project's
// shared, permanent memory store. There is no memory_update and no memory_delete — a memory is a
// record of a moment, not a value to mutate; "changing" one means appending a newer one (the
// name= convention, CurrentMemory) or writing a retracts=<id> memory to withdraw it (T21's
// notRetracted). Ported from agent-bob go/cmd/agentd/mcp_memory.go, leanly: no embed:false /
// degrade-on-write (T21's Embedder always succeeds or the create fails), no session permalink
// type beyond chat_url built straight from BOB_PUBLIC_URL.
func (a *app) memoryTools() []mcp.Tool {
	return []mcp.Tool{
		{
			Name:        "memory_create",
			Description: memoryCreateDescription,
			InputSchema: memoryCreateSchema,
			Call:        a.memoryCreate,
		},
		{
			Name:        "memory_search",
			Description: memorySearchDescription,
			InputSchema: memorySearchSchema,
			Call:        a.memorySearch,
		},
		{
			Name:        "memory_get",
			Description: memoryGetDescription,
			InputSchema: memoryGetSchema,
			Call:        a.memoryGet,
		},
		{
			Name:        "memory_current",
			Description: memoryCurrentDescription,
			InputSchema: memoryCurrentSchema,
			Call:        a.memoryCurrent,
		},
	}
}

// ---------------------------------------------------------------------------
// Tool descriptions (ported from agent-bob mcp_memory.go:132-221, trimmed for what T21 dropped)
// ---------------------------------------------------------------------------

var memoryCreateDescription = `Append a memory to this project's shared, permanent memory store.

Memories are append-only and immutable: there is no way to edit or delete one, ever. To "update" ` +
	`something, write a NEW memory with the same labels — readers take the newest match, and the ` +
	`older versions remain as an honest record.

Labels are how memories are found again. They are identifiers, not prose: keys and values must be ` +
	`alphanumeric with '-', '_' or '.', at most 63 characters, at most 32 per memory. Content is ` +
	`content; do not try to put an email subject or a sentence in a label.

Conventions worth knowing: kind=<something> says what sort of memory this is, worker=<name> says ` +
	`who it is about, and name=<x> means "this is the current value of x" (write a new one to ` +
	`change it, read it back with memory_current).

To WITHDRAW something the project got wrong, write a memory labelled retracts=<memory-id> whose ` +
	`content says why. The retracted memory stops reaching searches and memory_current from that ` +
	`moment on, and whatever it was covering up becomes current again — but nothing is deleted: ` +
	`both it and your retraction stay readable by memory_get. Use this for a fact that turned out ` +
	`to be false, not for one that has merely changed (for that, write the new value).

If you are REWRITING the current value of a name= memory that you just read, pass if_current: the ` +
	`id of the memory you read. Your write then lands only if nothing else has written that name in ` +
	`the meantime; if something has, nothing is stored and you are told which memory won, so you ` +
	`can re-read it and fold your work into what it now says. Do not pass it when writing something ` +
	`new rather than replacing a value you read. Content is at most ` + strconv.Itoa(store.MaxMemoryBytes) + ` bytes.`

const memorySearchDescription = `Search this project's memory store. Search before making decisions ` +
	`that earlier work might inform — that is what it is for.

Two independent filters, both optional:
  label_selector — Kubernetes-style, ANDed: "kind=lesson,worker=email-answerer", "kind in ` +
	`(summary, lesson)", "kind!=raw-transcript", "exists thread", "!archived". No OR and no ` +
	`nesting: if you need OR, run two searches.
  query          — free text, ranked by relevance over whatever the selector left.

With no query text you get the filtered set NEWEST FIRST — that is a recency question, not a ` +
	`relevance one. With query text you get a hybrid of exact-word and meaning-based matching, ` +
	`fused into one ranking.

IMPORTANT — read the scores. The ranking has no relevance threshold: it always returns up to ` +
	`'limit' rows, so the tail of a result list is filled with the least-bad matches even when ` +
	`nothing in the store is actually relevant. A low score means "nothing good", NOT "here is a ` +
	`weak but real answer". If the best hit does not visibly answer your question, treat the store ` +
	`as empty on that subject and say so.

Narrowing, all optional and all ANDed with the filters above:
  since / until  — a window over when the memory was written: an RFC3339 timestamp, unix ` +
	`milliseconds, or a relative age such as "7d" or "24h".
  latest_per     — a label KEY. Returns only the NEWEST memory for each distinct value of that ` +
	`label; memories without that key are omitted. This is memory_current generalised from one ` +
	`name to all of them.
  created_by_worker — restrict to memories written by one worker. Pass "self" for your own past ` +
	`work (resolved from who you are, not from anything you type — it cannot be spoofed), or a ` +
	`specific worker name to read another role's memory. Only available to a caller with a worker ` +
	`identity: a plain human chat session has none, and "self" there is refused.

Results are snippets. Use memory_get to read one in full — including after latest_per, which ` +
	`answers WHICH memories are current but still returns them abbreviated. Every hit carries who ` +
	`wrote it, in which session, and a chat_url link to that conversation.`

const memoryGetDescription = `Read one memory in full by id, with its labels and provenance. ` +
	`memory_search returns truncated snippets; this returns the entire content.`

const memoryCurrentDescription = `Read the current value of a named memory: the newest memory ` +
	`labelled name=<name>, in full.

This is the project's key/value convention. Values are updated by APPENDING a new memory with the ` +
	`same name label, so "current" always means "most recent". Equivalent to memory_search with ` +
	`label_selector "name=<name>" and limit 1, then memory_get on the result — but one call.

If nothing has ever been written under that name the result is {"found": false}: that is a normal ` +
	`answer, not an error. Do not invent a value.`

var memoryCreateSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"content": {"type": "string", "description": "The memory body. Arbitrary text; may be large."},
		"labels": {"type": "object", "additionalProperties": {"type": "string"}, "description": "Flat string→string labels. Identifiers only: [A-Za-z0-9] with '-', '_', '.', ≤63 chars, ≤32 labels."},
		"if_current": {"type": "string", "description": "Compare-and-swap, for replacing the current value of a name= memory: the id of the memory you read and are rewriting. Requires a name label. Omit for an ordinary append."}
	},
	"required": ["content"]
}`)

var memorySearchSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"label_selector": {"type": "string", "description": "Kubernetes-style label selector, comma-ANDed. Optional."},
		"query": {"type": "string", "description": "Free-text relevance query. Optional; omit for newest-first."},
		"limit": {"type": "integer", "description": "Maximum hits (default 20, maximum 100)."},
		"since": {"type": "string", "description": "Inclusive lower bound: ` + memTimeFormsHelp + `."},
		"until": {"type": "string", "description": "Inclusive upper bound: ` + memTimeFormsHelp + `."},
		"latest_per": {"type": "string", "description": "A label key. Returns only the newest memory for each distinct value of that label; memories without the key are omitted."},
		"created_by_worker": {"type": "string", "description": "Restrict to memories written by one worker. \"self\" means your own past work, resolved server-side."}
	}
}`)

var memoryGetSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"id": {"type": "string", "description": "The memory id, as returned by memory_search or memory_create."}
	},
	"required": ["id"]
}`)

var memoryCurrentSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"name": {"type": "string", "description": "The value of the name label, e.g. \"label-registry\"."}
	},
	"required": ["name"]
}`)

// ---------------------------------------------------------------------------
// Result shapes
// ---------------------------------------------------------------------------

// memoryRecord is a memory in full — what memory_create, memory_get and memory_current return.
type memoryRecord struct {
	ID               string            `json:"id"`
	Labels           map[string]string `json:"labels"`
	Content          string            `json:"content"`
	CreatedByWorker  string            `json:"created_by_worker"`
	CreatedBySession string            `json:"created_by_session"`
	ChatURL          string            `json:"chat_url"`
	CreatedAt        time.Time         `json:"created_at"`
}

// memoryHit is one search result: a snippet, not the whole body.
type memoryHit struct {
	ID               string            `json:"id"`
	Labels           map[string]string `json:"labels"`
	Snippet          string            `json:"snippet"`
	Score            float64           `json:"score"`
	CreatedByWorker  string            `json:"created_by_worker"`
	CreatedBySession string            `json:"created_by_session"`
	ChatURL          string            `json:"chat_url"`
	CreatedAt        time.Time         `json:"created_at"`
}

// chatURL builds the link to a chat from BOB_PUBLIC_URL, or "" when the memory carries no
// session (the session was deleted, or it never had one).
func (a *app) chatURL(project, session string) string {
	if session == "" {
		return ""
	}
	return a.publicURL + "/#/p/" + project + "/s/" + session
}

func (a *app) memoryRecordOf(m store.Memory) memoryRecord {
	return memoryRecord{ID: m.ID, Labels: m.Labels, Content: m.Content, CreatedByWorker: m.CreatedByWorker,
		CreatedBySession: m.CreatedBySession, ChatURL: a.chatURL(m.Project, m.CreatedBySession), CreatedAt: m.CreatedAt}
}

func (a *app) memoryHitOf(project string, h store.MemoryHit) memoryHit {
	return memoryHit{ID: h.ID, Labels: h.Labels, Snippet: h.Snippet, Score: h.Score, CreatedByWorker: h.CreatedByWorker,
		CreatedBySession: h.CreatedBySession, ChatURL: a.chatURL(project, h.CreatedBySession), CreatedAt: h.CreatedAt}
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

type memoryCreateArgs struct {
	Content   string            `json:"content"`
	Labels    map[string]string `json:"labels"`
	IfCurrent string            `json:"if_current"`
}

func (a *app) memoryCreate(ctx context.Context, c mcp.Caller, args json.RawMessage) (any, error) {
	var in memoryCreateArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.Content) == "" {
		return nil, errors.New("content is required and must not be blank")
	}
	if err := labels.Validate(in.Labels); err != nil {
		return nil, fmt.Errorf("labels: %w", err)
	}
	if len(in.Content) > store.MaxMemoryBytes {
		return nil, fmt.Errorf("this memory is %d bytes, over the %d-byte limit — keep the document elsewhere and remember where", len(in.Content), store.MaxMemoryBytes)
	}
	vec, err := a.embed.Embed(ctx, in.Content)
	if err != nil {
		return nil, fmt.Errorf("could not embed this memory, so it was NOT stored (retry rather than continue): %w", err)
	}
	m := store.Memory{Project: c.Project, Labels: in.Labels, Content: in.Content, CreatedByWorker: c.Worker, CreatedBySession: c.SessionID}
	stored, err := a.store.CreateMemory(ctx, m, vec, in.IfCurrent)
	var notCurrent store.ErrNotCurrent
	if errors.As(err, &notCurrent) {
		return nil, notCurrent
	}
	if err != nil {
		return nil, err
	}
	return a.memoryRecordOf(stored), nil
}

// memorySearchSelfSentinel is "my own past work": resolved server-side from Caller.Worker, never
// trusted from the argument itself — see agent-bob's Decision B3.
const memorySearchSelfSentinel = "self"

type memorySearchArgs struct {
	LabelSelector   string `json:"label_selector"`
	Query           string `json:"query"`
	Limit           int    `json:"limit"`
	Since           string `json:"since"`
	Until           string `json:"until"`
	LatestPer       string `json:"latest_per"`
	CreatedByWorker string `json:"created_by_worker"`
}

func (a *app) memorySearch(ctx context.Context, c mcp.Caller, args json.RawMessage) (any, error) {
	var in memorySearchArgs
	if len(args) > 0 {
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, err
		}
	}
	if in.Limit < 0 {
		return nil, fmt.Errorf("limit must not be negative, got %d", in.Limit)
	}
	sel, err := labels.Parse(in.LabelSelector)
	if err != nil {
		return nil, errors.New("label_selector: " + err.Error())
	}
	now := a.now()
	since, err := parseMemTime(in.Since, now)
	if err != nil {
		return nil, fmt.Errorf("since: %w", err)
	}
	until, err := parseMemTime(in.Until, now)
	if err != nil {
		return nil, fmt.Errorf("until: %w", err)
	}
	if !since.IsZero() && !until.IsZero() && since.After(until) {
		return nil, fmt.Errorf("since (%s) is after until (%s) — that range matches nothing", in.Since, in.Until)
	}

	createdByWorker := strings.TrimSpace(in.CreatedByWorker)
	if createdByWorker == memorySearchSelfSentinel {
		if c.Worker == "" {
			return nil, errors.New(`created_by_worker "self" has no meaning here: this session has no worker identity (it is a plain chat, not a worker run)`)
		}
		createdByWorker = c.Worker
	}

	var queryVec []float32
	if strings.TrimSpace(in.Query) != "" {
		v, err := a.embed.Embed(ctx, in.Query)
		if err == nil {
			queryVec = v
		}
		// READ path degrades: a provider hiccup costs this query its semantic leg, not the answer.
	}

	hits, err := a.store.SearchMemories(ctx, store.MemorySearch{
		Project: c.Project, Selector: sel, Query: in.Query, QueryEmbedding: queryVec, Limit: in.Limit,
		Since: since, Until: until, CreatedByWorker: createdByWorker, LatestPer: in.LatestPer,
	})
	if err != nil {
		return nil, err
	}
	out := make([]memoryHit, len(hits))
	for i, h := range hits {
		out[i] = a.memoryHitOf(c.Project, h)
	}
	return map[string]any{
		"results": out,
		"count":   len(out),
		"note":    "Scores are rank-fusion values with no relevance floor: a low score means nothing good matched, not a weak match worth using.",
	}, nil
}

func (a *app) memoryGet(ctx context.Context, c mcp.Caller, args json.RawMessage) (any, error) {
	var in struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.ID) == "" {
		return nil, errors.New("id is required")
	}
	m, err := a.store.Memory(ctx, c.Project, strings.TrimSpace(in.ID))
	if errors.Is(err, store.ErrNotFound) {
		return nil, fmt.Errorf("no memory with id %q in this project", in.ID)
	}
	if err != nil {
		return nil, err
	}
	return a.memoryRecordOf(m), nil
}

func (a *app) memoryCurrent(ctx context.Context, c mcp.Caller, args json.RawMessage) (any, error) {
	var in struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.Name) == "" {
		return nil, errors.New("name is required")
	}
	m, err := a.store.CurrentMemory(ctx, c.Project, strings.TrimSpace(in.Name))
	if errors.Is(err, store.ErrNotFound) {
		return map[string]bool{"found": false}, nil
	}
	if err != nil {
		return nil, err
	}
	return a.memoryRecordOf(m), nil
}

// listMemories serves GET /api/projects/{project}/memories: the newest memories, for the
// Overview page. limit defaults to 20 (the store's own default) and is capped at 100.
func (a *app) listMemories(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	hits, err := a.store.SearchMemories(r.Context(), store.MemorySearch{Project: project, Limit: limit})
	if err != nil {
		reply(w, nil, err)
		return
	}
	out := make([]memoryHit, len(hits))
	for i, h := range hits {
		out[i] = a.memoryHitOf(project, h)
	}
	reply(w, map[string]any{"memories": out}, nil)
}

// ---------------------------------------------------------------------------
// Time parsing: RFC3339, unix milliseconds, or a relative age ("7d", "24h", "90m", "30s"),
// ported from agent-bob's timearg.go / agentdb.ParseMSTime. Bob's store takes time.Time, not
// milliseconds, so this returns that directly; "" is the zero time (unbounded).
// ---------------------------------------------------------------------------

const memTimeFormsHelp = `an RFC3339 timestamp (2026-07-18T00:00:00Z), unix milliseconds, or a relative age such as 7d, 24h, 90m or 30s`

var relativeAgeRe = regexp.MustCompile(`^(\d+)(s|m|h|d)$`)

func parseMemTime(s string, now time.Time) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, nil
	}
	if m := relativeAgeRe.FindStringSubmatch(s); m != nil {
		n, _ := strconv.ParseInt(m[1], 10, 64)
		var unit time.Duration
		switch m[2] {
		case "s":
			unit = time.Second
		case "m":
			unit = time.Minute
		case "h":
			unit = time.Hour
		case "d":
			unit = 24 * time.Hour
		}
		return now.Add(-time.Duration(n) * unit), nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	if ms, err := strconv.ParseInt(s, 10, 64); err == nil {
		return time.UnixMilli(ms), nil
	}
	return time.Time{}, fmt.Errorf("%q is not %s", s, memTimeFormsHelp)
}
