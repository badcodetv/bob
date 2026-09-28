-- Memories (T21): append-only, labelled, per project, searched by keyword and by vector. The
-- extension needs a superuser the first time; store.Open says so before this runs.
CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE memories (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  project text NOT NULL REFERENCES projects(name),
  labels jsonb NOT NULL DEFAULT '{}',
  content text NOT NULL,
  content_tsv tsvector GENERATED ALWAYS AS (to_tsvector('english', content)) STORED,
  embedding vector(1536) NOT NULL,
  created_by_worker text NOT NULL DEFAULT '',
  created_by_session uuid REFERENCES sessions(id) ON DELETE SET NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX memories_project ON memories (project, created_at DESC, id DESC);
CREATE INDEX memories_labels ON memories USING gin (labels);
CREATE INDEX memories_content_tsv ON memories USING gin (content_tsv);
CREATE INDEX memories_embedding ON memories USING hnsw (embedding vector_cosine_ops);
-- A memory labelled retracts=<id> withdraws that memory from search (compared as id::text).
CREATE INDEX memories_retracts ON memories (project, (labels->>'retracts')) WHERE labels ? 'retracts';
