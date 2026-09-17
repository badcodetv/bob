-- A project is a git folder of configuration plus one container to run it in.
CREATE TABLE projects (
  name        text PRIMARY KEY CHECK (name ~ '^[a-z0-9][a-z0-9-]{0,40}$'),
  repo_url    text NOT NULL DEFAULT '',
  repo_ref    text NOT NULL DEFAULT 'main',
  subfolder   text NOT NULL DEFAULT '',
  image       text NOT NULL DEFAULT '',
  -- Dev only: a host path bind-mounted read-only at /seed (use repo_url file:///seed).
  repo_mount  text NOT NULL DEFAULT '',
  created_at  timestamptz NOT NULL DEFAULT now()
);

-- A session is one conversation with one worker. Its engine never changes.
CREATE TABLE sessions (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  project             text NOT NULL REFERENCES projects(name) ON DELETE CASCADE,
  worker              text NOT NULL,
  engine              text NOT NULL CHECK (engine IN ('claude', 'codex', 'opencode')),
  harness_session_id  text NOT NULL DEFAULT '',
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
