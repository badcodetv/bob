-- Who wrote a memory: the person whose message the chat was working on (their email), a schedule
-- ("schedule:<id>"), or an outside client holding a project's MCP key ("key:<project>", with the
-- client's own label in brackets when it sends one). '' for memories written before this column.
ALTER TABLE memories ADD COLUMN created_by text NOT NULL DEFAULT '';
