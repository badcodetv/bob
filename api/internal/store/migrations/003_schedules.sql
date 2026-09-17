-- A schedule starts a new session on a worker, with a message, when its cron expression fires.
CREATE TABLE schedules (
  id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  project        text NOT NULL REFERENCES projects(name) ON DELETE CASCADE,
  name           text NOT NULL CHECK (name ~ '^[a-z0-9][a-z0-9-]{0,40}$'),
  worker         text NOT NULL,
  cron           text NOT NULL,                  -- 5-field cron
  timezone       text NOT NULL DEFAULT 'UTC',
  message        text NOT NULL,                  -- the first message sent to the new session
  enabled        boolean NOT NULL DEFAULT true,
  keep_sessions  integer NOT NULL DEFAULT 30 CHECK (keep_sessions >= 1),
  created_at     timestamptz NOT NULL DEFAULT now(),
  UNIQUE (project, name)
);

-- Every firing, manual run and skip, in order. At most one run per schedule is 'running'.
CREATE TABLE schedule_runs (
  id           bigserial PRIMARY KEY,
  schedule_id  uuid NOT NULL REFERENCES schedules(id) ON DELETE CASCADE,
  session_id   uuid REFERENCES sessions(id) ON DELETE SET NULL,
  trigger      text NOT NULL CHECK (trigger IN ('cron', 'manual')),
  status       text NOT NULL CHECK (status IN ('running', 'ok', 'failed', 'skipped')),
  detail       text NOT NULL DEFAULT '',
  started_at   timestamptz NOT NULL DEFAULT now(),
  finished_at  timestamptz
);
CREATE INDEX schedule_runs_schedule ON schedule_runs (schedule_id, id DESC);

-- A session started by a schedule; kept when the schedule is deleted.
ALTER TABLE sessions ADD COLUMN schedule_id uuid REFERENCES schedules(id) ON DELETE SET NULL;

-- Bob-wide switches, e.g. schedules_paused.
CREATE TABLE settings (
  key    text PRIMARY KEY,
  value  jsonb NOT NULL
);
