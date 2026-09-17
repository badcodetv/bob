-- The folder of the repository the Files page opens on, e.g. "site". Empty = the repository root.
ALTER TABLE projects ADD COLUMN files_root text NOT NULL DEFAULT '';
