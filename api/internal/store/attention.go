package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Attention is a request for a person to look at a chat (T23). It is open until a person replies
// in the chat (closed as "answered") or dismisses it ("dismissed").
type Attention struct {
	ID          string     `json:"id"`
	Project     string     `json:"project"`
	SessionID   string     `json:"session_id"`
	Worker      string     `json:"worker"` // "" for a plain chat
	Message     string     `json:"message"`
	Kind        string     `json:"kind"` // ask | notice
	CreatedAt   time.Time  `json:"created_at"`
	ClosedAt    *time.Time `json:"closed_at"`
	ClosedBy    string     `json:"closed_by,omitempty"`
	CloseReason string     `json:"close_reason,omitempty"`
}

const attentionCols = `id, project, session_id, worker, message, kind, created_at, closed_at, COALESCE(closed_by, ''), COALESCE(close_reason, '')`

func scanAttention(row pgx.Row) (Attention, error) {
	var x Attention
	err := row.Scan(&x.ID, &x.Project, &x.SessionID, &x.Worker, &x.Message, &x.Kind, &x.CreatedAt, &x.ClosedAt, &x.ClosedBy, &x.CloseReason)
	if errors.Is(err, pgx.ErrNoRows) {
		return x, ErrNotFound
	}
	return x, err
}

// CreateAttention stores an open request. Project is taken from the session.
func (s *Store) CreateAttention(ctx context.Context, sessionID, worker, message, kind string) (Attention, error) {
	return scanAttention(s.db.QueryRow(ctx, `INSERT INTO attention_requests (project, session_id, worker, message, kind)
		SELECT project, id, $2, $3, $4 FROM sessions WHERE id = $1 RETURNING `+attentionCols, sessionID, worker, message, kind))
}

// Attention returns one request, or ErrNotFound (also for an id that is not a uuid).
func (s *Store) Attention(ctx context.Context, id string) (Attention, error) {
	return scanAttention(s.db.QueryRow(ctx, `SELECT `+attentionCols+` FROM attention_requests WHERE id::text = $1`, id))
}

// Attentions lists a project's requests, newest first: only the open ones, or the latest 100.
func (s *Store) Attentions(ctx context.Context, project string, openOnly bool) ([]Attention, error) {
	rows, err := s.db.Query(ctx, `SELECT `+attentionCols+` FROM attention_requests
		WHERE project = $1 AND (NOT $2 OR closed_at IS NULL) ORDER BY created_at DESC, id LIMIT 100`, project, openOnly)
	if err != nil {
		return nil, err
	}
	return collect(rows, scanAttention)
}

// CloseAttention closes one open request for the given reason ("answered" or "dismissed") on behalf
// of by. Closing an already-closed request changes nothing; an unknown id is ErrNotFound.
func (s *Store) CloseAttention(ctx context.Context, id, by, reason string) (Attention, error) {
	x, err := scanAttention(s.db.QueryRow(ctx, `UPDATE attention_requests SET closed_at = now(), closed_by = $2, close_reason = $3
		WHERE id::text = $1 AND closed_at IS NULL RETURNING `+attentionCols, id, by, reason))
	if errors.Is(err, ErrNotFound) {
		return s.Attention(ctx, id)
	}
	return x, err
}

// CloseSessionAttention closes every open request of a chat as answered by by, and says how many.
func (s *Store) CloseSessionAttention(ctx context.Context, sessionID, by string) (int, error) {
	tag, err := s.db.Exec(ctx, `UPDATE attention_requests SET closed_at = now(), closed_by = $2, close_reason = 'answered'
		WHERE session_id = $1 AND closed_at IS NULL`, sessionID, by)
	return int(tag.RowsAffected()), err
}
