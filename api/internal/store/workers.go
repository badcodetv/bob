package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
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
	Tools     []string  `json:"tools,omitempty"`
	Prompt    string    `json:"prompt"`
	CreatedBy string    `json:"created_by"`
	UpdatedBy string    `json:"updated_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

const workerCols = `id, project, name, engine, model, effort, tools, prompt, created_by, updated_by, created_at, updated_at`

func scanWorker(row pgx.Row) (Worker, error) {
	var w Worker
	err := row.Scan(&w.ID, &w.Project, &w.Name, &w.Engine, &w.Model, &w.Effort, &w.Tools, &w.Prompt,
		&w.CreatedBy, &w.UpdatedBy, &w.CreatedAt, &w.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return w, ErrNotFound
	}
	return w, err
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
