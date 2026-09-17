-- When a schedule was last turned on or had its timing changed: firings before then were not
-- asked for, so they are never caught up.
ALTER TABLE schedules ADD COLUMN changed_at timestamptz NOT NULL DEFAULT now();
