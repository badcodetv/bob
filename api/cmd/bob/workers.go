package main

import (
	"errors"
	"net/http"
	"regexp"

	"github.com/badcodetv/bob/internal/store"
)

var workerName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,40}$`)

// claudeEfforts and codexEfforts are the effort values each engine accepts (Interfaces →
// Database, and the plan's Architecture Decisions); "" is always allowed (harness default).
var (
	claudeEfforts = map[string]bool{"low": true, "medium": true, "high": true, "xhigh": true, "max": true}
	codexEfforts  = map[string]bool{"minimal": true, "low": true, "medium": true, "high": true, "xhigh": true}
)

// validWorker checks a worker's shared fields, regardless of whether it is being created or
// updated: name, engine, effort (which depends on the engine), model, and that tools are only
// set for claude.
func validWorker(w store.Worker) error {
	switch {
	case !workerName.MatchString(w.Name):
		return errors.New("name: lower-case letters, digits and -, starting with a letter or digit")
	case w.Engine != "claude" && w.Engine != "codex":
		return errors.New("engine must be claude or codex")
	case !modelName.MatchString(w.Model):
		return errors.New("model: letters, digits and . _ : / [ ] - only")
	case w.Effort != "" && !effortsFor(w.Engine)[w.Effort]:
		return errors.New("effort: " + effortListFor(w.Engine))
	case len(w.Tools) > 0 && w.Engine != "claude":
		return errors.New("tools are only for claude workers")
	}
	return nil
}

func effortsFor(engine string) map[string]bool {
	if engine == "codex" {
		return codexEfforts
	}
	return claudeEfforts
}

func effortListFor(engine string) string {
	if engine == "codex" {
		return "minimal, low, medium, high or xhigh"
	}
	return "low, medium, high, xhigh or max"
}

// validSettings checks the model and effort a session (or its override) runs with, for the
// engine it runs on.
func validSettings(w http.ResponseWriter, engine, model, effort string) bool {
	switch {
	case !modelName.MatchString(model):
		http.Error(w, "model: letters, digits and . _ : / [ ] - only", http.StatusBadRequest)
	case effort != "" && !effortsFor(engine)[effort]:
		http.Error(w, "effort: "+effortListFor(engine), http.StatusBadRequest)
	default:
		return true
	}
	return false
}

func (a *app) listWorkers(w http.ResponseWriter, r *http.Request) {
	ws, err := a.store.Workers(r.Context(), r.PathValue("project"))
	reply(w, map[string]any{"workers": ws}, err)
}

type workerBody struct {
	Name   string   `json:"name"`
	Engine string   `json:"engine"`
	Model  string   `json:"model"`
	Effort string   `json:"effort"`
	Tools  []string `json:"tools"`
	Prompt string   `json:"prompt"`
	Why    string   `json:"why"`
}

func (a *app) createWorker(w http.ResponseWriter, r *http.Request) {
	var body workerBody
	if !decode(w, r, &body) {
		return
	}
	x := store.Worker{Project: r.PathValue("project"), Name: body.Name, Engine: body.Engine,
		Model: body.Model, Effort: body.Effort, Tools: body.Tools, Prompt: body.Prompt}
	if err := validWorker(x); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	created, err := a.store.CreateWorker(r.Context(), x, a.auth.Email(r), body.Why)
	reply(w, created, err)
}

func (a *app) updateWorker(w http.ResponseWriter, r *http.Request) {
	var body workerBody
	if !decode(w, r, &body) {
		return
	}
	project, name := r.PathValue("project"), r.PathValue("worker")
	x := store.Worker{Project: project, Name: name, Engine: body.Engine,
		Model: body.Model, Effort: body.Effort, Tools: body.Tools, Prompt: body.Prompt}
	if err := validWorker(x); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	updated, err := a.store.UpdateWorker(r.Context(), x, a.auth.Email(r), body.Why)
	reply(w, updated, err)
}

func (a *app) deleteWorker(w http.ResponseWriter, r *http.Request) {
	project, name := r.PathValue("project"), r.PathValue("worker")
	why := r.URL.Query().Get("why")
	err := a.store.DeleteWorker(r.Context(), project, name, a.auth.Email(r), why)
	reply(w, map[string]bool{"ok": true}, err)
}

func (a *app) workerVersions(w http.ResponseWriter, r *http.Request) {
	wk, err := a.store.Worker(r.Context(), r.PathValue("project"), r.PathValue("worker"))
	if err != nil {
		reply(w, nil, err)
		return
	}
	versions, err := a.store.WorkerVersions(r.Context(), wk.ID)
	reply(w, map[string]any{"versions": versions}, err)
}

func (a *app) getProjectPrompt(w http.ResponseWriter, r *http.Request) {
	p, err := a.store.Project(r.Context(), r.PathValue("project"))
	if err != nil {
		reply(w, nil, err)
		return
	}
	versions, err := a.store.ProjectPromptVersions(r.Context(), r.PathValue("project"))
	if err != nil {
		reply(w, nil, err)
		return
	}
	reply(w, map[string]any{"prompt": p.Prompt, "versions": versions}, nil)
}

func (a *app) setProjectPrompt(w http.ResponseWriter, r *http.Request) {
	var body struct{ Prompt, Why string }
	if !decode(w, r, &body) {
		return
	}
	project := r.PathValue("project")
	err := a.store.SetProjectPrompt(r.Context(), project, body.Prompt, a.auth.Email(r), body.Why)
	if err != nil {
		reply(w, nil, err)
		return
	}
	reply(w, map[string]string{"prompt": body.Prompt}, nil)
}

// findWorkerID resolves a schedule body's worker name to its id: 400 if the project has no
// worker by that name.
func (a *app) findWorkerID(w http.ResponseWriter, r *http.Request, project, name string) (id string, ok bool) {
	wk, err := a.store.Worker(r.Context(), project, name)
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "unknown worker: "+name, http.StatusBadRequest)
		return "", false
	}
	if err != nil {
		reply(w, nil, err)
		return "", false
	}
	return wk.ID, true
}
