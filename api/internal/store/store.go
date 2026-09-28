// Package store is Bob's Postgres: projects, sessions and their events.
package store

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrations embed.FS

var ErrNotFound = errors.New("not found")

type Store struct {
	db *pgxpool.Pool
	// Now is the store's clock, for the times it stamps itself (a queued run's start); nil is
	// time.Now. Tests set it to the app's clock.
	Now func() time.Time
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func Open(ctx context.Context, url string) (*Store, error) {
	db, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(ctx); err != nil {
		return nil, fmt.Errorf("connect to postgres: %w", err)
	}
	s := &Store{db: db}
	return s, s.migrate(ctx)
}

func (s *Store) Close() { s.db.Close() }

// Ping checks the database answers.
func (s *Store) Ping(ctx context.Context) error { return s.db.Ping(ctx) }

// migrate applies every embedded migration not yet recorded, each in its own transaction.
func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (name text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return err
	}
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, e := range entries {
		sql, err := migrations.ReadFile("migrations/" + e.Name())
		if err != nil {
			return err
		}
		err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
			tag, err := tx.Exec(ctx, `INSERT INTO schema_migrations (name) VALUES ($1) ON CONFLICT DO NOTHING`, e.Name())
			if err != nil || tag.RowsAffected() == 0 {
				return err
			}
			_, err = tx.Exec(ctx, string(sql))
			return err
		})
		if err != nil {
			return fmt.Errorf("migration %s: %w", e.Name(), err)
		}
	}
	return nil
}

// Project is one project Bob serves. Which projects exist is deployment configuration
// (BOB_PROJECTS); the prompt is edited in Bob.
type Project struct {
	Name      string    `json:"name"`
	Prompt    string    `json:"prompt"`
	CreatedAt time.Time `json:"created_at"`
}

// ReconcileProjects brings the table in step with BOB_PROJECTS: every listed project is inserted
// if new and marked present, and any other is marked absent. Nothing else of a project changes
// here — its prompt is edited in Bob, not in the deploy.
//
// A project is never deleted here. Its sessions and schedules point at this row, and they are the
// record of work that happened; an absent project simply stops being served. Re-listing its name
// brings it back, with its history, which is why names must never be reused for something else.
func (s *Store) ReconcileProjects(ctx context.Context, names []string) error {
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		for _, name := range names {
			if _, err := tx.Exec(ctx, `INSERT INTO projects (name) VALUES ($1)
				ON CONFLICT (name) DO UPDATE SET absent_at = NULL`, name); err != nil {
				return fmt.Errorf("project %s: %w", name, err)
			}
		}
		_, err := tx.Exec(ctx, `UPDATE projects SET absent_at = now() WHERE absent_at IS NULL AND name <> ALL($1)`, names)
		return err
	})
}

// AbsentProjects names the projects in the table that BOB_PROJECTS no longer lists.
func (s *Store) AbsentProjects(ctx context.Context) ([]string, error) {
	rows, err := s.db.Query(ctx, `SELECT name FROM projects WHERE absent_at IS NOT NULL ORDER BY name`)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(row pgx.Row) (string, error) { var n string; return n, row.Scan(&n) })
}

const projectCols = `name, prompt, created_at`

func scanProject(row pgx.Row) (Project, error) {
	var p Project
	err := row.Scan(&p.Name, &p.Prompt, &p.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, ErrNotFound
	}
	return p, err
}

// Project reads one project Bob serves. A project BOB_PROJECTS no longer lists is not found, so its
// routes 404 exactly as an unknown name does.
func (s *Store) Project(ctx context.Context, name string) (Project, error) {
	return scanProject(s.db.QueryRow(ctx, `SELECT `+projectCols+` FROM projects WHERE name = $1 AND absent_at IS NULL`, name))
}

func (s *Store) Projects(ctx context.Context) ([]Project, error) {
	rows, err := s.db.Query(ctx, `SELECT `+projectCols+` FROM projects WHERE absent_at IS NULL ORDER BY name`)
	if err != nil {
		return nil, err
	}
	return collect(rows, scanProject)
}

type Session struct {
	ID      string `json:"id"`
	Project string `json:"project"`
	// WorkerID is the worker the session runs, nil for a plain chat or once that worker is
	// deleted. Worker is its name when the session was created ("" for a plain chat), kept after.
	WorkerID         *string `json:"worker_id"`
	Worker           string  `json:"worker"`
	Engine           string  `json:"engine"`
	HarnessSessionID string  `json:"harness_session_id"`
	// Model and Effort override the worker's settings; empty means the worker's.
	Model     string    `json:"model"`
	Effort    string    `json:"effort"`
	CreatedAt time.Time `json:"created_at"`
	// Filled only by Sessions (the list): the first message, how many messages the user has
	// sent, and when anything last happened, so a chat can be named and sorted by activity.
	Title        string     `json:"title,omitempty"`
	Messages     int        `json:"messages,omitempty"`
	LastActiveAt *time.Time `json:"last_active_at,omitempty"`
	// Schedule is the name of the schedule that started this chat, if one did (list only).
	Schedule string `json:"schedule,omitempty"`
	// WorkerRemoved is set when the session's worker has been deleted (list only): the chat stays
	// readable, but takes no more messages.
	WorkerRemoved bool `json:"worker_removed,omitempty"`
}

const sessionCols = `id, project, worker_id, worker, engine, harness_session_id, model, effort, created_at`

func scanSession(row pgx.Row) (Session, error) {
	var x Session
	err := row.Scan(&x.ID, &x.Project, &x.WorkerID, &x.Worker, &x.Engine, &x.HarnessSessionID, &x.Model, &x.Effort, &x.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return x, ErrNotFound
	}
	return x, err
}

// CreateSession starts a session on the worker workerID, named worker; a plain chat has a nil
// workerID and worker "".
func (s *Store) CreateSession(ctx context.Context, project string, workerID *string, worker, engine, model, effort string) (Session, error) {
	return scanSession(s.db.QueryRow(ctx, `INSERT INTO sessions (project, worker_id, worker, engine, model, effort) VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING `+sessionCols, project, workerID, worker, engine, model, effort))
}

// SetSessionSettings changes a session's model and effort for its next turn.
func (s *Store) SetSessionSettings(ctx context.Context, id, model, effort string) (Session, error) {
	return scanSession(s.db.QueryRow(ctx, `UPDATE sessions SET model = $2, effort = $3 WHERE id = $1
		RETURNING `+sessionCols, id, model, effort))
}

// DeleteSession removes a session and all its events.
func (s *Store) DeleteSession(ctx context.Context, id string) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM sessions WHERE id = $1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func (s *Store) Session(ctx context.Context, id string) (Session, error) {
	return scanSession(s.db.QueryRow(ctx, `SELECT `+sessionCols+` FROM sessions WHERE id = $1`, id))
}

// Sessions lists a project's sessions, most recently active first, each with its title (the
// first user message), message count, last activity, and whether its worker has been deleted.
func (s *Store) Sessions(ctx context.Context, project string) ([]Session, error) {
	rows, err := s.db.Query(ctx, `SELECT s.id, s.project, s.worker_id, s.worker, s.engine, s.harness_session_id, s.model, s.effort, s.created_at,
			COALESCE((SELECT e.payload->>'text' FROM events e WHERE e.session_id = s.id AND e.kind = 'bob.user_message' ORDER BY e.id LIMIT 1), ''),
			(SELECT count(*) FROM events e WHERE e.session_id = s.id AND e.kind = 'bob.user_message'),
			COALESCE((SELECT max(e.created_at) FROM events e WHERE e.session_id = s.id), s.created_at) AS last_active,
			COALESCE((SELECT sc.name FROM schedules sc WHERE sc.id = s.schedule_id), ''),
			s.worker <> '' AND s.worker_id IS NULL
		FROM sessions s WHERE s.project = $1 ORDER BY last_active DESC`, project)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(row pgx.Row) (Session, error) {
		var x Session
		var last time.Time
		err := row.Scan(&x.ID, &x.Project, &x.WorkerID, &x.Worker, &x.Engine, &x.HarnessSessionID, &x.Model, &x.Effort, &x.CreatedAt,
			&x.Title, &x.Messages, &last, &x.Schedule, &x.WorkerRemoved)
		x.LastActiveAt = &last
		return x, err
	})
}

func (s *Store) SetHarnessSessionID(ctx context.Context, id, harnessID string) error {
	_, err := s.db.Exec(ctx, `UPDATE sessions SET harness_session_id = $2 WHERE id = $1`, id, harnessID)
	return err
}

type Event struct {
	ID        int64           `json:"id"`
	SessionID string          `json:"session_id"`
	Engine    string          `json:"engine"`
	Kind      string          `json:"kind"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"created_at"`
}

func (s *Store) AppendEvent(ctx context.Context, e Event) (Event, error) {
	err := s.db.QueryRow(ctx, `INSERT INTO events (session_id, engine, kind, payload) VALUES ($1, $2, $3, $4)
		RETURNING id, created_at`, e.SessionID, e.Engine, e.Kind, e.Payload).Scan(&e.ID, &e.CreatedAt)
	return e, err
}

// Events returns a session's events with id > after, oldest first.
func (s *Store) Events(ctx context.Context, sessionID string, after int64) ([]Event, error) {
	rows, err := s.db.Query(ctx, `SELECT id, session_id, engine, kind, payload, created_at FROM events
		WHERE session_id = $1 AND id > $2 ORDER BY id`, sessionID, after)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(r pgx.Row) (Event, error) {
		var e Event
		return e, r.Scan(&e.ID, &e.SessionID, &e.Engine, &e.Kind, &e.Payload, &e.CreatedAt)
	})
}

// LastUserEmail is who sent a session's latest message (its bob.user_message's user_email:
// a person's email, or "schedule:<id>"), or "" before the first.
func (s *Store) LastUserEmail(ctx context.Context, sessionID string) (string, error) {
	var email string
	err := s.db.QueryRow(ctx, `SELECT COALESCE(payload->>'user_email', '') FROM events
		WHERE session_id = $1 AND kind = 'bob.user_message' ORDER BY id DESC LIMIT 1`, sessionID).Scan(&email)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return email, err
}

func collect[T any](rows pgx.Rows, scan func(pgx.Row) (T, error)) ([]T, error) {
	defer rows.Close()
	out := []T{}
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
