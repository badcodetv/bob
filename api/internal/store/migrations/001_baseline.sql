-- The whole schema, from scratch (2026-09-28): Bob started again with no data, so the migrations
-- that built the old git-configured schema were replaced by this one file.

-- A project is one container, declared in the deploy's compose file and named in BOB_PROJECTS.
-- A project that leaves BOB_PROJECTS is marked absent rather than deleted: its sessions and events
-- are history, and sessions.project / schedules.project are foreign keys to this table.
-- prompt is the project prompt, part of every chat's system prompt, edited in Bob.
CREATE TABLE projects (
  name        text PRIMARY KEY CHECK (name ~ '^[a-z0-9][a-z0-9-]{0,40}$'),
  prompt      text NOT NULL DEFAULT '',
  created_at  timestamptz NOT NULL DEFAULT now(),
  absent_at   timestamptz
);

-- Every change to a project's prompt: what it became, who changed it and why.
CREATE TABLE project_prompt_versions (
  id          bigserial PRIMARY KEY,
  project     text NOT NULL REFERENCES projects(name),
  prompt      text NOT NULL,
  why         text NOT NULL,
  changed_by  text NOT NULL,
  changed_at  timestamptz NOT NULL DEFAULT now()
);

-- A worker is a named agent definition: which harness runs it, how, and its prompt. Its name never
-- changes; a "rename" is a new worker and the old one deleted.
CREATE TABLE workers (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  project     text NOT NULL REFERENCES projects(name),
  name        text NOT NULL CHECK (name ~ '^[a-z0-9][a-z0-9-]{0,40}$'),
  engine      text NOT NULL CHECK (engine IN ('claude', 'codex')),
  model       text NOT NULL DEFAULT '',
  effort      text NOT NULL DEFAULT '',
  tools       jsonb,                             -- null = the harness's default
  prompt      text NOT NULL DEFAULT '',
  created_by  text NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_by  text NOT NULL,
  updated_at  timestamptz NOT NULL DEFAULT now(),
  UNIQUE (project, name)
);

-- Every create, update and delete of a worker, with the whole worker as it was after the change
-- (for a delete, as it was before). No foreign key to workers: the history outlives a deleted
-- worker, and a worker re-created under the same name has a new id and a history of its own.
CREATE TABLE worker_versions (
  id          bigserial PRIMARY KEY,
  worker_id   uuid NOT NULL,
  project     text NOT NULL REFERENCES projects(name),
  name        text NOT NULL,
  action      text NOT NULL CHECK (action IN ('create', 'update', 'delete')),
  snapshot    jsonb NOT NULL,
  why         text NOT NULL,
  changed_by  text NOT NULL,
  changed_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX worker_versions_worker ON worker_versions (worker_id, id DESC);

-- A session is one conversation. Its engine never changes. worker is the worker's name when the
-- session was created, '' for a plain chat; worker_id is nulled when that worker is deleted, and
-- such a session "has lost its worker" (worker <> '' AND worker_id IS NULL): readable, but closed.
-- model and effort override the worker's settings; empty = the worker's.
CREATE TABLE sessions (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  project             text NOT NULL REFERENCES projects(name),
  worker_id           uuid REFERENCES workers(id) ON DELETE SET NULL,
  worker              text NOT NULL DEFAULT '',
  engine              text NOT NULL CHECK (engine IN ('claude', 'codex')),
  harness_session_id  text NOT NULL DEFAULT '',
  model               text NOT NULL DEFAULT '',
  effort              text NOT NULL DEFAULT ''
    CHECK (effort IN ('', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max')),
  created_at          timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX sessions_project ON sessions (project, created_at DESC);

-- Events are stored exactly as the harness emitted them. kind is the harness's own event type
-- (e.g. claude "assistant", "result"), or "bob.*" for the few events Bob itself writes.
CREATE TABLE events (
  id          bigserial PRIMARY KEY,
  session_id  uuid NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  engine      text NOT NULL,
  kind        text NOT NULL,
  payload     jsonb NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX events_session ON events (session_id, id);

-- A schedule starts a new session on a worker, with a message, when its cron expression fires.
-- Deleting the worker deletes its schedules.
CREATE TABLE schedules (
  id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  project        text NOT NULL REFERENCES projects(name) ON DELETE CASCADE,
  name           text NOT NULL CHECK (name ~ '^[a-z0-9][a-z0-9-]{0,40}$'),
  worker_id      uuid NOT NULL REFERENCES workers(id) ON DELETE CASCADE,
  cron           text NOT NULL,                  -- 5-field cron
  timezone       text NOT NULL DEFAULT 'UTC',
  message        text NOT NULL,                  -- the first message sent to the new session
  enabled        boolean NOT NULL DEFAULT true,
  keep_sessions  integer NOT NULL DEFAULT 30 CHECK (keep_sessions >= 1),
  created_at     timestamptz NOT NULL DEFAULT now(),
  -- When it was last turned on or had its timing changed: firings before then were not asked
  -- for, so they are never caught up.
  changed_at     timestamptz NOT NULL DEFAULT now(),
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

-- Bob-wide switches, e.g. schedules_paused.
CREATE TABLE settings (
  key    text PRIMARY KEY,
  value  jsonb NOT NULL
);

-- A session started by a schedule; kept when the schedule is deleted.
ALTER TABLE sessions ADD COLUMN schedule_id uuid REFERENCES schedules(id) ON DELETE SET NULL;
