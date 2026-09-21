-- files_root is project content now: it lives in bob.md's front matter, beside the workers, and
-- changes with a git sync instead of a deploy. Bob reads it from the runtime with the workers.
ALTER TABLE projects DROP COLUMN files_root;
