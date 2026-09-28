package store

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/badcodetv/bob/internal/embed"
	"github.com/badcodetv/bob/internal/labels"
	"github.com/jackc/pgx/v5"
)

// Memories are append-only, labelled notes a project's agents keep. There is no update and no
// delete: a newer memory supersedes an older one, and two label conventions give that meaning —
//
//   - name=<x>: the memory is a version of "the current value of x", which is the newest memory
//     so labelled (CurrentMemory; CreateMemory's ifCurrent compares against it);
//   - retracts=<id>: the memory withdraws memory <id>, which then no longer appears in search or
//     as a current value. Reading it by id still works — nothing is erased.
//
// Search is hybrid: Postgres full-text and pgvector cosine distance, each ranked over the same
// filtered set, fused by Reciprocal Rank Fusion. Embeddings are computed by the caller
// (internal/embed) and passed in; the store never calls out.

// MaxMemoryBytes is the largest memory content: roughly what text-embedding-3-small accepts.
const MaxMemoryBytes = 24 * 1024

const (
	nameLabel      = "name"
	retractsLabel  = "retracts"
	snippetChars   = 500
	rrfK           = 60  // Reciprocal Rank Fusion: score = Σ 1/(rrfK + rank)
	candidateLimit = 200 // how deep each leg is ranked before fusion
)

type Memory struct {
	ID               string            `json:"id"`
	Project          string            `json:"project"`
	Labels           map[string]string `json:"labels"` // never nil
	Content          string            `json:"content"`
	CreatedByWorker  string            `json:"created_by_worker"`  // "" for a plain chat
	CreatedBySession string            `json:"created_by_session"` // "" once that chat is deleted
	CreatedAt        time.Time         `json:"created_at"`
}

// ErrNotCurrent is CreateMemory refusing a compare-and-swap: IfCurrent is not the newest memory
// labelled name=Name any more. Current is the one that is ("" if none is). Nothing was written.
type ErrNotCurrent struct{ Name, IfCurrent, Current string }

func (e ErrNotCurrent) Error() string {
	if e.Current == "" {
		return fmt.Sprintf("memory %s is not current: no memory holds name=%s now; nothing was written", e.IfCurrent, e.Name)
	}
	return fmt.Sprintf("memory %s is not current for name=%s: %s is; nothing was written — read it, fold in your change and try again", e.IfCurrent, e.Name, e.Current)
}

const memoryCols = `id, project, labels, content, created_by_worker, COALESCE(created_by_session::text, ''), created_at`

func scanMemory(row pgx.Row) (Memory, error) {
	var m Memory
	err := row.Scan(&m.ID, &m.Project, &m.Labels, &m.Content, &m.CreatedByWorker, &m.CreatedBySession, &m.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return m, ErrNotFound
	}
	if m.Labels == nil {
		m.Labels = map[string]string{}
	}
	return m, err
}

// notRetracted is the condition that hides memories another memory of the same project retracts,
// for a query whose memories table is called alias.
func notRetracted(alias string) string {
	return fmt.Sprintf(`NOT EXISTS (SELECT 1 FROM memories r WHERE r.project = %[1]s.project
		AND r.labels ? '%[2]s' AND r.labels->>'%[2]s' = %[1]s.id::text)`, alias, retractsLabel)
}

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// vectorLiteral renders v as pgvector text: [1,2.5,…].
func vectorLiteral(v []float32) string {
	parts := make([]string, len(v))
	for i, f := range v {
		parts[i] = strconv.FormatFloat(float64(f), 'g', -1, 32)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

type queryRower interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// CreateMemory appends m (Project, Content, Labels, CreatedByWorker, CreatedBySession) with its
// embedding, stamped with the store's clock. With ifCurrent set, m must carry a name label, and it
// is written only while ifCurrent is still the newest unretracted memory of that name; otherwise
// the error is ErrNotCurrent. Writers using ifCurrent for one name take turns on a lock; a write
// without it always lands.
func (s *Store) CreateMemory(ctx context.Context, m Memory, embedding []float32, ifCurrent string) (Memory, error) {
	switch {
	case m.Project == "":
		return Memory{}, errors.New("memory: project is required")
	case strings.TrimSpace(m.Content) == "":
		return Memory{}, errors.New("memory: content is required")
	case len(m.Content) > MaxMemoryBytes:
		return Memory{}, fmt.Errorf("memory: content is %d bytes, over the %d-byte limit — keep the document elsewhere and remember where", len(m.Content), MaxMemoryBytes)
	case len(embedding) != embed.Dim:
		return Memory{}, fmt.Errorf("memory: embedding must have %d dimensions, got %d", embed.Dim, len(embedding))
	}
	if err := labels.Validate(m.Labels); err != nil {
		return Memory{}, fmt.Errorf("memory: %w", err)
	}
	insert := func(q queryRower) (Memory, error) {
		return scanMemory(q.QueryRow(ctx, `INSERT INTO memories (project, labels, content, embedding, created_by_worker, created_by_session, created_at)
			VALUES ($1, $2, $3, $4::vector, $5, NULLIF($6, '')::uuid, $7) RETURNING `+memoryCols,
			m.Project, emptyIfNil(m.Labels), m.Content, vectorLiteral(embedding), m.CreatedByWorker, m.CreatedBySession, s.now()))
	}
	ifCurrent = strings.TrimSpace(ifCurrent)
	if ifCurrent == "" {
		return insert(s.db)
	}
	name := m.Labels[nameLabel]
	if name == "" {
		return Memory{}, errors.New("memory: if_current needs a name label: it compares against the newest memory of that name")
	}
	var out Memory
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		// Held to commit, so the next writer of this name reads a snapshot with this row in it.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('bob-memory:' || $1 || chr(10) || $2))`, m.Project, name); err != nil {
			return err
		}
		cur, err := currentMemory(ctx, tx, m.Project, name)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		if cur.ID != ifCurrent {
			return ErrNotCurrent{Name: name, IfCurrent: ifCurrent, Current: cur.ID}
		}
		out, err = insert(tx)
		return err
	})
	return out, err
}

// Memory reads one memory of project in full, retracted or not.
func (s *Store) Memory(ctx context.Context, project, id string) (Memory, error) {
	if !uuidRe.MatchString(id) {
		return Memory{}, ErrNotFound
	}
	return scanMemory(s.db.QueryRow(ctx, `SELECT `+memoryCols+` FROM memories WHERE project = $1 AND id = $2`, project, id))
}

// CurrentMemory is the newest unretracted memory of project labelled name=<name>, in full.
func (s *Store) CurrentMemory(ctx context.Context, project, name string) (Memory, error) {
	return currentMemory(ctx, s.db, project, name)
}

func currentMemory(ctx context.Context, q queryRower, project, name string) (Memory, error) {
	return scanMemory(q.QueryRow(ctx, `SELECT `+memoryCols+` FROM memories m
		WHERE m.project = $1 AND m.labels @> jsonb_build_object('`+nameLabel+`', $2::text) AND `+notRetracted("m")+`
		ORDER BY m.created_at DESC, m.id DESC LIMIT 1`, project, name))
}

// MemorySearch is one search of a project's memories. Every filter narrows the set before
// ranking; retracted memories are always left out.
type MemorySearch struct {
	Project  string
	Selector labels.Selector
	// Query is free text; "" lists the filtered set newest first. QueryEmbedding is its embedding;
	// nil searches by keyword alone.
	Query          string
	QueryEmbedding []float32
	Limit          int       // default 20, at most 100
	Since, Until   time.Time // inclusive; zero = unbounded
	// CreatedByWorker keeps only memories written by chats of this worker.
	CreatedByWorker string
	// LatestPer is a label key: keep only the newest memory for each value of it (and none
	// without it), before ranking.
	LatestPer string
}

type MemoryHit struct {
	ID               string            `json:"id"`
	Labels           map[string]string `json:"labels"`
	Snippet          string            `json:"snippet"` // the first 500 characters
	Score            float64           `json:"score"`   // 0 without a query
	CreatedByWorker  string            `json:"created_by_worker"`
	CreatedBySession string            `json:"created_by_session"`
	CreatedAt        time.Time         `json:"created_at"`
}

// SearchMemories filters a project's memories, then with a query ranks them by keyword
// (ts_rank_cd) and by vector distance, each leg's top 200, fused by Reciprocal Rank Fusion with
// k = 60; ties go to the newer memory. Without a query it returns the filtered set newest first.
func (s *Store) SearchMemories(ctx context.Context, q MemorySearch) ([]MemoryHit, error) {
	if q.Project == "" {
		return nil, errors.New("memory search: project is required")
	}
	if !q.Since.IsZero() && !q.Until.IsZero() && q.Since.After(q.Until) {
		return nil, errors.New("memory search: since is after until, which matches nothing")
	}
	if q.QueryEmbedding != nil && len(q.QueryEmbedding) != embed.Dim {
		return nil, fmt.Errorf("memory search: query embedding must have %d dimensions, got %d", embed.Dim, len(q.QueryEmbedding))
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 20
	}
	limit = min(limit, 100)

	var args []any
	arg := func(v any) string { args = append(args, v); return "$" + strconv.Itoa(len(args)) }
	where := []string{"f.project = " + arg(q.Project), notRetracted("f")}
	if len(q.Selector) > 0 {
		cond, selArgs := q.Selector.SQL("f.labels", len(args)+1)
		where, args = append(where, cond), append(args, selArgs...)
	}
	if !q.Since.IsZero() {
		where = append(where, "f.created_at >= "+arg(q.Since))
	}
	if !q.Until.IsZero() {
		where = append(where, "f.created_at <= "+arg(q.Until))
	}
	if q.CreatedByWorker != "" {
		where = append(where, "f.created_by_worker = "+arg(q.CreatedByWorker))
	}
	distinct, reduce := "", ""
	if key := strings.TrimSpace(q.LatestPer); key != "" {
		// Checked against the label grammar, so it is safe to write into the SQL: DISTINCT ON
		// must match its ORDER BY expression, which a placeholder cannot promise.
		if err := labels.Validate(map[string]string{key: "x"}); err != nil {
			return nil, fmt.Errorf("memory search: latest_per: %w", err)
		}
		where = append(where, "jsonb_exists(f.labels, "+arg(key)+")")
		distinct = fmt.Sprintf("DISTINCT ON (f.labels->>'%s') ", key)
		reduce = fmt.Sprintf("ORDER BY f.labels->>'%s', f.created_at DESC, f.id DESC", key)
	}
	sql := `WITH filtered AS (SELECT ` + distinct + `f.* FROM memories f WHERE ` + strings.Join(where, " AND ") + ` ` + reduce + `)`
	hitCols := fmt.Sprintf(`f.id, f.labels, left(f.content, %d), %%s, f.created_by_worker, COALESCE(f.created_by_session::text, ''), f.created_at`, snippetChars)

	if strings.TrimSpace(q.Query) == "" {
		sql += ` SELECT ` + fmt.Sprintf(hitCols, "0::float8") + ` FROM filtered f ORDER BY f.created_at DESC, f.id DESC LIMIT ` + arg(limit)
		return s.scanHits(ctx, sql, args)
	}

	query := arg(q.Query)
	sql += fmt.Sprintf(`, kw AS (
		SELECT id, row_number() OVER (ORDER BY kscore DESC, created_at DESC, id DESC) AS rnk FROM (
			SELECT id, created_at, ts_rank_cd(content_tsv, plainto_tsquery('english', %[1]s)) AS kscore FROM filtered
			WHERE content_tsv @@ plainto_tsquery('english', %[1]s)
			ORDER BY kscore DESC, created_at DESC, id DESC LIMIT %[2]d) k)`, query, candidateLimit)
	score := fmt.Sprintf(`COALESCE(1.0/(%d + kw.rnk), 0)`, rrfK)
	joins, matched := `LEFT JOIN kw ON kw.id = f.id`, `kw.id IS NOT NULL`
	if q.QueryEmbedding != nil {
		sql += fmt.Sprintf(`, sem AS (
		SELECT id, row_number() OVER (ORDER BY dist, created_at DESC, id DESC) AS rnk FROM (
			SELECT id, created_at, embedding <=> %s::vector AS dist FROM filtered
			ORDER BY dist, created_at DESC, id DESC LIMIT %d) v)`, arg(vectorLiteral(q.QueryEmbedding)), candidateLimit)
		score += fmt.Sprintf(` + COALESCE(1.0/(%d + sem.rnk), 0)`, rrfK)
		joins += ` LEFT JOIN sem ON sem.id = f.id`
		matched += ` OR sem.id IS NOT NULL`
	}
	sql += ` SELECT ` + fmt.Sprintf(hitCols, `(`+score+`)::float8 AS score`) + ` FROM filtered f ` + joins +
		` WHERE ` + matched + ` ORDER BY score DESC, f.created_at DESC, f.id DESC LIMIT ` + arg(limit)
	return s.scanHits(ctx, sql, args)
}

func (s *Store) scanHits(ctx context.Context, sql string, args []any) ([]MemoryHit, error) {
	rows, err := s.db.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("memory search: %w", err)
	}
	return collect(rows, func(row pgx.Row) (MemoryHit, error) {
		var h MemoryHit
		err := row.Scan(&h.ID, &h.Labels, &h.Snippet, &h.Score, &h.CreatedByWorker, &h.CreatedBySession, &h.CreatedAt)
		if h.Labels == nil {
			h.Labels = map[string]string{}
		}
		return h, err
	})
}
