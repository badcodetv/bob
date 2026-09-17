package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"sync"

	"github.com/badcodetv/bob/internal/broker"
	"github.com/badcodetv/bob/internal/engines"
	"github.com/badcodetv/bob/internal/runtime"
	"github.com/badcodetv/bob/internal/store"
)

type app struct {
	store   *store.Store
	broker  *broker.Broker
	runtime *runtime.Manager

	mu    sync.Mutex
	turns map[string]context.CancelFunc // session id → running turn
}

func (a *app) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects", a.listProjects)
	mux.HandleFunc("POST /api/projects", a.createProject)
	mux.HandleFunc("POST /api/projects/{project}/restart", a.restartProject)
	mux.HandleFunc("GET /api/projects/{project}/workers", a.listWorkers)
	mux.HandleFunc("GET /api/projects/{project}/sessions", a.listSessions)
	mux.HandleFunc("POST /api/projects/{project}/sessions", a.createSession)
	mux.HandleFunc("GET /api/sessions/{id}", a.getSession)
	mux.HandleFunc("GET /api/sessions/{id}/events", a.listEvents)
	mux.HandleFunc("GET /api/sessions/{id}/stream", a.streamEvents)
	mux.HandleFunc("POST /api/sessions/{id}/messages", a.sendMessage)
	mux.HandleFunc("POST /api/sessions/{id}/interrupt", a.interrupt)
	return mux
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

func (a *app) restartProject(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("project")
	if _, err := a.store.Project(r.Context(), name); err != nil {
		reply(w, nil, err)
		return
	}
	reply(w, map[string]bool{"ok": true}, a.runtime.Recreate(r.Context(), name))
}

func (a *app) listWorkers(w http.ResponseWriter, r *http.Request) {
	workers, err := a.workers(r.Context(), r.PathValue("project"))
	reply(w, map[string]any{"workers": workers}, err)
}

func (a *app) workers(ctx context.Context, project string) ([]runtime.Worker, error) {
	p, err := a.store.Project(ctx, project)
	if err != nil {
		return nil, err
	}
	base, err := a.runtime.Ensure(ctx, p)
	if err != nil {
		return nil, err
	}
	return runtime.Workers(ctx, base)
}

func (a *app) listSessions(w http.ResponseWriter, r *http.Request) {
	ss, err := a.store.Sessions(r.Context(), r.PathValue("project"))
	reply(w, map[string]any{"sessions": ss}, err)
}

func (a *app) createSession(w http.ResponseWriter, r *http.Request) {
	var body struct{ Worker string }
	if !decode(w, r, &body) {
		return
	}
	project := r.PathValue("project")
	workers, err := a.workers(r.Context(), project)
	if err != nil {
		reply(w, nil, err)
		return
	}
	for _, wk := range workers {
		if wk.Name == body.Worker {
			s, err := a.store.CreateSession(r.Context(), project, wk.Name, wk.Engine)
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
	err = runtime.RunTurn(ctx, base, runtime.TurnRequest{SessionID: sess.ID, Worker: sess.Worker, Text: text, Resume: sess.HarnessSessionID},
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
