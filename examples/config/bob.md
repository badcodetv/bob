---
files_root: ''
---
This project is Bob's own example: one assistant worker and one skill, enough to check that a
Bob can reach a repository, read its config folder and hold a conversation.

The body of this file is appended to every worker's system prompt, before the worker's own. It is
where a project says what it is, and how its workers are meant to work together — so that lives in
git, written by the project, rather than in Bob.

## Labelling scheme

Workers coordinate through labelled memories. This project has nothing to coordinate yet, so it
sets no labels. A project that does would name them here, for example:

- `kind=note|decision|data` — what sort of memory this is
- `topic=<slug>` — the thing it is about
