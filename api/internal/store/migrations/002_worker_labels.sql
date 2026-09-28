-- Workers can carry labels (key/value pairs) for grouping and selection (T18).
ALTER TABLE workers ADD COLUMN labels jsonb NOT NULL DEFAULT '{}';
CREATE INDEX workers_labels ON workers USING gin (labels);
