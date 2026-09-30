# Bob

Runs AI agent sessions for BadCode: one container per project, the labs' own harnesses inside it,
workers and memory and conversations in Postgres, work in git. Read [DESIGN.md](DESIGN.md) first.

Bob does not start containers and has no Docker socket. Each project's container is declared in
the deploy's compose file and Bob talks to it over HTTP, by name. So **a project is deployment
configuration**: adding one, or changing one of its secrets, is a deploy.

```
api/          Go API — sign-in, projects, workers, sessions, turns, stored events, the MCP server
runtime/      bob-runtime image — the small server inside each project container (Claude, Codex)
web/          the web app — assistant-ui chat, workers, schedules, overview
deploy/       compose.yml (the box), compose.dev.yml (the laptop's "dev" project), env.example
scripts/      publish, drive-token, dev-api, check-file-viewer.mjs
stack         local development and `./stack publish` / `./stack deploy <tag>`
```

## Which projects exist

`BOB_PROJECTS` lists them, comma-separated (`enc,marketing,wolf`). Each name needs a container
declared in the same deploy's compose file and a `BOB_RUNTIME_TOKEN_<NAME>` (the name upper-cased,
`-` as `_`, 32+ characters: `openssl rand -hex 32`); compose gives the container that same value as
`BOB_RUNTIME_TOKEN`. A missing or short token stops the API at boot. The `projects` table is a
mirror the API reconciles at boot.

A project you remove from `BOB_PROJECTS` is **marked absent, not deleted**: its chats and events
stay in Postgres, its routes 404, and its schedules stop firing. Putting the name back brings it and
its history back — which is why a name must never be reused for something else.

There is no way to create, change or delete a project over the API.

## Workers and the project prompt

Configuration lives in Postgres, not git. A **worker** is an engine (`claude` or `codex`), a model,
an effort, tools, labels and a system prompt; each project also has a **project prompt** shared by
its workers. Every change to either is recorded as a version with who made it, when and a required
*why* — the web app's worker editor shows the history, and agents can do the same through the
`worker_*` MCP tools. Worker names never change (a "rename" is create + delete).

For each turn the API composes the system prompt — an environment note, the project prompt, then
the worker's prompt, joined by blank lines — and sends it to the runtime with the engine, model,
effort and tools. The runtime reads no configuration. A **plain chat** has no worker: pick an engine
(and model, effort) when you create it; it sees every Bob tool, including `worker_create`.

Deleting a worker deletes its schedules and leaves its chats readable, but they refuse new messages
(as does a chat whose worker's engine has since changed).

## Run it locally

```sh
cp deploy/env.example .env            # once: fill it in (see Environment below); never commit it
./stack build                         # runtime image, web packages, API builds
./stack start                         # Postgres, the "dev" project container, API :8070, UI :8080
```

`./stack` on its own lists the rest: `stop`, `restart`, `status`, `logs`, `psql`, `sql`, `test`,
`containers`, `clean` (removes project containers and their volumes), `publish` and `deploy`. It
runs the API and the web app on the host; Postgres (the shared local one from the ops repository,
`OPS_DIR` if it is not beside this repository) and the project container run in Compose.

The one local project is `dev` ([deploy/compose.dev.yml](deploy/compose.dev.yml)). Its runtime
publishes port 8100 on `127.0.0.1`, because a Bob running on the host cannot resolve container
names; `./stack start` sets `BOB_PROJECTS=dev` and `BOB_RUNTIME_HOSTS=dev=127.0.0.1:8100` for the
API. The container reaches the API at `http://host.docker.internal:8070`.

**Memory needs pgvector.** The API refuses to boot unless the `vector` extension exists in the
`bob` database, and the `bob` role cannot create it. As the Postgres superuser, once per database
(and again after any schema reset, which drops it):

```sh
docker compose -f ~/projects/badcode/ops/apps/postgres/compose.local.yml exec -T postgres \
  psql -U postgres -d bob -c 'CREATE EXTENSION IF NOT EXISTS vector'
```

The box needs the same, in its `bob` database, before the first deploy that includes memory.

Sign in with Google (an account in `BOB_PROJECT_MAP`), open `dev`, and start a chat.

⚠️ Never run `docker compose down -v` here. `-v` deletes volumes, and a project's volume holds the
work folder and harness state (including Codex's login). The compose files declare volumes
`external` precisely so that cannot happen through them — but `-v` on another compose file in this
directory still can.

Bob talks to each project's container with that project's runtime token (basic auth,
`bob:<token>`). The runtime deletes it from its own environment at startup, so nothing its
harnesses or tools run can read it. The other direction uses a different credential: each turn
carries a **per-chat MCP token** the API mints (`<session id>.<HMAC-SHA256 of the id, keyed by
BOB_SESSION_SECRET>`), which `/mcp` verifies and turns back into the chat and its project. An agent
can read its own chat's token — that only lets it act as the chat it already is.

**The work folder.** Every chat in a project shares `/project/work` on the volume; a chat clones
repositories one level below it (`/project/work/<repo>`) and commits and pushes them itself, so
other chats may be changing files there at the same time. Git reaches `github.com` with
`GITHUB_TOKEN`, read from the environment by a credential helper each time git asks — the token is
never written to a file, and never sent to other hosts. To push, the token needs **Contents: read
and write** on the repository (a fine-grained token, one per project on the box).

A turn knows who it is for. Its tools see `BOB_USER_EMAIL` and `BOB_USER_NAME` (the signed-in
person; a scheduled turn has `schedule:<schedule id>` and the schedule's name), and a commit made
during the turn is authored by that person and committed by `Bob <bob@badcode.tv>`. These are set
by Bob, not taken from the conversation. They are a courtesy, not proof: the agent's own tools can
change environment variables and git authors. The record of who sent each message is Bob's
`bob.user_message` event (`user_email`, `user_name`), written outside the container.

**Skills** are `/project/skills`, created at boot and symlinked into each harness's own skills
folder (`$CLAUDE_CONFIG_DIR/skills`, `$CODEX_HOME/skills`), so a skill a chat adds is seen by every
chat and both engines.

## Environment

Copy [deploy/env.example](deploy/env.example) to `.env` (laptop; `./stack` sources it) or
`.env.box` (production; `docker compose --env-file` reads it literally, so no shell quoting). Both
are git-ignored — this repository is public. The compose files only *name* variables.

**API** (listed in the header of `api/cmd/bob/main.go`; on the box each must also be named in the
`api` service of `deploy/compose.yml`, or the API never sees it):

| Variable | | |
| --- | --- | --- |
| `BOB_DATABASE_URL` | required | Postgres connection string |
| `BOB_PROJECTS` | required | the projects Bob serves, comma-separated |
| `BOB_RUNTIME_TOKEN_<NAME>` | one per project | that project's runtime password, 32+ characters |
| `BOB_PUBLIC_URL` | required | where people reach Bob, no trailing slash (`https://bob.box.badcode.tv`, `http://localhost:8080` locally); used for chat links in tool results and webhooks |
| `GOOGLE_CLIENT_ID` | required | Google sign-in |
| `BOB_SESSION_SECRET` | required | signs session cookies and each chat's MCP token |
| `BOB_PROJECT_MAP` / `_FILE` | required | who may sign in and which projects they use (see below) |
| `OPENAI_API_KEY` | required | embeddings for memory search |
| `BOB_EMBEDDING_MODEL` | optional | default `text-embedding-3-small`; changing it after memories exist worsens search over the old ones, which are not re-embedded |
| `BOB_DRIVE_CLIENT_ID`, `BOB_DRIVE_CLIENT_SECRET` | if any Drive token is set | the OAuth client that minted the tokens |
| `BOB_DRIVE_TOKEN_<NAME>` | optional | that project's Drive refresh token |
| `BOB_ATTENTION_WEBHOOK_<NAME>` | optional | URL Bob POSTs to when a chat of that project asks for a person |
| `BOB_ADDR` | optional | listen address, default `:8070` |
| `BOB_RUNTIME_HOSTS` | laptop only | `project=host:port,…` when Bob runs on the host and cannot resolve container names (`./stack start` sets it) |
| `BOB_WEB_DIR` | optional | serve the built web app from here (the API image sets it; dev uses Vite) |
| `BOB_ALLOWED_EMAILS` | deprecated | emails, each an admin, when no map is set |

**A project container** (`deploy/compose.yml`, `deploy/compose.dev.yml`; read by `runtime/src`):
`BOB_PROJECT_NAME`, `BOB_RUNTIME_TOKEN` and `BOB_API_URL` (`http://api:8070` on the box) are
required — the runtime refuses to serve without the token and the URL. `CLAUDE_CODE_OAUTH_TOKEN`
signs Claude in; `GITHUB_TOKEN` (on the box, from `GITHUB_TOKEN_<NAME>`) is git's credential; a
project may be given more (`FRED_API_KEY` for wolf). The image sets `PORT` (8080),
`BOB_PROJECT_DIR` (`/project`), `CLAUDE_CONFIG_DIR` and `CODEX_HOME` (both under `/project/.bob`);
leave them alone, along with `BOB_*` and `GIT_*`. A worker can read its project's secrets (that is
the point), so it could also repeat one in a chat.

## Signing engines in

**Claude** uses `CLAUDE_CODE_OAUTH_TOKEN` from the environment (`claude setup-token`). An
`ANTHROPIC_API_KEY` also works but is not the default.

**Codex** logs in once **per project**, by hand, and the login lives on the project's volume
(`$CODEX_HOME/auth.json`, `/project/.bob/codex`); Codex refreshes it itself, so nothing goes in
`.env`. Device-code login must be enabled in the ChatGPT account's security settings.

```sh
docker exec -it bob-project-<name> codex login --device-auth      # bob-project-dev locally
```

Until then a Codex chat fails with exactly that instruction. Wiping the volume (`./stack clean`)
loses the login.

**Google Drive** is read-only and per project. Create a **Desktop app** OAuth client in Google
Cloud with the Drive API enabled, and set its consent screen to **In production** — a client left
in *Testing* gets refresh tokens that expire after seven days. Put its id and secret in the env as
`BOB_DRIVE_CLIENT_ID` / `BOB_DRIVE_CLIENT_SECRET`, then mint one token per project, signed in as
the Google account whose Drive that project should see:

```sh
BOB_DRIVE_CLIENT_ID=… BOB_DRIVE_CLIENT_SECRET=… node scripts/drive-token
```

It opens a browser (loopback redirect, PKCE, scope `drive.readonly`) and prints the refresh token
and the account's email; store it as `BOB_DRIVE_TOKEN_<NAME>`. A project with no token has no
`drive_*` tools. A revoked token makes the tools say so.

## Bob's tools

Every chat is given Bob's MCP server, `/mcp` on the API (JSON-RPC over streamable HTTP, scoped to
the calling chat's project): `worker_create/update/list/delete`, `schedule_create/update/list/delete`
(agents may manage schedules even though the HTTP schedule routes are admin-only),
`memory_create/search/get/current`, `drive_search/list/read/fetch` (when the project has a Drive
token) and `request_human_attention`.

**Memory** is labelled text with hybrid search: label selectors, Postgres full-text and pgvector
embeddings (OpenAI, 1536 dimensions) fused by Reciprocal Rank Fusion. Memories are append-only:
there is no update or delete. A newer memory supersedes an older one (`memory_current` returns the
newest labelled with a given name) and a `retracts=<id>` memory withdraws one. The Overview lists
the project's recent memories.

**Human attention.** `request_human_attention` stores a request and ends the turn. The chat and
the project get a dot in the sidebar, the Overview has a **Needs you** list, and, when
`BOB_ATTENTION_WEBHOOK_<NAME>` is set, Bob POSTs `{project, worker, kind, message, chat_url}` to it.
A request closes when a person replies in that chat or presses **Dismiss**. There is no expiry.

**Secrets.** A project's secrets are **environment variables on its compose service**. Changing one
is a deploy: edit `.env.box`, `./stack deploy <tag>`. Only the containers whose environment changed
are recreated. There is no encrypted secrets table and no editor in the web app.

## Files

`GET /api/projects/<p>/files/<path>` reads the project's **work folder** (`/project/work`, shared
by every chat): a file, or a directory as `{entries:[{name,type,size}]}`. Paths that leave the
folder — `..` plain or encoded, symlinks pointing out — and `.git` are 404.

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

In the web app, **Files** (sidebar) browses the work folder and shows a file in a sandboxed frame.
**Refresh** reloads the listing; **Open in new tab** opens the file on its own, still sandboxed.

## Schedules

A schedule starts a **new chat** on a worker with a fixed first message whenever its cron
expression fires (5 fields, in the schedule's timezone, e.g. `0 6 * * 1-5` in `Europe/London`).
Each run, in order: check the worker exists, start the chat as `schedule:<id>`, and wait for the
turn. Every run is recorded with its status (`queued`, `running`, `ok`, `failed`, `skipped`) and
its chat.

- **One at a time per project:** a firing records a `queued` run, and a project's oldest queued run
  starts only when that project has no scheduled run going. A schedule that already has a run
  queued or running records the new firing as `skipped`. Only scheduled runs queue; people's chats
  never do.
- **Missed firings** (Bob was down) run once if the latest was under 6 hours ago; older ones are
  recorded as skipped. Firings from before a schedule was turned on, or had its timing changed,
  are never run. When a local time happens twice as clocks go back, it fires once.
- **Old chats:** only the newest `keep_sessions` (default 30) of a schedule's chats are kept.
- **Pause everything:** `PATCH /api/settings {"schedules_paused": true}` (admin) stops every
  schedule firing; firings missed while paused follow the missed-firing rule on resume.
  **Run now** still works while paused or disabled, because a person asked for it. A queued run
  already accepted is still dispatched.
- A run that was going when Bob stopped is marked failed when Bob starts again.
- Deleting a worker deletes its schedules.

In the web app, **Schedules** (sidebar) lists each schedule with its timing in words, the next
firing, the last run and a link to its chat, **Run now**, and — for admins — turn on/off, edit,
delete and **Pause all schedules**. Chats a schedule started show its name with a clock.

## Who can use what

`BOB_PROJECT_MAP` (or a file named by `BOB_PROJECT_MAP_FILE`; the inline one wins) lists who may
sign in and which projects each person uses:

```json
{"kai@example.com": ["*"], "tester@example.com": ["wolf"]}
```

`"*"` is an **admin**: every project, and creating, changing and deleting schedules.
Anyone else sees only their listed projects; they can chat, view files and run schedules there,
and every other project's routes answer 404, as if it did not exist. Bob refuses to start on a map
that is not valid JSON, lists nobody, or has a non-string entry. The old `BOB_ALLOWED_EMAILS`
still works when no map is set, making each email an admin, and logs that it is deprecated.

## API

Admin only: `GET /api/busy`, creating, changing and deleting schedules, and changing settings.
Everything else: anyone who may use that project. Projects are not created, changed or deleted over
the API — they come from `BOB_PROJECTS`.

| | |
| --- | --- |
| `GET /api/config` · `POST /api/login` · `POST /api/logout` | Google sign-in, and who is signed in (`email`, `admin`); everything else needs the session cookie |
| `GET /api/projects` | the projects you may use |
| `GET /api/projects/{p}` | one project |
| `GET/POST /api/projects/{p}/workers` | list, create `{name, engine, model?, effort?, tools?, labels?, prompt, why}` |
| `PATCH/DELETE /api/projects/{p}/workers/{w}` | change (with a `why`), delete (its schedules go; its chats stay readable) |
| `GET /api/projects/{p}/workers/{w}/versions` | the worker's history |
| `GET/PUT /api/projects/{p}/prompt` | the project prompt and its history; `PUT {prompt, why}` |
| `GET/POST /api/projects/{p}/sessions` | list, create `{worker, model?, effort?}` or, for a plain chat, `{worker: "", engine, model?, effort?}` |
| `GET /api/sessions/{id}` | one session |
| `PATCH /api/sessions/{id}` | `{model, effort}` for the next turn; empty = the worker's setting |
| `DELETE /api/sessions/{id}` | stop it, delete it and its events |
| `POST /api/sessions/{id}/messages` | `{text}` → 202; the turn runs in the background |
| `POST /api/sessions/{id}/interrupt` | stop the running turn |
| `GET /api/sessions/{id}/events?after=` | stored events |
| `GET /api/sessions/{id}/stream` | SSE: stored events, then live ones (token deltas are live-only) |
| `GET /api/projects/{p}/files/{path}` | a file or directory listing from the work folder, sandboxed (see Files) |
| `POST /api/projects/{p}/view` · `GET /api/view/{token}/{path}` | a 12-hour viewer link, and files read through it without the cookie |
| `GET /api/projects/{p}/memories?limit=` | the project's recent memories |
| `GET /api/projects/{p}/attention?state=open\|all` · `POST /api/attention/{id}/dismiss` | requests for a person; close one |
| `GET /api/busy` | the projects with a turn or scheduled run in progress — a deploy script waits on this (admin) |
| `GET /api/projects/{p}/schedules` | schedules, each with `next_at` and `last_run`; and whether all are `paused` |
| `POST /api/projects/{p}/schedules` | `{name, worker, cron, timezone?, message, enabled?, keep_sessions?}` |
| `PATCH /api/schedules/{id}` · `DELETE /api/schedules/{id}` | change any of those fields; delete (its chats stay) |
| `POST /api/schedules/{id}/run` | run now → 202 with the run (`skipped` if one is still going) |
| `GET /api/schedules/{id}/runs` | the last 50 runs, newest first |
| `GET/PATCH /api/settings` | `{schedules_paused}` — the switch that pauses every schedule |
| `POST /mcp` | Bob's MCP server; no cookie, a chat's Bearer MCP token |
| `GET /drive/fetch/{token}` | a Drive file, by a signed link that expires in 10 minutes; no cookie |
| `GET /healthz` | 200 when the database answers |

`/mcp` and `/drive/fetch` are reachable from the internet through Caddy like every other path; the
HMAC tokens are their protection.

Events are stored exactly as the harness emitted them, with `engine` and `kind` beside the
payload. Bob's own events are `bob.user_message`, `bob.turn_done` and `bob.turn_failed`.

## Running it on a server

One image holds the API and the built web app (`Dockerfile`); project containers use
`runtime/Dockerfile`. This repository owns its own deployment: `deploy/compose.yml` and
`deploy/env.example` are committed here, and two `./stack` verbs drive publishing and deploying:

```sh
./stack publish        # scripts/publish: builds api+runtime, pushes them tagged with HEAD's sha
                        #   (refuses a dirty tree, so a tag always names committed code)
./stack deploy <tag>   # deploys that tag to the box ($BOX, default ubuntu@box.badcode.tv)
```

`./stack deploy <tag>` refuses without a tag, and refuses unless `deploy/compose.yml` exists at
that tag (i.e. it was published) and `.env.box` exists here (the box's production secrets — see
below; git-ignored, never committed). It then, over ssh: writes `deploy/compose.yml` at that tag and
`.env.box` (mode 600, umask 077 + write-then-rename, so there is never a partial or
world-readable `.env`) to `/srv/apps/bob`, pins `TAG=<tag>` in `release.env`, removes any leftover
compose and project files from the old ops-generated deployment, makes sure the `bob-projects`
network and each `bob-project-*` volume the new compose file names exist, then `sudo app bob pull
--quiet && sudo app bob up -d --remove-orphans` and waits up to 60s on
`http://127.0.0.1:8100/healthz` before printing `docker ps --filter name=bob-project-`.

`deploy/compose.yml` has Bob and **one service per project**, declared by hand rather than
generated — see the file itself. The API joins Postgres's network (`box-db`) and the
`bob-projects` network, on which every project service also sits; project services publish no
ports and carry their own `BOB_RUNTIME_TOKEN_<NAME>` / `GITHUB_TOKEN_<NAME>`, their limits
(`mem_limit`, `pids_limit`) and a `stop_grace_period` long enough for a turn in flight to finish —
the runtime drains on SIGTERM, and Docker SIGKILLs it when that runs out. The API is published on
`127.0.0.1:8100` (container port 8070) for Caddy only, and is named variable by variable rather
than given the whole env file, so it never sees a project's credentials. Bob itself has **no Docker
socket**.

**Production secrets** are `.env.box` here: copy `deploy/env.example` and fill in every variable
the compose file names, including `OPENAI_API_KEY` (the API will not start without it) and, per
project, `BOB_RUNTIME_TOKEN_<NAME>` and `GITHUB_TOKEN_<NAME>`.

> **Status:** production still runs the old Bob from `apps/bob/` in the private ops repository.
> `.env.box` is prepared but has empty values Kai must supply (`GITHUB_TOKEN_ENC`,
> `BOB_DRIVE_CLIENT_ID`, `BOB_DRIVE_CLIENT_SECRET`, `BOB_DRIVE_TOKEN_ENC`), and the box cutover
> (stop the old API and `bob-project-wolf`, reset the `bob` schema, `CREATE EXTENSION vector` as
> the superuser, publish, deploy, Codex login) and retiring `apps/bob/` in the ops repository
> (T16 items 2, 4 and 5) have **not** happened. Until they do, none of the deploy steps above
> have been run against the box.

A project's volume is declared `external`, so:

- create it before the first `up` — `docker volume create bob-project-<name>` (`./stack deploy`
  does) — because compose will not create an external volume;
- `docker compose down -v` cannot delete it. Never use `-v` anyway.

**Adding a project:** add it to `BOB_PROJECTS` and a `BOB_RUNTIME_TOKEN_<NAME>` to the `api`
service and declare a `bob-project-<name>` service and volume in `deploy/compose.yml`, add its
secrets to `.env.box`, publish, deploy; then run `codex login` in it if it uses Codex. **Changing a
secret:** edit `.env.box` and deploy — only the containers whose environment changed restart. Check
`GET /api/busy` first: restarting a container cuts off a running turn, and a scheduled run cut off
that way is recorded failed and not retried.

**Backups — three things, not one.** Postgres holds the record (workers and their history,
conversations, schedules, memory, attention requests): back it up properly, off the box. Each
`bob-project-<name>` volume holds that project's work folder, skills and harness state, including
Codex's login — a cache by design, but restoring one means restoring to the *same path* with
`tar --numeric-owner`, because git checkouts may store absolute paths. `deploy/compose.yml` is in
this repository; `.env.box` is not, so keep a copy where the other production secrets are.

⚠️ **Never rename a project.** Its name is the container name, the volume name and the
`projects.name` primary key.

## Tests

```sh
./stack test        # everything below, with a throwaway database per Go test; ends "all green"
```

Go tests that need a database **skip** without `BOB_TEST_DATABASE_URL`, and `go test` still prints
`ok` — `./stack test` sets it (a superuser URL on the shared local Postgres, since each test
creates its own database). The runtime tests run from `dist/`, so rebuild first:

```sh
(cd api && go vet ./... && BOB_TEST_DATABASE_URL=postgres://postgres:local@127.0.0.1:5432/postgres?sslmode=disable go test ./...)
(cd runtime && rm -rf dist && npm run build && npm test)
(cd web && npm run build)
```
