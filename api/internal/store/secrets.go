package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// SecretInfo is what may be shown about a secret: never its value.
type SecretInfo struct {
	Name      string    `json:"name"`
	UpdatedBy string    `json:"updated_by"`
	UpdatedAt time.Time `json:"updated_at"`
}

// SealedSecret is a secret as stored, still encrypted.
type SealedSecret struct {
	Name       string
	Nonce      []byte
	Ciphertext []byte
}

func (s *Store) SetSecret(ctx context.Context, project, name string, nonce, ciphertext []byte, by string) error {
	_, err := s.db.Exec(ctx, `INSERT INTO project_secrets (project, name, nonce, ciphertext, updated_by) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (project, name) DO UPDATE SET nonce = EXCLUDED.nonce, ciphertext = EXCLUDED.ciphertext,
		updated_by = EXCLUDED.updated_by, updated_at = now()`, project, name, nonce, ciphertext, by)
	return err
}

func (s *Store) DeleteSecret(ctx context.Context, project, name string) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM project_secrets WHERE project = $1 AND name = $2`, project, name)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// Secrets lists a project's secrets by name, without values.
func (s *Store) Secrets(ctx context.Context, project string) ([]SecretInfo, error) {
	rows, err := s.db.Query(ctx, `SELECT name, updated_by, updated_at FROM project_secrets WHERE project = $1 ORDER BY name`, project)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(r pgx.Row) (SecretInfo, error) {
		var x SecretInfo
		return x, r.Scan(&x.Name, &x.UpdatedBy, &x.UpdatedAt)
	})
}

// SealedSecrets returns a project's secrets, encrypted, for its container.
func (s *Store) SealedSecrets(ctx context.Context, project string) ([]SealedSecret, error) {
	rows, err := s.db.Query(ctx, `SELECT name, nonce, ciphertext FROM project_secrets WHERE project = $1 ORDER BY name`, project)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(r pgx.Row) (SealedSecret, error) {
		var x SealedSecret
		return x, r.Scan(&x.Name, &x.Nonce, &x.Ciphertext)
	})
}
