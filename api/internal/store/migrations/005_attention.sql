-- Human attention (T23): a chat asking a person to look at it. Open until a person replies in the
-- chat (answered) or dismisses the request (dismissed).
CREATE TABLE attention_requests (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  project text NOT NULL REFERENCES projects(name),
  session_id uuid NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  worker text NOT NULL,
  message text NOT NULL,
  kind text NOT NULL CHECK (kind IN ('ask','notice')),
  created_at timestamptz NOT NULL DEFAULT now(),
  closed_at timestamptz,
  closed_by text,
  close_reason text CHECK (close_reason IN ('answered','dismissed'))
);
CREATE INDEX attention_open ON attention_requests (project, created_at) WHERE closed_at IS NULL;
