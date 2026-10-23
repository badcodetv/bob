package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/badcodetv/bob/internal/labels"
	"github.com/badcodetv/bob/internal/mcp"
	"github.com/badcodetv/bob/internal/store"
)

// workerTools returns worker_create, worker_update, worker_list and worker_delete: an agent
// managing its own project's workers. A worker's name is permanent — the Caller's project scopes
// every call, so a worker in another project is neither visible nor reachable. validWorker
// (workers.go, T5) and the store methods (T4) are shared with the HTTP handlers; only the
// argument shape and the tool-error wrapping differ.
func (a *app) workerTools() []mcp.Tool {
	return []mcp.Tool{
		{
			Name:    "worker_create",
			ForKeys: true,
			Description: "Creates a new worker in this project: an agent with its own engine, model, prompt " +
				"and tools that chats can be started with." + workerNote,
			InputSchema: workerCreateSchema,
			Call: func(ctx context.Context, c mcp.Caller, args json.RawMessage) (any, error) {
				in, err := decodeWorkerArgs(args)
				if err != nil {
					return nil, err
				}
				w := in.toStoreWorker(c.Project)
				if err := validWorker(w); err != nil {
					return nil, err
				}
				created, err := a.store.CreateWorker(ctx, w, c.User, in.Why)
				if errors.Is(err, store.ErrConflict) {
					return nil, errors.New("a worker named " + in.Name + " already exists in this project")
				}
				return created, err
			},
		},
		{
			Name:    "worker_update",
			ForKeys: true,
			Description: "Replaces an existing worker's engine, model, effort, tools and prompt. Fields left " +
				"out are cleared, not kept — send every field you want the worker to keep." + workerNote,
			InputSchema: workerUpdateSchema,
			Call: func(ctx context.Context, c mcp.Caller, args json.RawMessage) (any, error) {
				in, err := decodeWorkerArgs(args)
				if err != nil {
					return nil, err
				}
				w := in.toStoreWorker(c.Project)
				if err := validWorker(w); err != nil {
					return nil, err
				}
				updated, err := a.store.UpdateWorker(ctx, w, c.User, in.Why)
				if errors.Is(err, store.ErrNotFound) {
					return nil, errors.New("no worker named " + in.Name + " in this project")
				}
				return updated, err
			},
		},
		{
			Name:    "worker_list",
			ForKeys: true,
			Description: "Lists this project's workers. With name, returns that one worker in full, " +
				"including its prompt. Without name, returns a summary of every worker (no prompt).",
			InputSchema: workerListSchema,
			Call: func(ctx context.Context, c mcp.Caller, args json.RawMessage) (any, error) {
				var in struct {
					Name          string `json:"name"`
					LabelSelector string `json:"label_selector"`
				}
				if len(args) > 0 {
					if err := json.Unmarshal(args, &in); err != nil {
						return nil, err
					}
				}
				if in.Name != "" {
					w, err := a.store.Worker(ctx, c.Project, in.Name)
					if errors.Is(err, store.ErrNotFound) {
						return nil, errors.New("no worker named " + in.Name + " in this project")
					}
					return w, err
				}
				sel, err := labels.Parse(in.LabelSelector)
				if err != nil {
					return nil, errors.New("label_selector: " + err.Error())
				}
				ws, err := a.store.Workers(ctx, c.Project, sel)
				if err != nil {
					return nil, err
				}
				out := make([]workerSummary, len(ws))
				for i, w := range ws {
					out[i] = workerSummary{Name: w.Name, Engine: w.Engine, Model: w.Model, Effort: w.Effort,
						Labels: w.Labels, UpdatedAt: w.UpdatedAt}
				}
				return out, nil
			},
		},
		{
			Name:        "worker_delete",
			ForKeys:     true,
			Description: "Deletes a worker from this project. Its chats stay readable but can no longer be resumed; its schedules are deleted." + workerNote,
			InputSchema: workerDeleteSchema,
			Call: func(ctx context.Context, c mcp.Caller, args json.RawMessage) (any, error) {
				var in struct {
					Name string `json:"name"`
					Why  string `json:"why"`
				}
				if err := json.Unmarshal(args, &in); err != nil {
					return nil, err
				}
				if in.Why == "" {
					return nil, errors.New("why is required")
				}
				err := a.store.DeleteWorker(ctx, c.Project, in.Name, c.User, in.Why)
				if errors.Is(err, store.ErrNotFound) {
					return nil, errors.New("no worker named " + in.Name + " in this project")
				}
				return map[string]bool{"ok": err == nil}, err
			},
		},
	}
}

// workerNote is appended to every worker tool's description: shared ground rules an agent needs
// before it calls any of them.
const workerNote = " Names are permanent: to rename a worker, create a new one and delete the old " +
	"one. why is recorded in the worker's history, so future readers know what changed and why. " +
	"The prompt is the worker's whole job description — Bob's own note and the project's prompt are " +
	"prepended to it automatically, so the prompt itself should not repeat them."

var workerCreateSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"name": {"type": "string", "description": "lower-case letters, digits and -, starting with a letter or digit; permanent"},
		"engine": {"type": "string", "enum": ["claude", "codex"]},
		"model": {"type": "string"},
		"effort": {"type": "string"},
		"tools": {"type": "array", "items": {"type": "string"}, "description": "claude only"},
		"prompt": {"type": "string", "description": "the worker's whole job description"},
		"labels": {"type": "object", "additionalProperties": {"type": "string"}, "description": "key/value labels for selection, e.g. by worker_list's label_selector; keys may not start with bob."},
		"why": {"type": "string"}
	},
	"required": ["name", "engine", "prompt", "why"]
}`)

var workerUpdateSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"name": {"type": "string", "description": "the worker to update; cannot be changed"},
		"engine": {"type": "string", "enum": ["claude", "codex"]},
		"model": {"type": "string"},
		"effort": {"type": "string"},
		"tools": {"type": "array", "items": {"type": "string"}, "description": "claude only"},
		"prompt": {"type": "string", "description": "the worker's whole job description"},
		"labels": {"type": "object", "additionalProperties": {"type": "string"}, "description": "key/value labels for selection, e.g. by worker_list's label_selector; keys may not start with bob."},
		"why": {"type": "string"}
	},
	"required": ["name", "why"]
}`)

var workerListSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"name": {"type": "string", "description": "with a name: that one worker, including its prompt. Without: a summary of every worker."},
		"label_selector": {"type": "string", "description": "e.g. \"kind=hypothesis\", \"env in (dev,staging)\", \"!retired\"; filters the summary list. Ignored when name is set."}
	}
}`)

var workerDeleteSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"name": {"type": "string"},
		"why": {"type": "string"}
	},
	"required": ["name", "why"]
}`)

// workerToolArgs is worker_create's and worker_update's arguments: the shared worker fields plus why.
type workerToolArgs struct {
	Name   string            `json:"name"`
	Engine string            `json:"engine"`
	Model  string            `json:"model"`
	Effort string            `json:"effort"`
	Tools  []string          `json:"tools"`
	Prompt string            `json:"prompt"`
	Labels map[string]string `json:"labels"`
	Why    string            `json:"why"`
}

func decodeWorkerArgs(args json.RawMessage) (workerToolArgs, error) {
	var in workerToolArgs
	err := json.Unmarshal(args, &in)
	return in, err
}

func (in workerToolArgs) toStoreWorker(project string) store.Worker {
	return store.Worker{Project: project, Name: in.Name, Engine: in.Engine, Model: in.Model,
		Effort: in.Effort, Tools: in.Tools, Prompt: in.Prompt, Labels: in.Labels}
}

// workerSummary is one row of worker_list's answer when it is not asked for a single worker: the
// prompt is left out, since it can be long and is rarely what a listing call needs.
type workerSummary struct {
	Name      string            `json:"name"`
	Engine    string            `json:"engine"`
	Model     string            `json:"model"`
	Effort    string            `json:"effort"`
	Labels    map[string]string `json:"labels"`
	UpdatedAt time.Time         `json:"updated_at"`
}

// projectPromptTools returns project_prompt_get and project_prompt_set: the prompt every worker
// and plain chat of the project shares, with its history, as the web app's Project prompt page.
func (a *app) projectPromptTools() []mcp.Tool {
	return []mcp.Tool{
		{
			Name:    "project_prompt_get",
			ForKeys: true,
			Description: "Returns this project's prompt (shared by all its workers and plain chats, after Bob's " +
				"environment note and before a worker's own prompt) and its recent history: who changed it, when and why.",
			Call: func(ctx context.Context, c mcp.Caller, _ json.RawMessage) (any, error) {
				p, err := a.store.Project(ctx, c.Project)
				if err != nil {
					return nil, err
				}
				versions, err := a.store.ProjectPromptVersions(ctx, c.Project)
				if err != nil {
					return nil, err
				}
				if len(versions) > 10 {
					versions = versions[:10]
				}
				return map[string]any{"prompt": p.Prompt, "versions": versions}, nil
			},
		},
		{
			Name:    "project_prompt_set",
			ForKeys: true,
			Description: "Replaces this project's prompt with prompt, in full: read it with project_prompt_get " +
				"first and send the whole new text. It takes effect on every chat's next message. why is " +
				"recorded in the prompt's history.",
			InputSchema: projectPromptSetSchema,
			Call: func(ctx context.Context, c mcp.Caller, args json.RawMessage) (any, error) {
				var in struct {
					Prompt *string `json:"prompt"`
					Why    string  `json:"why"`
				}
				if err := json.Unmarshal(args, &in); err != nil {
					return nil, err
				}
				if in.Prompt == nil {
					return nil, errors.New("prompt is required: the whole new text (\"\" to clear it)")
				}
				if strings.TrimSpace(in.Why) == "" {
					return nil, errors.New("why is required")
				}
				if err := a.store.SetProjectPrompt(ctx, c.Project, *in.Prompt, c.User, in.Why); err != nil {
					return nil, err
				}
				return map[string]string{"prompt": *in.Prompt}, nil
			},
		},
	}
}

var projectPromptSetSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "prompt": {"type": "string", "description": "The whole new project prompt."},
    "why": {"type": "string", "description": "Why it changed; kept in its history."}
  },
  "required": ["prompt", "why"]
}`)
