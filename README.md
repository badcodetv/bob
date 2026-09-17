# Bob

Runs AI agent sessions for BadCode: one container per project, the labs' own harnesses inside it,
configuration from git, conversations in Postgres. Read [DESIGN.md](DESIGN.md) first.

```
api/        Go API — sign-in, projects, sessions, turns, stored events, Docker control
runtime/    bob-runtime image — the small server inside each project container
web/        the web app — assistant-ui chat, projects, workers
examples/   an example project config folder (workers/, skills/)
scripts/    import-agent-bob-env, dev-api
```

## A project's config folder

A project points at a git repository, a branch and a subfolder:

```
workers/<name>.md     front matter: engine (claude|codex|opencode), model, effort, tools
                      body: the worker's system prompt
skills/<name>/SKILL.md
```

## Run it locally

```sh
scripts/import-agent-bob-env          # once: reuse agent-bob's .env values (never printed)
docker compose up -d postgres
(cd runtime && npm ci && docker build -t bob-runtime:dev .)
scripts/dev-api                       # API on :8090
(cd web && npm ci && npm run dev)     # UI on http://localhost:8080 (proxies /api)
```

Sign in with Google (an account in `BOB_PROJECT_MAP`), create a project pointing at a git
repository and subfolder, and pick a worker to start a chat. Push a change to the repository and
press **Sync git** to pick it up — no restart. This repository's own `examples/config` works as a
first project: `https://github.com/badcodetv/bob`, branch `main`, subfolder `examples/config`.

Each chat works in its own git worktree of the project's repository (`/project/work/<session>`,
branch `bob/<session>`), so it can read and change the code without affecting other chats.
Deleting the chat removes the worktree and the branch. Nothing is pushed.

A turn knows who it is for. Its tools see `BOB_USER_EMAIL` and `BOB_USER_NAME` (the signed-in
person; a scheduled turn has `schedule:<schedule id>` and the schedule's name), and a commit made
during the turn is authored by that person and committed by `Bob <bob@badcode.tv>`. These are set
by Bob, not taken from the conversation, so the agent cannot be talked into another name.

Private config repositories are cloned with `GITHUB_TOKEN`. For a local repository during
development, create the project over the API with `"repo_url": "file:///seed"` and
`"repo_mount": "/abs/path/to/repo"`.

A project's **Settings** (sidebar) change its repository, branch, subfolder and image, or delete
it — deleting removes the container, the volume and every chat. `POST /api/projects/<name>/restart`
recreates a project's container (keeping its volume, and so
every session) — needed after changing the project's own settings or passed-in credentials.

## Who can use what

`BOB_PROJECT_MAP` (or a file named by `BOB_PROJECT_MAP_FILE`; the inline one wins) lists who may
sign in and which projects each person uses:

```json
{"kai@example.com": ["*"], "tester@example.com": ["wolf"]}
```

`"*"` is an **admin**: every project, and creating, changing and deleting projects (and schedules).
Anyone else sees only their listed projects; they can chat, view files and run schedules there,
and every other project's routes answer 404, as if it did not exist. Bob refuses to start on a map
that is not valid JSON, lists nobody, or has a non-string entry. The old `BOB_ALLOWED_EMAILS`
still works when no map is set, making each email an admin, and logs that it is deprecated.
`scripts/import-agent-bob-env` copies the users of agent-bob's `AGENTKIT_PROJECT_MAP`.

## API

Admin only: creating projects, `PATCH`/`DELETE` a project, restart. Everything else: anyone who
may use that project.

| | |
| --- | --- |
| `GET /api/config` · `POST /api/login` · `POST /api/logout` | Google sign-in, and who is signed in (`email`, `admin`); everything else needs the session cookie |
| `GET/POST /api/projects` | list (only the projects you may use), create |
| `GET/PATCH /api/projects/{p}` | read; change `{repo_url, repo_ref, subfolder, image}` (recreates the container, keeps the volume) |
| `DELETE /api/projects/{p}` | remove its container **and volume**, its chats and their events |
| `POST /api/projects/{p}/restart` | recreate the container, keep the volume |
| `GET /api/projects/{p}/workers` | workers read from git, and the last git sync |
| `POST /api/projects/{p}/sync` | pull the config folder again, then list workers |
| `GET/POST /api/projects/{p}/sessions` | list, create `{worker, model?, effort?}` |
| `GET /api/sessions/{id}` | one session |
| `PATCH /api/sessions/{id}` | `{model, effort}` for the next turn; empty = the worker's setting |
| `DELETE /api/sessions/{id}` | stop it, remove its worktree and branch, delete it and its events |
| `POST /api/sessions/{id}/messages` | `{text}` → 202; the turn runs in the background |
| `POST /api/sessions/{id}/interrupt` | stop the running turn |
| `GET /api/sessions/{id}/events?after=` | stored events |
| `GET /api/sessions/{id}/stream` | SSE: stored events, then live ones (token deltas are live-only) |

Events are stored exactly as the harness emitted them, with `engine` and `kind` beside the
payload. Bob's own events are `bob.user_message`, `bob.turn_done` and `bob.turn_failed`.

## Tests

```sh
(cd api && go vet ./... && go test ./...)
(cd runtime && npx tsc -p . && npm test)
(cd web && npx tsc -b && npx vite build)
```
