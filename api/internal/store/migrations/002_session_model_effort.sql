-- Per-session overrides of the worker's model and effort. Empty = use the worker's setting.
ALTER TABLE sessions ADD COLUMN model  text NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN effort text NOT NULL DEFAULT ''
  CHECK (effort IN ('', 'low', 'medium', 'high', 'xhigh', 'max'));
