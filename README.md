# Bob

Runs AI agent sessions for BadCode: one container per project, the labs' own harnesses inside it,
configuration from git, conversations in Postgres. Read [DESIGN.md](DESIGN.md) first.

Bob does not start containers and has no Docker socket. Each project's container is declared in
the deploy's compose file and Bob talks to it over HTTP, by name. So **a project is deployment
configuration**: adding one, or changing one of its secrets, is a deploy.

```
api/          Go API — sign-in, projects, sessions, turns, stored events
runtime/      bob-runtime image — the small server inside each project container
web/          the web app — assistant-ui chat, projects, workers
examples/     an example project config folder (bob.md, workers/, skills/)
scripts/      import-agent-bob-env, compose-projects
projects.dev.yaml   the projects a local Bob serves
```

## Which projects exist

`BOB_PROJECTS_FILE` names a YAML file listing them. Bob reads it at boot and brings its `projects`
table into step; `scripts/compose-projects.mjs` turns the same file into a compose file with one
service per project, so the two can never disagree about which projects there are.

```yaml
projects:
  - name: wolf                                 # also the container and volume name, so it can
    repo: https://github.com/badcodetv/wolf    #   never change
    ref: main
    config_dir: bob                            # holds bob.md, workers/, skills/
    pass: [CLAUDE_CODE_OAUTH_TOKEN, GITHUB_TOKEN, FRED_API_KEY]   # what this project may see
```

A project you remove from the file is **marked absent, not deleted**: its chats and events stay in
Postgres, its routes 404, and its schedules stop firing. Putting the name back brings it and its
history back — which is why a name must never be reused for something else.

There is no way to create, change or delete a project over the API. The **Settings** dialog shows
what this Bob is running, read-only.

## A project's config folder

A project points at a git repository, a branch and a subfolder:

```
bob.md                front matter: files_root, default_model
                      body: what this project is, and its memory labelling scheme —
                            appended to every worker's system prompt, before the worker's own
workers/<name>.md     front matter: engine (claude|codex|opencode), model, effort, tools
                      body: the worker's system prompt
skills/<name>/SKILL.md
```

`bob.md` is read on every turn, so pushing a change and pressing **Sync git** takes effect on the
next message. A project without one is fine; it simply has no shared preamble.

## Run it locally

```sh
scripts/import-agent-bob-env          # once: reuse agent-bob's .env values (never printed)
./stack build                         # runtime image, web packages, API builds
./stack start                         # Postgres, a container per project, API :8090, UI :8080
```

`./stack` on its own lists the rest: `stop`, `restart`, `status`, `logs`, `psql`, `sql`, `test`,
`containers` and `clean` (removes project containers and their volumes). It runs the API and the
web app on the host; Postgres and the project containers run in Compose.

The projects are [projects.dev.yaml](projects.dev.yaml), from which `./stack` generates
`.stack/projects.yml`. Each project publishes its runtime port on `127.0.0.1`, because a Bob
running on the host cannot resolve container names; `BOB_RUNTIME_HOSTS` tells Bob where they are.
**Adding a project locally means editing that file and `./stack restart`.**

⚠️ Never run `docker compose down -v` here. `-v` deletes volumes, and a project's volume holds
every chat's worktree and harness state. The generated file declares them `external` precisely so
that cannot happen through it — but `-v` on another compose file in this directory still can.

Sign in with Google (an account in `BOB_PROJECT_MAP`) and pick a worker to start a chat. Push a
change to the project's repository and press **Sync git** to pick it up — no restart.

Bob talks to each project's container with a password of its own, so a container cannot drive
another project's runtime over the Docker network. Nothing writes that password down: Bob and the
container each derive it from `BOB_RUNTIME_KEY` and the project's name (`runtimeToken` in
`runtime/src/auth.ts`, `runtime.Token` in Go — `auth.test.ts` pins them to the same fixed vector).
The runtime removes the key from the environment its harnesses and tools run in. Changing
`BOB_RUNTIME_KEY` means regenerating the compose file and recreating every project container.

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

A project's secrets are **environment variables on its compose service** — an API key, a GitHub
token that may push. Which variables a project may see is its `pass` list in the projects file;
the generated compose file names each one as `${NAME}` and compose resolves it from the `.env`
beside it, so the compose file itself holds no secret and is safe to read, diff and commit.

Changing a secret is a deploy: edit the ops `.env`, regenerate, `docker compose up -d` that
service. Only that project's container restarts.

There is no encrypted secrets table, no `BOB_SECRETS_KEY` and no editor in the web app. There was,
until 2026-09-21; since adding a project is a deploy, making a secret one too removed a whole
subsystem and a key that could permanently destroy data if lost.

Do not set names Bob or the image control on a project service — `BOB_*`, `GIT_*`, `PATH`, `HOME`,
`CLAUDE_CONFIG_DIR`, `CODEX_HOME`. A worker can read its project's secrets (that is the point), so
it could also repeat one in a chat.

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
opening on `files_root` from the project's `bob.md` (e.g. `site`) and its `index.html` when there is
one. **Refresh** pulls from git and reloads the page; **Open in new tab** opens the file on its
own, still sandboxed. Changing `files_root` needs only a git sync.

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

Admin only: `GET /api/busy`, creating, changing and deleting schedules, and settings. Everything
else: anyone who may use that project. Projects are not created, changed or deleted over the API —
they come from the projects file (see **Which projects exist**).

| | |
| --- | --- |
| `GET /api/config` · `POST /api/login` · `POST /api/logout` | Google sign-in, and who is signed in (`email`, `admin`); everything else needs the session cookie |
| `GET /api/projects` | the projects you may use |
| `GET /api/projects/{p}` | one project's settings, as the projects file gave them (read-only) |
| `GET /api/projects/{p}/workers` | workers and `bob.md` settings read from git, and the last git sync |
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
| `GET /api/busy` | the projects with a turn, scheduled run or sync in progress — a deploy script waits on this (admin) |
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

The compose file has Postgres, Bob, and **one service per project**, generated from the same
`projects.yaml` Bob reads:

```sh
scripts/compose-projects.mjs projects.yaml compose.projects.yml \
  --image '<registry>/runtime:${TAG}' --network bob-projects
docker compose up -d                  # compose.yml includes compose.projects.yml
```

Every project service is on a network Bob also joins, publishes no ports, and carries its own
repository settings, its `pass` variables as `${NAME}`, `BOB_RUNTIME_KEY`, `BOB_PROJECT_NAME`, its
limits (`mem_limit`, `pids_limit`) and a `stop_grace_period` long enough for a turn in flight to
finish — the runtime drains on SIGTERM, and Docker SIGKILLs it when that runs out. Bob needs
`BOB_PROJECTS_FILE` and `BOB_RUNTIME_KEY`, and **no Docker socket**. `GET /healthz` answers 200
when the database does.

BadCode's own deployment is `apps/bob/` in the private ops repository.

A project's volume is declared `external`, so:

- create it before the first `up` — `docker volume create bob-project-<name>` — because compose
  will not create an external volume;
- `docker compose down -v` cannot delete it. Never use `-v` anyway.

**Adding a project:** add it to `projects.yaml`, regenerate, `docker compose up -d`. Only the new
service starts. **Changing a secret:** edit the ops `.env`, regenerate, `up -d` that one service —
only that project restarts. Check `GET /api/busy` first: restarting a container cuts off a running
turn, and a scheduled run cut off that way is recorded failed and not retried.

**Backups — three things, not one.** Postgres holds the record (conversations, schedules, memory):
back it up properly, off the box. Each `bob-project-<name>` volume holds that project's checkout,
worktrees and harness state — a cache by design, but restoring one means restoring to the *same
path* with `tar --numeric-owner`, because git worktrees store absolute paths. `projects.yaml` and
the ops `.env` reproduce the deployment; keep them in the ops repository.

⚠️ **Never rename a project.** Its name is the container name, the volume name and the
`projects.name` primary key, and its worktrees hold absolute paths.

## Tests

```sh
(cd api && go vet ./... && go test ./...)
# schedule tests need a Postgres where the user can create databases; each test makes its own:
(cd api && BOB_TEST_DATABASE_URL=postgres://bob:bob@127.0.0.1:5433/bob go test ./cmd/bob)
(cd runtime && npx tsc -p . && npm test)
(cd web && npx tsc -b && npx vite build)
```
