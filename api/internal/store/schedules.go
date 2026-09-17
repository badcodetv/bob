package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Schedule starts a new session on Worker with Message whenever Cron fires in Timezone.
type Schedule struct {
	ID       string `json:"id"`
	Project  string `json:"project"`
	Name     string `json:"name"`
	Worker   string `json:"worker"`
	Cron     string `json:"cron"`
	Timezone string `json:"timezone"`
	Message  string `json:"message"`
	Enabled  bool   `json:"enabled"`
	// KeepSessions is how many of this schedule's sessions to keep; older ones are deleted.
	KeepSessions int       `json:"keep_sessions"`
	CreatedAt    time.Time `json:"created_at"`
	// ChangedAt is when it was last turned on or had its cron or timezone changed.
	ChangedAt time.Time `json:"changed_at"`
}

const scheduleCols = `id, project, name, worker, cron, timezone, message, enabled, keep_sessions, created_at, changed_at`

func scanSchedule(row pgx.Row) (Schedule, error) {
	var x Schedule
	err := row.Scan(&x.ID, &x.Project, &x.Name, &x.Worker, &x.Cron, &x.Timezone, &x.Message, &x.Enabled, &x.KeepSessions, &x.CreatedAt, &x.ChangedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return x, ErrNotFound
	}
	return x, err
}

func (s *Store) CreateSchedule(ctx context.Context, x Schedule) (Schedule, error) {
	return scanSchedule(s.db.QueryRow(ctx, `INSERT INTO schedules (project, name, worker, cron, timezone, message, enabled, keep_sessions)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING `+scheduleCols,
		x.Project, x.Name, x.Worker, x.Cron, x.Timezone, x.Message, x.Enabled, x.KeepSessions))
}

// UpdateSchedule saves every field but the id, project and creation time.
func (s *Store) UpdateSchedule(ctx context.Context, x Schedule) (Schedule, error) {
	return scanSchedule(s.db.QueryRow(ctx, `UPDATE schedules SET name = $2, worker = $3, cron = $4, timezone = $5, message = $6,
		enabled = $7, keep_sessions = $8, changed_at = $9 WHERE id = $1 RETURNING `+scheduleCols,
		x.ID, x.Name, x.Worker, x.Cron, x.Timezone, x.Message, x.Enabled, x.KeepSessions, x.ChangedAt))
}

func (s *Store) DeleteSchedule(ctx context.Context, id string) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM schedules WHERE id = $1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func (s *Store) Schedule(ctx context.Context, id string) (Schedule, error) {
	if !isUUID(id) {
		return Schedule{}, ErrNotFound
	}
	return scanSchedule(s.db.QueryRow(ctx, `SELECT `+scheduleCols+` FROM schedules WHERE id = $1`, id))
}

// Schedules lists a project's schedules by name; with project "", every enabled schedule.
func (s *Store) Schedules(ctx context.Context, project string) ([]Schedule, error) {
	q, args := `SELECT `+scheduleCols+` FROM schedules WHERE project = $1 ORDER BY name`, []any{project}
	if project == "" {
		q, args = `SELECT `+scheduleCols+` FROM schedules WHERE enabled ORDER BY project, name`, nil
	}
	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	return collect(rows, scanSchedule)
}

// Run is one firing, manual run or skip of a schedule.
type Run struct {
	ID         int64      `json:"id"`
	ScheduleID string     `json:"schedule_id"`
	SessionID  *string    `json:"session_id"`
	Trigger    string     `json:"trigger"` // cron | manual
	Status     string     `json:"status"`  // running | ok | failed | skipped
	Detail     string     `json:"detail"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
}

const runCols = `id, schedule_id, session_id, trigger, status, detail, started_at, finished_at`

func scanRun(row pgx.Row) (Run, error) {
	var x Run
	err := row.Scan(&x.ID, &x.ScheduleID, &x.SessionID, &x.Trigger, &x.Status, &x.Detail, &x.StartedAt, &x.FinishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return x, ErrNotFound
	}
	return x, err
}

// StartRun records a run as running, unless the schedule already has one running: then it
// records a skipped run instead. The schedule's row is locked while deciding, so two callers
// can never both start.
func (s *Store) StartRun(ctx context.Context, scheduleID, trigger string, at time.Time) (Run, error) {
	var run Run
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var id string
		if err := tx.QueryRow(ctx, `SELECT id FROM schedules WHERE id = $1 FOR UPDATE`, scheduleID).Scan(&id); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		var busy bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM schedule_runs WHERE schedule_id = $1 AND status = 'running')`, scheduleID).Scan(&busy); err != nil {
			return err
		}
		status, detail, finished := "running", "", (*time.Time)(nil)
		if busy {
			status, detail, finished = "skipped", "previous run still running", &at
		}
		var err error
		run, err = scanRun(tx.QueryRow(ctx, `INSERT INTO schedule_runs (schedule_id, trigger, status, detail, started_at, finished_at)
			VALUES ($1, $2, $3, $4, $5, $6) RETURNING `+runCols, scheduleID, trigger, status, detail, at, finished))
		return err
	})
	return run, err
}

// SkipRun records a firing that did not run, with the reason.
func (s *Store) SkipRun(ctx context.Context, scheduleID, trigger, detail string, at time.Time) (Run, error) {
	return scanRun(s.db.QueryRow(ctx, `INSERT INTO schedule_runs (schedule_id, trigger, status, detail, started_at, finished_at)
		VALUES ($1, $2, 'skipped', $3, $4, $4) RETURNING `+runCols, scheduleID, trigger, detail, at))
}

// StartScheduledSession links a new session to its schedule and its run.
func (s *Store) StartScheduledSession(ctx context.Context, runID int64, scheduleID, sessionID string) error {
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE sessions SET schedule_id = $2 WHERE id = $1`, sessionID, scheduleID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE schedule_runs SET session_id = $2 WHERE id = $1`, runID, sessionID)
		return err
	})
}

func (s *Store) FinishRun(ctx context.Context, runID int64, status, detail string, at time.Time) error {
	_, err := s.db.Exec(ctx, `UPDATE schedule_runs SET status = $2, detail = $3, finished_at = $4 WHERE id = $1`, runID, status, detail, at)
	return err
}

// FailInterruptedRuns marks runs left running by a previous Bob process as failed, so they do
// not block their schedules forever.
func (s *Store) FailInterruptedRuns(ctx context.Context, at time.Time) error {
	_, err := s.db.Exec(ctx, `UPDATE schedule_runs SET status = 'failed', detail = 'Bob stopped during the run', finished_at = $1
		WHERE status = 'running'`, at)
	return err
}

// Runs lists a schedule's runs, newest first.
func (s *Store) Runs(ctx context.Context, scheduleID string, limit int) ([]Run, error) {
	rows, err := s.db.Query(ctx, `SELECT `+runCols+` FROM schedule_runs WHERE schedule_id = $1 ORDER BY id DESC LIMIT $2`, scheduleID, limit)
	if err != nil {
		return nil, err
	}
	return collect(rows, scanRun)
}

// LastCronRun is when cron last fired the schedule (ran or skipped); zero if never.
func (s *Store) LastCronRun(ctx context.Context, scheduleID string) (time.Time, error) {
	var t *time.Time
	err := s.db.QueryRow(ctx, `SELECT max(started_at) FROM schedule_runs WHERE schedule_id = $1 AND trigger = 'cron'`, scheduleID).Scan(&t)
	if err != nil || t == nil {
		return time.Time{}, err
	}
	return *t, nil
}

// ExpiredScheduledSessions lists a schedule's sessions beyond its newest keep, oldest first.
func (s *Store) ExpiredScheduledSessions(ctx context.Context, scheduleID string, keep int) ([]string, error) {
	rows, err := s.db.Query(ctx, `SELECT id FROM sessions WHERE schedule_id = $1 ORDER BY created_at DESC, id OFFSET $2`, scheduleID, keep)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(r pgx.Row) (string, error) {
		var id string
		return id, r.Scan(&id)
	})
}

// SchedulesPaused reports the Bob-wide switch that stops every schedule firing.
func (s *Store) SchedulesPaused(ctx context.Context) (bool, error) {
	var paused bool
	err := s.db.QueryRow(ctx, `SELECT value::boolean FROM settings WHERE key = 'schedules_paused'`).Scan(&paused)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return paused, err
}

func (s *Store) SetSchedulesPaused(ctx context.Context, paused bool) error {
	v, _ := json.Marshal(paused)
	_, err := s.db.Exec(ctx, `INSERT INTO settings (key, value) VALUES ('schedules_paused', $1)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, v)
	return err
}

func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch {
		case i == 8 || i == 13 || i == 18 || i == 23:
			if c != '-' {
				return false
			}
		case !('0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'):
			return false
		}
	}
	return true
}
