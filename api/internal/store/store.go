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
	CreatedAt time.Time `json:"created_at"`
}

func (s *Store) CreateProject(ctx context.Context, p Project) (Project, error) {
	if p.RepoRef == "" {
		p.RepoRef = "main"
	}
	err := s.db.QueryRow(ctx, `INSERT INTO projects (name, repo_url, repo_ref, subfolder, image, repo_mount)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING created_at`,
		p.Name, p.RepoURL, p.RepoRef, p.Subfolder, p.Image, p.RepoMount).Scan(&p.CreatedAt)
	return p, err
}

const projectCols = `name, repo_url, repo_ref, subfolder, image, repo_mount, created_at`

func scanProject(row pgx.Row) (Project, error) {
	var p Project
	err := row.Scan(&p.Name, &p.RepoURL, &p.RepoRef, &p.Subfolder, &p.Image, &p.RepoMount, &p.CreatedAt)
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
	ID               string    `json:"id"`
	Project          string    `json:"project"`
	Worker           string    `json:"worker"`
	Engine           string    `json:"engine"`
	HarnessSessionID string    `json:"harness_session_id"`
	CreatedAt        time.Time `json:"created_at"`
}

const sessionCols = `id, project, worker, engine, harness_session_id, created_at`

func scanSession(row pgx.Row) (Session, error) {
	var x Session
	err := row.Scan(&x.ID, &x.Project, &x.Worker, &x.Engine, &x.HarnessSessionID, &x.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return x, ErrNotFound
	}
	return x, err
}

func (s *Store) CreateSession(ctx context.Context, project, worker, engine string) (Session, error) {
	return scanSession(s.db.QueryRow(ctx, `INSERT INTO sessions (project, worker, engine) VALUES ($1, $2, $3)
		RETURNING `+sessionCols, project, worker, engine))
}

func (s *Store) Session(ctx context.Context, id string) (Session, error) {
	return scanSession(s.db.QueryRow(ctx, `SELECT `+sessionCols+` FROM sessions WHERE id = $1`, id))
}

func (s *Store) Sessions(ctx context.Context, project string) ([]Session, error) {
	rows, err := s.db.Query(ctx, `SELECT `+sessionCols+` FROM sessions WHERE project = $1 ORDER BY created_at DESC`, project)
	if err != nil {
		return nil, err
	}
	return collect(rows, scanSession)
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
