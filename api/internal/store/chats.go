package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// ChatHit is one message of a chat that matched a search: a person's message or an agent's reply.
type ChatHit struct {
	SessionID string    `json:"chat_id"`
	EventID   int64     `json:"event_id"`
	Role      string    `json:"role"` // "user" or "assistant"
	UserEmail string    `json:"user_email,omitempty"`
	Snippet   string    `json:"snippet"`
	CreatedAt time.Time `json:"created_at"`
}

// messageText is the readable text of an event, empty for one that is no message: a person's
// bob.user_message, a Claude reply's text blocks (not a sub-agent's), or a Codex reply in either
// of its stored formats (app-server item/completed, exec item.completed). It mirrors what the web
// app's engine converters show as a message.
const messageText = `COALESCE(CASE e.kind
	WHEN 'bob.user_message' THEN e.payload->>'text'
	WHEN 'assistant' THEN CASE WHEN COALESCE(e.payload->>'parent_tool_use_id', '') = '' THEN
		(SELECT string_agg(b->>'text', E'\n') FROM jsonb_array_elements(CASE WHEN jsonb_typeof(e.payload#>'{message,content}') = 'array'
			THEN e.payload#>'{message,content}' ELSE '[]'::jsonb END) b WHERE b->>'type' = 'text') END
	WHEN 'item/completed' THEN CASE WHEN e.payload#>>'{params,item,type}' = 'agentMessage' THEN e.payload#>>'{params,item,text}' END
	WHEN 'item.completed' THEN CASE WHEN e.payload#>>'{item,type}' = 'agent_message' THEN e.payload#>>'{item,text}' END
END, '')`

// SearchChats finds the messages of a project's chats that match query (web-search syntax:
// words, "quoted phrases", -excluded, or), best match first. There is no index behind it: it
// reads every message of the project, which is fine at Bob's size.
func (s *Store) SearchChats(ctx context.Context, project, query string, limit int) ([]ChatHit, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := s.db.Query(ctx, `SELECT e.session_id, e.id, CASE WHEN e.kind = 'bob.user_message' THEN 'user' ELSE 'assistant' END,
			COALESCE(e.payload->>'user_email', ''),
			ts_headline('english', m.text, q, 'MaxWords=40, MinWords=15, StartSel=**, StopSel=**'), e.created_at
		FROM events e JOIN sessions s ON s.id = e.session_id,
			LATERAL (SELECT `+messageText+` AS text) m, websearch_to_tsquery('english', $2) q
		WHERE s.project = $1 AND e.kind IN ('bob.user_message', 'assistant', 'item/completed', 'item.completed')
			AND m.text <> '' AND to_tsvector('english', m.text) @@ q
		ORDER BY ts_rank(to_tsvector('english', m.text), q) DESC, e.id DESC LIMIT $3`, project, query, limit)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(r pgx.Row) (ChatHit, error) {
		var h ChatHit
		err := r.Scan(&h.SessionID, &h.EventID, &h.Role, &h.UserEmail, &h.Snippet, &h.CreatedAt)
		return h, err
	})
}
