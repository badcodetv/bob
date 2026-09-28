-- Scheduled runs queue per project (T20): a firing records a queued run, and a dispatcher starts a
-- project's oldest queued run only when that project has no running scheduled run.
ALTER TABLE schedule_runs DROP CONSTRAINT schedule_runs_status_check;
ALTER TABLE schedule_runs ADD CONSTRAINT schedule_runs_status_check
  CHECK (status IN ('queued', 'running', 'ok', 'failed', 'skipped'));
-- The dispatcher looks for a project's running and oldest queued runs.
CREATE INDEX schedule_runs_active ON schedule_runs (status, id) WHERE status IN ('queued', 'running');
