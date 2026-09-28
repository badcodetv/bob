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
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/badcodetv/bob/internal/access"
	"github.com/badcodetv/bob/internal/auth"
	"github.com/badcodetv/bob/internal/broker"
	"github.com/badcodetv/bob/internal/engines"
	"github.com/badcodetv/bob/internal/runtime"
	"github.com/badcodetv/bob/internal/store"
)

// containers finds a project's runtime server (runtime.Manager; faked in tests). Bob does not
// start containers: each project's is declared in the deploy's compose file.
type containers interface {
	Ensure(ctx context.Context, p store.Project) (string, error)
}

type app struct {
	auth   *auth.Auth
	access access.Map
	// projectOf resolves the project a scoped route's session or schedule belongs to ("" if none).
	projectOf func(ctx context.Context, kind, id string) string
	webDir    string
	publicURL string // BOB_PUBLIC_URL: where people reach Bob, for links to chats
	store     *store.Store
	broker    *broker.Broker
	runtime   containers
	now       func() time.Time

	mu        sync.Mutex
	turns     map[string]context.CancelFunc // session id → running turn
	scheduled sync.WaitGroup                // scheduled runs in progress
	active    map[string]int                // project → turns and scheduled runs in progress
}

// hold marks work in progress in a project, so a deploy can wait for it instead of cutting it
// off (GET /api/busy). Call the returned func exactly once when the work ends.
func (a *app) hold(project string) (release func()) {
	a.mu.Lock()
	if a.active == nil {
		a.active = map[string]int{}
	}
	a.active[project]++
	a.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			a.mu.Lock()
			a.active[project]--
			if a.active[project] == 0 {
				delete(a.active, project)
			}
			a.mu.Unlock()
		})
	}
}

// policy is who may call a route. Routes naming a project, session or schedule are scoped to
// that project: someone who may not use it gets 404, so project names don't leak.
type policy int

const (
	signedIn policy = iota // anyone signed in
	member                 // may use the project: chat, view files, run schedules
	admin                  // "*" in the project map: create, change and delete projects and schedules
)

func (a *app) routes() http.Handler { return a.mux(nil) }

// mux builds the router. Tests pass stub to replace every handler and check only the guards.
func (a *app) mux(stub http.HandlerFunc) http.Handler {
	api := http.NewServeMux()
	handle := func(pattern string, p policy, h http.HandlerFunc) {
		if stub != nil {
			h = stub
		}
		api.Handle(pattern, a.guard(p, h))
	}
	handle("GET /api/projects", signedIn, a.listProjects)
	handle("GET /api/projects/{project}", member, a.getProject)
	handle("GET /api/projects/{project}/sessions", member, a.listSessions)
	handle("POST /api/projects/{project}/sessions", member, a.createSession)
	handle("GET /api/projects/{project}/workers", member, a.listWorkers)
	handle("POST /api/projects/{project}/workers", member, a.createWorker)
	handle("PATCH /api/projects/{project}/workers/{worker}", member, a.updateWorker)
	handle("DELETE /api/projects/{project}/workers/{worker}", member, a.deleteWorker)
	handle("GET /api/projects/{project}/workers/{worker}/versions", member, a.workerVersions)
	handle("GET /api/projects/{project}/prompt", member, a.getProjectPrompt)
	handle("PUT /api/projects/{project}/prompt", member, a.setProjectPrompt)
	handle("GET /api/sessions/{session}", member, a.getSession)
	handle("PATCH /api/sessions/{session}", member, a.updateSession)
	handle("DELETE /api/sessions/{session}", member, a.deleteSession)
	handle("GET /api/sessions/{session}/events", member, a.listEvents)
	handle("GET /api/sessions/{session}/stream", member, a.streamEvents)
	handle("POST /api/sessions/{session}/messages", member, a.sendMessage)
	handle("POST /api/sessions/{session}/interrupt", member, a.interrupt)
	handle("GET /api/projects/{project}/files", member, a.getFile)
	handle("GET /api/projects/{project}/files/{path...}", member, a.getFile)
	handle("POST /api/projects/{project}/view", member, a.viewLink)
	handle("GET /api/busy", admin, a.busy)
	handle("GET /api/projects/{project}/schedules", member, a.listSchedules)
	handle("POST /api/projects/{project}/schedules", admin, a.createSchedule)
	handle("PATCH /api/schedules/{schedule}", admin, a.updateSchedule)
	handle("DELETE /api/schedules/{schedule}", admin, a.deleteSchedule)
	handle("POST /api/schedules/{schedule}/run", member, a.runScheduleNow)
	handle("GET /api/schedules/{schedule}/runs", member, a.listRuns)
	handle("GET /api/settings", signedIn, a.getSettings)
	handle("PATCH /api/settings", admin, a.updateSettings)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", a.healthz)
	mux.HandleFunc("GET /api/config", a.config)
	mux.HandleFunc("POST /api/login", a.login)
	mux.HandleFunc("POST /api/logout", a.logout)
	mux.HandleFunc("GET /api/view/{token}", a.viewFile)
	mux.HandleFunc("GET /api/view/{token}/{path...}", a.viewFile)
	mux.Handle("/api/", a.requireLogin(api))
	if a.webDir != "" {
		mux.Handle("/", spa(a.webDir))
	}
	return mux
}

// guard enforces a route's policy against the project map.
func (a *app) guard(p policy, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		email := a.auth.Email(r)
		if p == signedIn {
			next.ServeHTTP(w, r)
			return
		}
		project, scoped := r.PathValue("project"), true
		switch {
		case project != "":
		case r.PathValue("session") != "":
			project = a.projectOf(r.Context(), "session", r.PathValue("session"))
		case r.PathValue("schedule") != "":
			project = a.projectOf(r.Context(), "schedule", r.PathValue("schedule"))
		default:
			scoped = false
		}
		if scoped && (project == "" || !a.access.Member(email, project)) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if p == admin && !a.access.Admin(email) {
			http.Error(w, "only an admin can do this", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// storeProjectOf is the project a session or schedule belongs to, or "" when there is none.
func (a *app) storeProjectOf(ctx context.Context, kind, id string) string {
	switch kind {
	case "session":
		if s, err := a.store.Session(ctx, id); err == nil {
			return s.Project
		}
	case "schedule":
		if s, err := a.store.Schedule(ctx, id); err == nil {
			return s.Project
		}
	}
	return ""
}

// config tells the web app how to sign in, and who is signed in.
func (a *app) config(w http.ResponseWriter, r *http.Request) {
	email := a.auth.Email(r)
	reply(w, map[string]any{"google_client_id": a.auth.ClientID, "email": email, "admin": email != "" && a.access.Admin(email)}, nil)
}

func (a *app) login(w http.ResponseWriter, r *http.Request) {
	var body struct{ Credential string }
	if !decode(w, r, &body) {
		return
	}
	u, err := a.auth.VerifyGoogle(r.Context(), body.Credential)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	a.auth.SetSession(w, u)
	reply(w, u, nil)
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
// healthz is for the box's healthcheck and monitoring: 200 when the database answers. No auth, no detail.
func (a *app) healthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if err := a.store.Ping(ctx); err != nil {
		http.Error(w, "database unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Write([]byte("ok\n"))
}

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
	email, visible := a.auth.Email(r), []store.Project{}
	for _, p := range ps {
		if a.access.Member(email, p.Name) {
			visible = append(visible, p)
		}
	}
	reply(w, map[string]any{"projects": visible}, err)
}

func (a *app) getProject(w http.ResponseWriter, r *http.Request) {
	p, err := a.store.Project(r.Context(), r.PathValue("project"))
	reply(w, p, err)
}

// busy names the projects with work in progress — a turn or a scheduled run. A deploy
// script waits on it, because restarting a project's container cuts off whatever it is doing.
func (a *app) busy(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	projects := make([]string, 0, len(a.active))
	for project := range a.active {
		projects = append(projects, project)
	}
	a.mu.Unlock()
	sort.Strings(projects)
	reply(w, map[string]any{"busy": projects}, nil)
}

func (a *app) listSessions(w http.ResponseWriter, r *http.Request) {
	ss, err := a.store.Sessions(r.Context(), r.PathValue("project"))
	reply(w, map[string]any{"sessions": ss}, err)
}

// createSession starts a chat. With a worker name, it looks the worker up (400 if unknown) and
// takes its engine; with worker: "" it is a plain chat and the engine is required.
func (a *app) createSession(w http.ResponseWriter, r *http.Request) {
	var body struct{ Worker, Engine, Model, Effort string }
	if !decode(w, r, &body) {
		return
	}
	project := r.PathValue("project")
	if body.Worker == "" {
		if body.Engine != "claude" && body.Engine != "codex" {
			http.Error(w, "engine must be claude or codex", http.StatusBadRequest)
			return
		}
		if !validSettings(w, body.Engine, body.Model, body.Effort) {
			return
		}
		s, err := a.store.CreateSession(r.Context(), project, nil, "", body.Engine, body.Model, body.Effort)
		reply(w, s, err)
		return
	}
	wk, err := a.store.Worker(r.Context(), project, body.Worker)
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, fmt.Sprintf("project %s has no worker named %q", project, body.Worker), http.StatusBadRequest)
		return
	}
	if err != nil {
		reply(w, nil, err)
		return
	}
	if !validSettings(w, wk.Engine, body.Model, body.Effort) {
		return
	}
	s, err := a.store.CreateSession(r.Context(), project, &wk.ID, wk.Name, wk.Engine, body.Model, body.Effort)
	reply(w, s, err)
}

func (a *app) getSession(w http.ResponseWriter, r *http.Request) {
	s, err := a.store.Session(r.Context(), r.PathValue("session"))
	reply(w, s, err)
}

func (a *app) updateSession(w http.ResponseWriter, r *http.Request) {
	var body struct{ Model, Effort string }
	if !decode(w, r, &body) {
		return
	}
	sess, err := a.store.Session(r.Context(), r.PathValue("session"))
	if err != nil {
		reply(w, nil, err)
		return
	}
	if !validSettings(w, sess.Engine, body.Model, body.Effort) {
		return
	}
	s, err := a.store.SetSessionSettings(r.Context(), sess.ID, body.Model, body.Effort)
	reply(w, s, err)
}

// deleteSession stops any running turn, then deletes the session and its events. The project
// container keeps nothing per session: every chat works in the one shared folder.
func (a *app) deleteSession(w http.ResponseWriter, r *http.Request) {
	sess, err := a.store.Session(r.Context(), r.PathValue("session"))
	if err != nil {
		reply(w, nil, err)
		return
	}
	reply(w, map[string]bool{"ok": true}, a.removeSession(r.Context(), sess))
}

func (a *app) removeSession(ctx context.Context, sess store.Session) error {
	a.endTurn(sess.ID)
	return a.store.DeleteSession(ctx, sess.ID)
}

// modelName and validSettings are in workers.go, shared with worker validation.
var modelName = regexp.MustCompile(`^[A-Za-z0-9._:/\[\]-]{0,100}$`)

func (a *app) listEvents(w http.ResponseWriter, r *http.Request) {
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	es, err := a.store.Events(r.Context(), r.PathValue("session"), after)
	reply(w, map[string]any{"events": es}, err)
}

// streamEvents is SSE: stored events after ?after= (or Last-Event-ID), then live ones.
// Stored events carry their id; ephemeral ones (token deltas) carry none.
func (a *app) streamEvents(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("session")
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
	sess, err := a.store.Session(r.Context(), r.PathValue("session"))
	if err != nil {
		reply(w, nil, err)
		return
	}
	userEvent, run, err := a.startTurn(r.Context(), sess, a.auth.User(r), body.Text)
	if errors.Is(err, errBusy) {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	if err != nil {
		reply(w, nil, err)
		return
	}
	go run()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	reply(w, userEvent, nil)
}

var errBusy = errors.New("a turn is already running in this session")

// startTurn claims the session for a turn and records the user's message. The caller runs the
// returned function, now or in the background, to execute the turn; it returns the turn's failure.
func (a *app) startTurn(ctx context.Context, sess store.Session, user auth.User, text string) (store.Event, func() error, error) {
	turnCtx, cancel := context.WithCancel(context.Background())
	a.mu.Lock()
	if _, busy := a.turns[sess.ID]; busy {
		a.mu.Unlock()
		cancel()
		return store.Event{}, nil, errBusy
	}
	a.turns[sess.ID] = cancel
	a.mu.Unlock()
	release := a.hold(sess.Project)

	// Who sent it is recorded here, by Bob: the authoritative record. The agent sees the same person
	// in its environment, but anything inside the container (git authors included) can be altered.
	payload, _ := json.Marshal(map[string]string{"text": text, "user_email": user.Email, "user_name": user.Name})
	userEvent, err := a.record(ctx, store.Event{SessionID: sess.ID, Engine: sess.Engine, Kind: "bob.user_message", Payload: payload})
	if err != nil {
		a.endTurn(sess.ID)
		release()
		return store.Event{}, nil, err
	}
	return userEvent, func() error {
		defer release()
		return a.runTurn(turnCtx, sess, user, text)
	}, nil
}

func (a *app) interrupt(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	cancel, ok := a.turns[r.PathValue("session")]
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

// runTurn runs one turn as user: the runtime gives the agent's tools that person's email and
// name, and makes them the author of commits made during the turn. The worker is read afresh
// from the database, so a change to it takes effect on the next message.
// It returns nil when the turn finished (bob.turn_done), and the failure otherwise (bob.turn_failed).
func (a *app) runTurn(ctx context.Context, sess store.Session, user auth.User, text string) (failure error) {
	defer a.endTurn(sess.ID)
	fail := func(err error) {
		failure = err
		log.Printf("session %s: turn failed: %v", sess.ID, err)
		payload, _ := json.Marshal(map[string]string{"error": err.Error()})
		_, _ = a.record(context.Background(), store.Event{SessionID: sess.ID, Engine: sess.Engine, Kind: "bob.turn_failed", Payload: payload})
	}
	turn, err := a.turnRequest(ctx, sess)
	if err != nil {
		fail(err)
		return
	}
	turn.Text, turn.UserEmail, turn.UserName = text, user.Email, user.Name
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
	err = runtime.RunTurn(ctx, base, turn,
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
	return failure
}

// turnRequest is what a session's next turn runs with: its worker's engine, tools and prompt, and
// the session's model and effort where they override the worker's. The system prompt is composed
// fresh each turn: the Bob environment note, the project prompt, and (for a worker chat) the
// worker's prompt — so a change to any of them takes effect on the next message.
func (a *app) turnRequest(ctx context.Context, sess store.Session) (runtime.TurnRequest, error) {
	t := runtime.TurnRequest{SessionID: sess.ID, Engine: sess.Engine, Resume: sess.HarnessSessionID, Model: sess.Model, Effort: sess.Effort}
	p, err := a.store.Project(ctx, sess.Project)
	if err != nil {
		return t, err
	}
	if sess.WorkerID == nil {
		if sess.Worker != "" {
			return t, fmt.Errorf("this chat's worker was deleted; start a new chat")
		}
		t.SystemPrompt = composePrompt(bobNote(sess.Project), p.Prompt)
		return t, nil // a plain chat: nothing but the session's own settings
	}
	wk, err := a.store.WorkerByID(ctx, *sess.WorkerID)
	if err != nil {
		return t, fmt.Errorf("worker %q: %w", sess.Worker, err)
	}
	if wk.Engine != sess.Engine {
		return t, fmt.Errorf("this chat's worker now runs on %s; start a new chat", wk.Engine)
	}
	if t.Model == "" {
		t.Model = wk.Model
	}
	if t.Effort == "" {
		t.Effort = wk.Effort
	}
	t.Tools, t.SystemPrompt = wk.Tools, composePrompt(bobNote(sess.Project), p.Prompt, wk.Prompt)
	return t, nil
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
	if errors.Is(err, store.ErrConflict) {
		http.Error(w, err.Error(), http.StatusConflict)
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
