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
./stack build                         # runtime image, web packages, API builds
./stack start                         # Postgres, API on :8090, UI on http://localhost:8080
```

`./stack` on its own lists the rest: `stop`, `restart`, `status`, `logs`, `psql`, `sql`, `test`,
`containers` and `clean` (removes project containers and their volumes). It runs the API and the
web app on the host, with only Postgres in Compose.

Sign in with Google (an account in `BOB_PROJECT_MAP`), create a project pointing at a git
repository and subfolder, and pick a worker to start a chat. Push a change to the repository and
press **Sync git** to pick it up — no restart. This repository's own `examples/config` works as a
first project: `https://github.com/badcodetv/bob`, branch `main`, subfolder `examples/config`.

Bob talks to each project's container with a password of its own (`BOB_RUNTIME_TOKEN`, derived
from `BOB_SESSION_SECRET`), because containers can reach each other over Docker's network. The
runtime refuses any request without it and removes it from the environment its harnesses and
tools run in. Changing `BOB_SESSION_SECRET` means restarting every project.

Each chat works in its own git worktree of the project's repository (`/project/work/<session>`,
branch `bob/<session>`), so it can read and change the code without affecting other chats.
Deleting the chat removes the worktree and the branch.

**Pushing work.** A chat's commits stay on its own branch until it publishes them. The image has
`bob-push [ref]` for that: `git pull --rebase origin <ref>`, then `git push origin HEAD:<ref>`,
pulling and retrying up to 3 times when another chat pushed first. A rebase conflict stops it
and names the files; after fixing them and `git add`, running `bob-push` again finishes the
rebase and pushes. Tell workers to use it (or run those two git commands themselves). Git reaches
`github.com` with `GITHUB_TOKEN`, read from the environment by a credential helper each time git
asks — the token is never written to a file, and never sent to other hosts. To push, the token
needs **Contents: read and write** on the repository (a fine-grained token; a project can have its
own as a secret). The token imported from agent-bob can read but was refused a push to
`badcodetv/bob` on 2026-09-17, so a pushing project needs a new one.

A turn knows who it is for. Its tools see `BOB_USER_EMAIL` and `BOB_USER_NAME` (the signed-in
person; a scheduled turn has `schedule:<schedule id>` and the schedule's name), and a commit made
during the turn is authored by that person and committed by `Bob <bob@badcode.tv>`. These are set
by Bob, not taken from the conversation. They are a courtesy, not proof: the agent's own tools can
change environment variables and git authors. The record of who sent each message is Bob's
`bob.user_message` event (`user_email`, `user_name`), written outside the container.

Private repositories on github.com are cloned with `GITHUB_TOKEN`. For a local repository during
development, create the project over the API with `"repo_url": "file:///seed"` and
`"repo_mount": "/abs/path/to/repo"`.

A project's **Settings** (sidebar) change its repository, branch, subfolder and image, or delete
it — deleting removes the container, the volume and every chat. `POST /api/projects/<name>/restart`
recreates a project's container (keeping its volume, and so
every session) — needed after changing the project's own settings or passed-in credentials.

## Secrets

A project's **Settings** hold its secrets: environment variables for that project's container
only (an API key, a GitHub token that may push). A secret replaces a `BOB_PASS_ENV` variable of
the same name, so one project can have its own `GITHUB_TOKEN`. Values are encrypted with
AES-256-GCM under `BOB_SECRETS_KEY` (32 random bytes, base64; `scripts/import-agent-bob-env`
generates one) and bound to their project and name. They are never logged and never sent to the
browser: the API lists names, who set them and when. Saving or deleting one restarts the
project's container — unless work is running there (a chat turn, a scheduled run, a git sync),
in which case the change shows as pending and is applied the moment that work ends. Names Bob or the image set (`BOB_*`, `GIT_*`, `PATH`, `HOME`, …) are refused. Without
`BOB_SECRETS_KEY` secrets are off, and a project that has some will not start.

Keep `BOB_SECRETS_KEY` safe: losing it makes stored secrets unreadable. A worker can read its
project's secrets (that is the point), so it could also repeat one in a chat.

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

In the web app, **Files** (sidebar) browses the checkout and shows a file in a sandboxed frame,
opening on the project's **Files folder** setting (e.g. `site`) and its `index.html` when there is
one. **Refresh** pulls from git and reloads the page; **Open in new tab** opens the file on its
own, still sandboxed. Changing the Files folder does not restart the project's container.

## Schedules

A schedule starts a **new chat** on a worker with a fixed first message whenever its cron
expression fires (5 fields, in the schedule's timezone, e.g. `0 6 * * 1-5` in `Europe/London`).
Each run, in order: pull the project's git (a failed pull fails the run — it never runs
yesterday's prompt), check the worker exists, start the chat as `schedule:<id>`, wait for the
turn, then pull git again so anything the run pushed shows up. Every run is recorded with its
status (`running`, `ok`, `failed`, `skipped`) and its chat.

- **No overlap:** if the previous run is still going, the firing is recorded as skipped.
- **Missed firings** (Bob was down) run once if the latest was under 6 hours ago; older ones are
  recorded as skipped. Firings from before a schedule was turned on, or had its timing changed,
  are never run. When a local time happens twice as clocks go back, it fires once.
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

Admin only: creating projects, `PATCH`/`DELETE` a project, restart, secrets, creating, changing
and deleting schedules, and settings. Everything else: anyone who may use that project.

| | |
| --- | --- |
| `GET /api/config` · `POST /api/login` · `POST /api/logout` | Google sign-in, and who is signed in (`email`, `admin`); everything else needs the session cookie |
| `GET/POST /api/projects` | list (only the projects you may use), create |
| `GET/PATCH /api/projects/{p}` | read; change `{repo_url, repo_ref, subfolder, image, files_root}` (the first four recreate the container, keeping the volume) |
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
| `GET /api/projects/{p}/secrets` | names, `updated_by`, `updated_at` — never values (admin) |
| `PUT /api/projects/{p}/secrets/{NAME}` · `DELETE …` | `{value}`; set or delete, then `{applied}`: whether the container restarted with it (admin) |
| `GET /api/projects/{p}/schedules` | schedules, each with `next_at` and `last_run`; and whether all are `paused` |
| `POST /api/projects/{p}/schedules` | `{name, worker, cron, timezone?, message, enabled?, keep_sessions?}` |
| `PATCH /api/schedules/{id}` · `DELETE /api/schedules/{id}` | change any of those fields; delete (its chats stay) |
| `POST /api/schedules/{id}/run` | run now → 202 with the run (`skipped` if one is still going) |
| `GET /api/schedules/{id}/runs` | the last 50 runs, newest first |
| `GET/PATCH /api/settings` | `{schedules_paused}` — the switch that pauses every schedule |

Events are stored exactly as the harness emitted them, with `engine` and `kind` beside the
payload. Bob's own events are `bob.user_message`, `bob.turn_done` and `bob.turn_failed`.

## Running it on a server

One image holds the API and the built web app (`Dockerfile`); project containers use
`runtime/Dockerfile`. `scripts/publish` builds both and pushes them to Artifact Registry tagged with
the commit. The server's compose file and secrets live in BadCode's private ops repository.

In a container, Bob needs the host's Docker socket (`/var/run/docker.sock`), a Docker network for
project containers that it also joins (`BOB_DOCKER_NETWORK`), and the runtime image already pulled
on the host (Bob does not pull images). Project containers publish no ports; Bob reaches them by
name on that network. `GET /healthz` answers 200 when the database does.

Limits for each project container: `BOB_PROJECT_MEMORY` (e.g. `8g`), `BOB_PROJECT_CPUS` (e.g. `4`),
`BOB_PROJECT_PIDS` (default 4096). When Bob starts, it replaces any project container whose settings
changed — a new runtime image, a rotated pass-through token, new limits — so the next turn gets a
fresh container on the same volume.

## Tests

```sh
(cd api && go vet ./... && go test ./...)
# schedule tests need a Postgres where the user can create databases; each test makes its own:
(cd api && BOB_TEST_DATABASE_URL=postgres://bob:bob@127.0.0.1:5433/bob go test ./cmd/bob)
(cd runtime && npx tsc -p . && npm test)
(cd web && npx tsc -b && npx vite build)
```
