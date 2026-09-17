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

type Store struct{ db *pgxpool.Pool }

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

type Project struct {
	Name      string    `json:"name"`
	RepoURL   string    `json:"repo_url"`
	RepoRef   string    `json:"repo_ref"`
	Subfolder string    `json:"subfolder"`
	Image     string    `json:"image"`
	RepoMount string    `json:"repo_mount,omitempty"`
	// FilesRoot is the repository folder the Files page opens on; empty = the root.
	FilesRoot string `json:"files_root"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Store) CreateProject(ctx context.Context, p Project) (Project, error) {
	if p.RepoRef == "" {
		p.RepoRef = "main"
	}
	err := s.db.QueryRow(ctx, `INSERT INTO projects (name, repo_url, repo_ref, subfolder, image, repo_mount, files_root)
		VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING created_at`,
		p.Name, p.RepoURL, p.RepoRef, p.Subfolder, p.Image, p.RepoMount, p.FilesRoot).Scan(&p.CreatedAt)
	return p, err
}

// UpdateProject changes a project's repository settings. The name cannot change: the
// project's container and volume are named after it.
func (s *Store) UpdateProject(ctx context.Context, p Project) (Project, error) {
	if p.RepoRef == "" {
		p.RepoRef = "main"
	}
	return scanProject(s.db.QueryRow(ctx, `UPDATE projects SET repo_url = $2, repo_ref = $3, subfolder = $4, image = $5, files_root = $6
		WHERE name = $1 RETURNING `+projectCols, p.Name, p.RepoURL, p.RepoRef, p.Subfolder, p.Image, p.FilesRoot))
}

// DeleteProject removes a project with all its sessions and their events.
func (s *Store) DeleteProject(ctx context.Context, name string) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM projects WHERE name = $1`, name)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

const projectCols = `name, repo_url, repo_ref, subfolder, image, repo_mount, files_root, created_at`

func scanProject(row pgx.Row) (Project, error) {
	var p Project
	err := row.Scan(&p.Name, &p.RepoURL, &p.RepoRef, &p.Subfolder, &p.Image, &p.RepoMount, &p.FilesRoot, &p.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, ErrNotFound
	}
	return p, err
}

func (s *Store) Project(ctx context.Context, name string) (Project, error) {
	return scanProject(s.db.QueryRow(ctx, `SELECT `+projectCols+` FROM projects WHERE name = $1`, name))
}

func (s *Store) Projects(ctx context.Context) ([]Project, error) {
	rows, err := s.db.Query(ctx, `SELECT `+projectCols+` FROM projects ORDER BY name`)
	if err != nil {
		return nil, err
	}
	return collect(rows, scanProject)
}

type Session struct {
	ID               string `json:"id"`
	Project          string `json:"project"`
	Worker           string `json:"worker"`
	Engine           string `json:"engine"`
	HarnessSessionID string `json:"harness_session_id"`
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
}

const sessionCols = `id, project, worker, engine, harness_session_id, model, effort, created_at`

func scanSession(row pgx.Row) (Session, error) {
	var x Session
	err := row.Scan(&x.ID, &x.Project, &x.Worker, &x.Engine, &x.HarnessSessionID, &x.Model, &x.Effort, &x.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return x, ErrNotFound
	}
	return x, err
}

func (s *Store) CreateSession(ctx context.Context, project, worker, engine, model, effort string) (Session, error) {
	return scanSession(s.db.QueryRow(ctx, `INSERT INTO sessions (project, worker, engine, model, effort) VALUES ($1, $2, $3, $4, $5)
		RETURNING `+sessionCols, project, worker, engine, model, effort))
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
// first user message), message count and last activity.
func (s *Store) Sessions(ctx context.Context, project string) ([]Session, error) {
	rows, err := s.db.Query(ctx, `SELECT s.id, s.project, s.worker, s.engine, s.harness_session_id, s.model, s.effort, s.created_at,
			COALESCE((SELECT e.payload->>'text' FROM events e WHERE e.session_id = s.id AND e.kind = 'bob.user_message' ORDER BY e.id LIMIT 1), ''),
			(SELECT count(*) FROM events e WHERE e.session_id = s.id AND e.kind = 'bob.user_message'),
			COALESCE((SELECT max(e.created_at) FROM events e WHERE e.session_id = s.id), s.created_at) AS last_active,
			COALESCE((SELECT sc.name FROM schedules sc WHERE sc.id = s.schedule_id), '')
		FROM sessions s WHERE s.project = $1 ORDER BY last_active DESC`, project)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(row pgx.Row) (Session, error) {
		var x Session
		var last time.Time
		err := row.Scan(&x.ID, &x.Project, &x.Worker, &x.Engine, &x.HarnessSessionID, &x.Model, &x.Effort, &x.CreatedAt,
			&x.Title, &x.Messages, &last, &x.Schedule)
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
