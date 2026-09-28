package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/badcodetv/bob/internal/labels"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Worker is one of a project's agents: which harness runs it, with what model, effort and tools,
// and its prompt. Its name never changes; a "rename" is a new worker.
type Worker struct {
	ID      string `json:"id"`
	Project string `json:"project"`
	Name    string `json:"name"`
	Engine  string `json:"engine"`
	Model   string `json:"model"`
	Effort  string `json:"effort"`
	// Tools are what the agent may use without asking; nil means the harness's default.
	Tools  []string `json:"tools,omitempty"`
	Prompt string   `json:"prompt"`
	// Labels is never nil for a worker returned by this package: {} rather than null.
	Labels    map[string]string `json:"labels"`
	CreatedBy string            `json:"created_by"`
	UpdatedBy string            `json:"updated_by"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
}

// WorkerVersion is one create, update or delete of a worker: the worker as it was after the
// change (for a delete, as it was before), who changed it, when and why.
type WorkerVersion struct {
	ID        int64           `json:"id"`
	WorkerID  string          `json:"worker_id"`
	Project   string          `json:"project"`
	Name      string          `json:"name"`
	Action    string          `json:"action"` // create | update | delete
	Snapshot  json.RawMessage `json:"snapshot"`
	Why       string          `json:"why"`
	ChangedBy string          `json:"changed_by"`
	ChangedAt time.Time       `json:"changed_at"`
}

// PromptVersion is one change to a project's prompt.
type PromptVersion struct {
	ID        int64     `json:"id"`
	Prompt    string    `json:"prompt"`
	Why       string    `json:"why"`
	ChangedBy string    `json:"changed_by"`
	ChangedAt time.Time `json:"changed_at"`
}

// ErrConflict is returned when a write collides with something that already exists, e.g. a
// worker name already used in the project.
var ErrConflict = errors.New("already exists")

const workerCols = `id, project, name, engine, model, effort, tools, prompt, labels, created_by, updated_by, created_at, updated_at`

func scanWorker(row pgx.Row) (Worker, error) {
	var w Worker
	err := row.Scan(&w.ID, &w.Project, &w.Name, &w.Engine, &w.Model, &w.Effort, &w.Tools, &w.Prompt, &w.Labels,
		&w.CreatedBy, &w.UpdatedBy, &w.CreatedAt, &w.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return w, ErrNotFound
	}
	if w.Labels == nil {
		w.Labels = map[string]string{}
	}
	return w, err
}

// emptyIfNil returns m, or an empty map if m is nil, so labels is always jsonb '{}' rather than
// null.
func emptyIfNil(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

// isUniqueViolation reports whether err is Postgres error code 23505 (unique_violation).
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// WorkerByID reads the worker a session or schedule points at.
func (s *Store) WorkerByID(ctx context.Context, id string) (Worker, error) {
	if !isUUID(id) {
		return Worker{}, ErrNotFound
	}
	return scanWorker(s.db.QueryRow(ctx, `SELECT `+workerCols+` FROM workers WHERE id = $1`, id))
}

// Worker reads a project's worker by name.
func (s *Store) Worker(ctx context.Context, project, name string) (Worker, error) {
	return scanWorker(s.db.QueryRow(ctx, `SELECT `+workerCols+` FROM workers WHERE project = $1 AND name = $2`, project, name))
}

// Workers lists a project's workers by name, restricted to those matching sel (a zero-length
// Selector matches every worker).
func (s *Store) Workers(ctx context.Context, project string, sel labels.Selector) ([]Worker, error) {
	cond, args := sel.SQL("labels", 2)
	rows, err := s.db.Query(ctx, `SELECT `+workerCols+` FROM workers WHERE project = $1 AND `+cond+` ORDER BY name`,
		append([]any{project}, args...)...)
	if err != nil {
		return nil, err
	}
	return collect(rows, scanWorker)
}

// CreateWorker inserts a worker and records the creation as its first version. why must not be
// empty. A duplicate (project, name) is ErrConflict.
func (s *Store) CreateWorker(ctx context.Context, w Worker, by, why string) (Worker, error) {
	if why == "" {
		return Worker{}, errors.New("why is required")
	}
	var created Worker
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var err error
		created, err = scanWorker(tx.QueryRow(ctx, `INSERT INTO workers (project, name, engine, model, effort, tools, prompt, labels, created_by, updated_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9) RETURNING `+workerCols,
			w.Project, w.Name, w.Engine, w.Model, w.Effort, w.Tools, w.Prompt, emptyIfNil(w.Labels), by))
		if err != nil {
			return err
		}
		return recordWorkerVersion(ctx, tx, created, "create", why, by)
	})
	if isUniqueViolation(err) {
		return Worker{}, ErrConflict
	}
	return created, err
}

// UpdateWorker saves a worker's engine, model, effort, tools and prompt, matched by (project,
// name); its name cannot change. why must not be empty.
func (s *Store) UpdateWorker(ctx context.Context, w Worker, by, why string) (Worker, error) {
	if why == "" {
		return Worker{}, errors.New("why is required")
	}
	var updated Worker
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var err error
		updated, err = scanWorker(tx.QueryRow(ctx, `UPDATE workers SET engine = $3, model = $4, effort = $5, tools = $6, prompt = $7,
			labels = $8, updated_by = $9, updated_at = now() WHERE project = $1 AND name = $2 RETURNING `+workerCols,
			w.Project, w.Name, w.Engine, w.Model, w.Effort, w.Tools, w.Prompt, emptyIfNil(w.Labels), by))
		if err != nil {
			return err
		}
		return recordWorkerVersion(ctx, tx, updated, "update", why, by)
	})
	return updated, err
}

// DeleteWorker removes a worker, records the deletion with its last snapshot, and — through the
// database's foreign keys — cascades: its schedules are deleted and its sessions' worker_id is
// nulled (they stay readable, but the session keeps the worker's name). why must not be empty.
func (s *Store) DeleteWorker(ctx context.Context, project, name, by, why string) error {
	if why == "" {
		return errors.New("why is required")
	}
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		w, err := scanWorker(tx.QueryRow(ctx, `SELECT `+workerCols+` FROM workers WHERE project = $1 AND name = $2 FOR UPDATE`, project, name))
		if err != nil {
			return err
		}
		if err := recordWorkerVersion(ctx, tx, w, "delete", why, by); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `DELETE FROM workers WHERE id = $1`, w.ID)
		return err
	})
}

// recordWorkerVersion snapshots w into worker_versions as one row of the transaction that wrote it.
func recordWorkerVersion(ctx context.Context, tx pgx.Tx, w Worker, action, why, by string) error {
	snapshot, err := json.Marshal(w)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO worker_versions (worker_id, project, name, action, snapshot, why, changed_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`, w.ID, w.Project, w.Name, action, snapshot, why, by)
	return err
}

// WorkerVersions lists a worker's history, newest first. It works after the worker is deleted:
// there is no foreign key from worker_versions to workers.
func (s *Store) WorkerVersions(ctx context.Context, workerID string) ([]WorkerVersion, error) {
	rows, err := s.db.Query(ctx, `SELECT id, worker_id, project, name, action, snapshot, why, changed_by, changed_at
		FROM worker_versions WHERE worker_id = $1 ORDER BY id DESC`, workerID)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(row pgx.Row) (WorkerVersion, error) {
		var v WorkerVersion
		err := row.Scan(&v.ID, &v.WorkerID, &v.Project, &v.Name, &v.Action, &v.Snapshot, &v.Why, &v.ChangedBy, &v.ChangedAt)
		return v, err
	})
}

// SetProjectPrompt changes a project's prompt and records the change. why must not be empty.
func (s *Store) SetProjectPrompt(ctx context.Context, project, prompt, by, why string) error {
	if why == "" {
		return errors.New("why is required")
	}
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE projects SET prompt = $2 WHERE name = $1`, project, prompt)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		_, err = tx.Exec(ctx, `INSERT INTO project_prompt_versions (project, prompt, why, changed_by) VALUES ($1, $2, $3, $4)`,
			project, prompt, why, by)
		return err
	})
}

// ProjectPromptVersions lists a project's prompt history, newest first.
func (s *Store) ProjectPromptVersions(ctx context.Context, project string) ([]PromptVersion, error) {
	rows, err := s.db.Query(ctx, `SELECT id, prompt, why, changed_by, changed_at FROM project_prompt_versions
		WHERE project = $1 ORDER BY id DESC`, project)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(row pgx.Row) (PromptVersion, error) {
		var v PromptVersion
		err := row.Scan(&v.ID, &v.Prompt, &v.Why, &v.ChangedBy, &v.ChangedAt)
		return v, err
	})
}
