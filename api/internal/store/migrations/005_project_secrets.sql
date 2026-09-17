-- A project's secrets, passed to its container as environment variables. Values are encrypted by
-- the API (AES-256-GCM under BOB_SECRETS_KEY) and never leave it except into that container.
CREATE TABLE project_secrets (
  project     text NOT NULL REFERENCES projects(name) ON DELETE CASCADE,
  name        text NOT NULL CHECK (name ~ '^[A-Z][A-Z0-9_]{0,63}$'),
  nonce       bytea NOT NULL,
  ciphertext  bytea NOT NULL,
  updated_by  text NOT NULL DEFAULT '',
  updated_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (project, name)
);
