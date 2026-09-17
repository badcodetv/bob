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

## Files

`GET /api/projects/<p>/files/<path>` reads the project's **synced checkout** (what the last git
sync fetched, not a chat's worktree): a file, or a directory as `{entries:[{name,type,size}]}`.
Paths that leave the checkout — `..` plain or encoded, symlinks pointing out — and `.git` are 404.

Agents write these files, and an agent can be talked into writing a hostile page, so every
response carries a fixed policy: `Content-Security-Policy: sandbox; default-src 'none'` (plus
images, styles and fonts from Bob itself), `nosniff`, `no-referrer`, `no-store`. The page runs no
JavaScript and has no origin of its own, so it cannot use your sign-in, even opened directly in a
tab. Pages therefore use static HTML, CSS, SVG and plain links.

A sandboxed page's own images and stylesheets are requested without your cookie, so pages are
viewed through a **viewer link**: `POST /api/projects/<p>/view` returns a base like
`/api/view/<token>/`, under which the project's files load for 12 hours with no cookie. The token
names the project and you, grants reading that project's files only, and is checked against the
project map on every request. `scripts/check-file-viewer.mjs` is the browser check.

## Schedules

A schedule starts a **new chat** on a worker with a fixed first message whenever its cron
expression fires (5 fields, in the schedule's timezone, e.g. `0 6 * * 1-5` in `Europe/London`).
Each run, in order: pull the project's git (a failed pull fails the run — it never runs
yesterday's prompt), check the worker exists, start the chat as `schedule:<id>`, wait for the
turn, then pull git again so anything the run pushed shows up. Every run is recorded with its
status (`running`, `ok`, `failed`, `skipped`) and its chat.

- **No overlap:** if the previous run is still going, the firing is recorded as skipped.
- **Missed firings** (Bob was down) run once if the latest was under 6 hours ago; older ones are
  recorded as skipped. When a local time happens twice as clocks go back, it fires once.
- **Old chats:** only the newest `keep_sessions` (default 30) of a schedule's chats are kept.
- **Pause everything:** `PATCH /api/settings {"schedules_paused": true}` (admin) stops every
  schedule firing; firings missed while paused follow the missed-firing rule on resume.
  **Run now** still works while paused or disabled, because a person asked for it.
- A run that was going when Bob stopped is marked failed when Bob starts again.

In the web app, **Schedules** (sidebar) lists each schedule with its timing in words, the next
firing, the last run and a link to its chat, **Run now**, and — for admins — turn on/off, edit,
delete and **Pause all schedules**. Chats a schedule started show its name with a clock.

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

Admin only: creating projects, `PATCH`/`DELETE` a project, restart, creating, changing and
deleting schedules, and settings. Everything else: anyone who may use that project.

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
| `GET /api/projects/{p}/files/{path}` | a file or directory listing from the synced checkout, sandboxed (see Files) |
| `POST /api/projects/{p}/view` · `GET /api/view/{token}/{path}` | a 12-hour viewer link, and files read through it without the cookie |
| `GET /api/projects/{p}/schedules` | schedules, each with `next_at` and `last_run`; and whether all are `paused` |
| `POST /api/projects/{p}/schedules` | `{name, worker, cron, timezone?, message, enabled?, keep_sessions?}` |
| `PATCH /api/schedules/{id}` · `DELETE /api/schedules/{id}` | change any of those fields; delete (its chats stay) |
| `POST /api/schedules/{id}/run` | run now → 202 with the run (`skipped` if one is still going) |
| `GET /api/schedules/{id}/runs` | the last 50 runs, newest first |
| `GET/PATCH /api/settings` | `{schedules_paused}` — the switch that pauses every schedule |

Events are stored exactly as the harness emitted them, with `engine` and `kind` beside the
payload. Bob's own events are `bob.user_message`, `bob.turn_done` and `bob.turn_failed`.

## Tests

```sh
(cd api && go vet ./... && go test ./...)
# schedule tests need a Postgres where the user can create databases; each test makes its own:
(cd api && BOB_TEST_DATABASE_URL=postgres://bob:bob@127.0.0.1:5433/bob go test ./cmd/bob)
(cd runtime && npx tsc -p . && npm test)
(cd web && npx tsc -b && npx vite build)
```
