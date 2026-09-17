package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"

	"github.com/badcodetv/bob/internal/auth"
	"github.com/badcodetv/bob/internal/broker"
	"github.com/badcodetv/bob/internal/engines"
	"github.com/badcodetv/bob/internal/runtime"
	"github.com/badcodetv/bob/internal/store"
)

type app struct {
	auth    *auth.Auth
	webDir  string
	store   *store.Store
	broker  *broker.Broker
	runtime *runtime.Manager

	mu    sync.Mutex
	turns map[string]context.CancelFunc // session id → running turn
}

func (a *app) routes() http.Handler {
	api := http.NewServeMux()
	api.HandleFunc("GET /api/projects", a.listProjects)
	api.HandleFunc("POST /api/projects", a.createProject)
	api.HandleFunc("GET /api/projects/{project}", a.getProject)
	api.HandleFunc("PATCH /api/projects/{project}", a.updateProject)
	api.HandleFunc("DELETE /api/projects/{project}", a.deleteProject)
	api.HandleFunc("POST /api/projects/{project}/restart", a.restartProject)
	api.HandleFunc("POST /api/projects/{project}/sync", a.syncProject)
	api.HandleFunc("GET /api/projects/{project}/workers", a.listWorkers)
	api.HandleFunc("GET /api/projects/{project}/sessions", a.listSessions)
	api.HandleFunc("POST /api/projects/{project}/sessions", a.createSession)
	api.HandleFunc("GET /api/sessions/{id}", a.getSession)
	api.HandleFunc("PATCH /api/sessions/{id}", a.updateSession)
	api.HandleFunc("DELETE /api/sessions/{id}", a.deleteSession)
	api.HandleFunc("GET /api/sessions/{id}/events", a.listEvents)
	api.HandleFunc("GET /api/sessions/{id}/stream", a.streamEvents)
	api.HandleFunc("POST /api/sessions/{id}/messages", a.sendMessage)
	api.HandleFunc("POST /api/sessions/{id}/interrupt", a.interrupt)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/config", a.config)
	mux.HandleFunc("POST /api/login", a.login)
	mux.HandleFunc("POST /api/logout", a.logout)
	mux.Handle("/api/", a.requireLogin(api))
	if a.webDir != "" {
		mux.Handle("/", spa(a.webDir))
	}
	return mux
}

// config tells the web app how to sign in, and who is signed in.
func (a *app) config(w http.ResponseWriter, r *http.Request) {
	reply(w, map[string]string{"google_client_id": a.auth.ClientID, "email": a.auth.Email(r)}, nil)
}

func (a *app) login(w http.ResponseWriter, r *http.Request) {
	var body struct{ Credential string }
	if !decode(w, r, &body) {
		return
	}
	email, err := a.auth.VerifyGoogle(r.Context(), body.Credential)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	a.auth.SetSession(w, email)
	reply(w, map[string]string{"email": email}, nil)
}

func (a *app) logout(w http.ResponseWriter, r *http.Request) {
	a.auth.ClearSession(w)
	reply(w, map[string]bool{"ok": true}, nil)
}

func (a *app) requireLogin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.auth.Email(r) == "" {
			http.Error(w, "sign in first", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// spa serves the built web app, falling back to index.html for client-side routes.
func spa(dir string) http.Handler {
	files := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := os.Stat(filepath.Join(dir, filepath.Clean("/"+r.URL.Path))); err != nil {
			http.ServeFile(w, r, filepath.Join(dir, "index.html"))
			return
		}
		files.ServeHTTP(w, r)
	})
}

func (a *app) listProjects(w http.ResponseWriter, r *http.Request) {
	ps, err := a.store.Projects(r.Context())
	reply(w, map[string]any{"projects": ps}, err)
}

func (a *app) createProject(w http.ResponseWriter, r *http.Request) {
	var p store.Project
	if !decode(w, r, &p) {
		return
	}
	p, err := a.store.CreateProject(r.Context(), p)
	reply(w, p, err)
}

func (a *app) getProject(w http.ResponseWriter, r *http.Request) {
	p, err := a.store.Project(r.Context(), r.PathValue("project"))
	reply(w, p, err)
}

// updateProject saves new repository settings and recreates the project's container, keeping
// its volume, so the next request boots with them and syncs git.
func (a *app) updateProject(w http.ResponseWriter, r *http.Request) {
	var body store.Project
	if !decode(w, r, &body) {
		return
	}
	body.Name = r.PathValue("project")
	p, err := a.store.UpdateProject(r.Context(), body)
	if err != nil {
		reply(w, nil, err)
		return
	}
	reply(w, p, a.runtime.Recreate(r.Context(), p.Name))
}

// deleteProject stops the project's running turns, removes its container and volume, then
// deletes it with its sessions and their events.
func (a *app) deleteProject(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("project")
	sessions, err := a.store.Sessions(r.Context(), name)
	if err != nil {
		reply(w, nil, err)
		return
	}
	if _, err := a.store.Project(r.Context(), name); err != nil {
		reply(w, nil, err)
		return
	}
	for _, s := range sessions {
		a.endTurn(s.ID)
	}
	if err := a.runtime.Destroy(r.Context(), name); err != nil {
		reply(w, nil, fmt.Errorf("removing the project's container and volume: %w", err))
		return
	}
	reply(w, map[string]bool{"ok": true}, a.store.DeleteProject(r.Context(), name))
}

func (a *app) restartProject(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("project")
	if _, err := a.store.Project(r.Context(), name); err != nil {
		reply(w, nil, err)
		return
	}
	reply(w, map[string]bool{"ok": true}, a.runtime.Recreate(r.Context(), name))
}

func (a *app) listWorkers(w http.ResponseWriter, r *http.Request) {
	list, err := a.workers(r.Context(), r.PathValue("project"), false)
	reply(w, list, err)
}

func (a *app) syncProject(w http.ResponseWriter, r *http.Request) {
	list, err := a.workers(r.Context(), r.PathValue("project"), true)
	reply(w, list, err)
}

func (a *app) workers(ctx context.Context, project string, sync bool) (runtime.WorkerList, error) {
	p, err := a.store.Project(ctx, project)
	if err != nil {
		return runtime.WorkerList{}, err
	}
	base, err := a.runtime.Ensure(ctx, p)
	if err != nil {
		return runtime.WorkerList{}, err
	}
	return runtime.Workers(ctx, base, sync)
}

func (a *app) listSessions(w http.ResponseWriter, r *http.Request) {
	ss, err := a.store.Sessions(r.Context(), r.PathValue("project"))
	reply(w, map[string]any{"sessions": ss}, err)
}

func (a *app) createSession(w http.ResponseWriter, r *http.Request) {
	var body struct{ Worker, Model, Effort string }
	if !decode(w, r, &body) || !validSettings(w, body.Model, body.Effort) {
		return
	}
	project := r.PathValue("project")
	list, err := a.workers(r.Context(), project, false)
	if err != nil {
		reply(w, nil, err)
		return
	}
	for _, wk := range list.Workers {
		if wk.Name == body.Worker {
			s, err := a.store.CreateSession(r.Context(), project, wk.Name, wk.Engine, body.Model, body.Effort)
			reply(w, s, err)
			return
		}
	}
	http.Error(w, fmt.Sprintf("project %s has no worker named %q", project, body.Worker), http.StatusBadRequest)
}

func (a *app) getSession(w http.ResponseWriter, r *http.Request) {
	s, err := a.store.Session(r.Context(), r.PathValue("id"))
	reply(w, s, err)
}

func (a *app) updateSession(w http.ResponseWriter, r *http.Request) {
	var body struct{ Model, Effort string }
	if !decode(w, r, &body) || !validSettings(w, body.Model, body.Effort) {
		return
	}
	s, err := a.store.SetSessionSettings(r.Context(), r.PathValue("id"), body.Model, body.Effort)
	reply(w, s, err)
}

// deleteSession stops any running turn, removes the session's worktree in its project
// container, then deletes the session and its events. A worktree that cannot be removed
// (container gone, say) does not keep the session alive.
func (a *app) deleteSession(w http.ResponseWriter, r *http.Request) {
	sess, err := a.store.Session(r.Context(), r.PathValue("id"))
	if err != nil {
		reply(w, nil, err)
		return
	}
	a.endTurn(sess.ID)
	if p, err := a.store.Project(r.Context(), sess.Project); err == nil {
		if base, err := a.runtime.Ensure(r.Context(), p); err == nil {
			if err := runtime.RemoveSession(r.Context(), base, sess.ID); err != nil {
				log.Printf("session %s: removing worktree: %v", sess.ID, err)
			}
		}
	}
	reply(w, map[string]bool{"ok": true}, a.store.DeleteSession(r.Context(), sess.ID))
}

var modelName = regexp.MustCompile(`^[A-Za-z0-9._:/\[\]-]{0,100}$`)

func validSettings(w http.ResponseWriter, model, effort string) bool {
	switch {
	case !modelName.MatchString(model):
		http.Error(w, "model: letters, digits and . _ : / [ ] - only", http.StatusBadRequest)
	case effort != "" && effort != "low" && effort != "medium" && effort != "high" && effort != "xhigh" && effort != "max":
		http.Error(w, "effort must be low, medium, high, xhigh or max", http.StatusBadRequest)
	default:
		return true
	}
	return false
}

func (a *app) listEvents(w http.ResponseWriter, r *http.Request) {
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	es, err := a.store.Events(r.Context(), r.PathValue("id"), after)
	reply(w, map[string]any{"events": es}, err)
}

// streamEvents is SSE: stored events after ?after= (or Last-Event-ID), then live ones.
// Stored events carry their id; ephemeral ones (token deltas) carry none.
func (a *app) streamEvents(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := a.store.Session(r.Context(), id); err != nil {
		reply(w, nil, err)
		return
	}
	cursor := r.Header.Get("Last-Event-ID")
	if cursor == "" {
		cursor = r.URL.Query().Get("after")
	}
	after, _ := strconv.ParseInt(cursor, 10, 64)

	live, unsubscribe := a.broker.Subscribe(id) // before the replay, so nothing falls between
	defer unsubscribe()
	stored, err := a.store.Events(r.Context(), id, after)
	if err != nil {
		reply(w, nil, err)
		return
	}
	flusher, _ := w.(http.Flusher)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	send := func(e store.Event) {
		b, _ := json.Marshal(e)
		if e.ID > 0 {
			fmt.Fprintf(w, "id: %d\n", e.ID)
		}
		fmt.Fprintf(w, "data: %s\n\n", b)
		if flusher != nil {
			flusher.Flush()
		}
	}
	for _, e := range stored {
		send(e)
		after = e.ID
	}
	if flusher != nil {
		flusher.Flush()
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case e := <-live:
			if e.ID > 0 && e.ID <= after {
				continue
			}
			send(e)
			if e.ID > 0 {
				after = e.ID
			}
		}
	}
}

func (a *app) sendMessage(w http.ResponseWriter, r *http.Request) {
	var body struct{ Text string }
	if !decode(w, r, &body) {
		return
	}
	if body.Text == "" {
		http.Error(w, "text is required", http.StatusBadRequest)
		return
	}
	sess, err := a.store.Session(r.Context(), r.PathValue("id"))
	if err != nil {
		reply(w, nil, err)
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.mu.Lock()
	if _, busy := a.turns[sess.ID]; busy {
		a.mu.Unlock()
		cancel()
		http.Error(w, "a turn is already running in this session", http.StatusConflict)
		return
	}
	a.turns[sess.ID] = cancel
	a.mu.Unlock()

	payload, _ := json.Marshal(map[string]string{"text": body.Text})
	userEvent, err := a.record(r.Context(), store.Event{SessionID: sess.ID, Engine: sess.Engine, Kind: "bob.user_message", Payload: payload})
	if err != nil {
		a.endTurn(sess.ID)
		reply(w, nil, err)
		return
	}
	go a.runTurn(ctx, sess, body.Text)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	reply(w, userEvent, nil)
}

func (a *app) interrupt(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	cancel, ok := a.turns[r.PathValue("id")]
	a.mu.Unlock()
	if ok {
		cancel()
	}
	reply(w, map[string]bool{"interrupted": ok}, nil)
}

func (a *app) endTurn(sessionID string) {
	a.mu.Lock()
	if cancel, ok := a.turns[sessionID]; ok {
		cancel()
		delete(a.turns, sessionID)
	}
	a.mu.Unlock()
}

func (a *app) runTurn(ctx context.Context, sess store.Session, text string) {
	defer a.endTurn(sess.ID)
	fail := func(err error) {
		log.Printf("session %s: turn failed: %v", sess.ID, err)
		payload, _ := json.Marshal(map[string]string{"error": err.Error()})
		_, _ = a.record(context.Background(), store.Event{SessionID: sess.ID, Engine: sess.Engine, Kind: "bob.turn_failed", Payload: payload})
	}
	p, err := a.store.Project(ctx, sess.Project)
	if err != nil {
		fail(err)
		return
	}
	base, err := a.runtime.Ensure(ctx, p)
	if err != nil {
		fail(err)
		return
	}
	var done *runtime.TurnLine
	err = runtime.RunTurn(ctx, base, runtime.TurnRequest{SessionID: sess.ID, Worker: sess.Worker, Text: text, Resume: sess.HarnessSessionID, Model: sess.Model, Effort: sess.Effort},
		func(line runtime.TurnLine) error {
			if line.Done {
				done = &line
				return nil
			}
			kind, ephemeral := engines.Classify(sess.Engine, line.Event)
			e := store.Event{SessionID: sess.ID, Engine: sess.Engine, Kind: kind, Payload: line.Event}
			if ephemeral {
				a.broker.Publish(e)
				return nil
			}
			_, err := a.record(context.Background(), e)
			return err
		})
	switch {
	case err != nil:
		fail(err)
	case done == nil:
		fail(errors.New("runtime closed the turn without finishing it"))
	case done.Error != "":
		fail(errors.New(done.Error))
	}
	if done != nil && done.HarnessSessionID != "" && done.HarnessSessionID != sess.HarnessSessionID {
		if err := a.store.SetHarnessSessionID(context.Background(), sess.ID, done.HarnessSessionID); err != nil {
			log.Printf("session %s: saving harness session id: %v", sess.ID, err)
		}
	}
	if err == nil && done != nil && done.Error == "" {
		_, _ = a.record(context.Background(), store.Event{SessionID: sess.ID, Engine: sess.Engine, Kind: "bob.turn_done", Payload: json.RawMessage(`{}`)})
	}
}

// record stores an event and publishes it to live watchers.
func (a *app) record(ctx context.Context, e store.Event) (store.Event, error) {
	e, err := a.store.AppendEvent(ctx, e)
	if err == nil {
		a.broker.Publish(e)
	}
	return e, err
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(v); err != nil {
		http.Error(w, "bad JSON: "+err.Error(), http.StatusBadRequest)
		return false
	}
	return true
}

func reply(w http.ResponseWriter, v any, err error) {
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/json")
	}
	_ = json.NewEncoder(w).Encode(v)
}
