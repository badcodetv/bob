-- Projects are deployment configuration now, read from a file at boot, not created over the API.
-- A project that leaves the file is marked absent rather than deleted: its sessions and events are
-- history, and sessions.project / schedules.project are foreign keys to this table.
ALTER TABLE projects ADD COLUMN absent_at timestamptz;
