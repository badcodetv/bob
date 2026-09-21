# Projects in compose, Docker out of Bob — plan

*Written 2026-09-21 by a Claude session, from Kai's decisions in that session. Nothing here is
built. It replaces an earlier draft of this date that proposed collapsing every project into one
shared container; an adversarial review found that the collapse bought no goal the static compose
file does not buy, at the cost of a root supervisor, setuid spawning, uid pinning and a broken
cutover. The review's surviving findings are folded into the tickets below.*

*This plan supersedes DESIGN.md decision 1 ("one container per project, **started by the API
through the host Docker socket**") in its second half only. One container per project stays. Bob
stops starting them.*

---

## 0. Plain-English summary

Today Bob's API holds the host's **Docker socket** — a file that is, in effect, root on the
server — solely so it can start a container for each project on demand.

It does not need to. Projects are already deployment configuration: adding one is a deploy, and
changing a secret is a deploy. So each project's container is simply **declared in the compose
file**, started by Docker Compose at boot like any other service. Bob finds it by name over the
Docker network, which is exactly how it already addresses containers in production.

Bob therefore never talks to Docker at all. The socket, the Docker client, the container
lifecycle, the encrypted-secrets subsystem and its encryption key all go away — about **900 lines**
— and almost nothing new is written to replace them.

**The cutover is nearly free.** Compose can adopt the containers' existing names
(`bob-project-<name>`) and volumes (`bob-project-<name>`), both of which Bob already uses
(`api/internal/runtime/runtime.go:83,86`), and both of which exist on the current host. Nothing
moves on disk, no path changes, and every existing chat keeps resuming.

### Glossary

| Term | Meaning here |
| --- | --- |
| **Project container** | One `bob-runtime` container per project, holding that project's checkout, chat worktrees and harness state. Unchanged from today except in who starts it. |
| **Runtime server** | The small TypeScript server inside each project container, the only thing Bob's API talks to. Unchanged by this plan. |
| **Compose** | `docker compose`, reading a YAML file that declares every service. Lives in BadCode's private ops repository. |
| **`projects.yaml`** | The single list of which projects exist. The compose generator reads it; the API reads it to keep its `projects` table in step. |
| **Harness** | A lab's own agent CLI/SDK: Claude Agent SDK, Codex, OpenCode. Bob never reimplements their features. |
| **`bob.md`** | A new file in a project's config folder: front matter for project settings, body appended to every worker's system prompt. |

---

## 1. Decisions taken (Kai, 2026-09-21)

1. **One container per project stays. Compose starts them, not Bob.** The Docker socket leaves the
   design entirely.
2. **Which projects exist is a file**, not database rows created over the API. Adding a project is
   a deploy: add a service to compose, `docker compose up -d`.
3. **Per-project secrets are plain environment variables on that project's service.** The
   encrypted-at-rest secrets system — the table, the AES-GCM box, `BOB_SECRETS_KEY`, the Settings
   editor — is deleted. Changing a secret is a deploy.
4. **Bob does not manage `CLAUDE.md`, `AGENTS.md`, or which repositories a project clones.** That
   is each project's own business, done by the project's own scripts. Bob clones one repository:
   the one holding the project's config folder.
5. **`bob.md` exists.** Front matter holds project settings; the body is the project's goal and its
   memory **labelling scheme**, appended to every worker's system prompt. The labelling scheme is
   how worker fleets coordinate, so it must be prose a project writes for itself.
6. **Tool configuration names environment variables, never values.**
7. **The memory system is ported from `agent-bob`, not adopted from a framework.**
8. **Docker leaves Bob before Bob is deployed**, so production is never built around machinery with
   a known expiry date.

## 2. Decisions this plan takes (each can be overturned; say so before Part A starts)

| # | Decision | Why | Alternative |
| --- | --- | --- | --- |
| Q1 | **Compose adopts the existing container and volume names** (`bob-project-<name>`), declared with an explicit `name:` and `external: true`. | Makes the cutover a restart rather than a migration: paths inside the container are unchanged, so harness resume state and git worktrees keep working. Worktrees store **absolute** paths and would break on any move. | Fresh volumes. Every existing chat loses its worktree and its native resume. |
| Q2 | **The `projects` table stays, as a mirror of `projects.yaml`**, reconciled at boot; the create/update/delete routes are deleted. A project no longer in the file is marked **absent**, never deleted. | `sessions.project` and `schedules.project` are foreign keys to `projects(name)` (`001_init.sql`), so the rows must exist for the history to. *Note: the cascade deletes are NOT the reason — after this plan nothing calls `DeleteProject`.* | Drop the foreign keys, make `project` free text. Loses referential integrity for no gain. |
| Q3 | **A new `BOB_RUNTIME_KEY` derives each project's runtime token**, replacing today's derivation from `BOB_SESSION_SECRET`. | Today "changing `BOB_SESSION_SECRET` means restarting every project" (README). Splitting them means rotating the cookie secret no longer touches any project. The compose generator derives the same per-project token from the same key. | A random token per project in the ops environment. More to manage, and needs project-name→variable-name mapping, which is ambiguous for names containing `-`. |
| Q4 | **`projects.yaml` is mounted into the API as a file, not baked into an image.** | One copy, one source of truth. Baking it into two images means they can disagree mid-deploy. | Bake it. Cheaper to build, worse to reason about. |
| Q5 | **Per-project images and resource limits come back**, as ordinary compose fields (`image:`, `mem_limit:`, `pids_limit:`). | They cost nothing here. The project needing FFmpeg builds `FROM bob-runtime` and names its image in its own service. | Drop them. No reason to. |
| Q6 | **A project container that is not running is an error, not something Bob fixes.** The turn fails with a message naming the project. | Bob has no socket and cannot start anything. `restart: unless-stopped` is compose's job. | Give Bob the socket back for this one case. Defeats the plan. |
| Q7 | **`files_root` moves from a database column into `bob.md` front matter.** | It is project content, read from git, changeable without a deploy. It is the only project setting that was never worth a restart. | Leave it in the table, editable in Settings. Keeps a write path that nothing else needs. |
| Q8 | **The local `./stack` generates a dev compose file from a dev `projects.yaml`.** The `repo_mount` / `/seed` database column is replaced by an ordinary bind mount in that generated file. | Dev and production then differ by one file rather than by a code path. | Keep `repo_mount`. A dev-only column in a production table. |

---

## Part A — take Docker out of Bob

Ordered by dependency. Each ticket names what exists today, what changes, and how to check it.

### A1. `Ensure` becomes "return the URL and wait for health"

**Today:** `Manager.Ensure` (`api/internal/runtime/runtime.go:88`) inspects, creates and starts a
container through the Docker socket, then builds a base URL. When `BOB_DOCKER_NETWORK` is set it
already addresses the container **by name** (`runtime.go:121`) — that half is the half that stays.

**Change:**

- `Ensure(ctx, p)` becomes: build `http://bob-project-<name>:8080` with the project's token as basic
  auth, then `waitHealthy`. **Keep `waitHealthy`**, with a short timeout, so a turn sent while a
  container is restarting waits rather than failing instantly. When it times out, the error names
  the project and says the container is not running (Q6).
- Delete the `api/internal/docker` package entirely (165 lines).
- From `api/internal/runtime/runtime.go`, delete `spec`, `ReplaceStale`, `Recreate`, `Destroy`,
  `Revive`, the `destroyed` map, `SpecLabel`, `VolumeName`, and the Docker fields of `Config`
  (`DefaultImage`, `Network`, `PassEnv`, `Secrets`, `Memory`, `NanoCPUs`, `PidsLimit`).
  **Keep** `Token`, `ContainerName`, `waitHealthy`, and the package-level client functions
  `Workers`, `RunTurn`, `File`, `RemoveSession` (`runtime.go:277,319,335,344` — they are package
  functions, not methods).
- `Token(key, project)` now takes `BOB_RUNTIME_KEY` rather than the session secret (Q3).
- From `api/cmd/bob/main.go`, delete `BOB_DOCKER_SOCKET`, `BOB_RUNTIME_IMAGE`,
  `BOB_DOCKER_NETWORK`, `BOB_PASS_ENV`, `BOB_PROJECT_MEMORY`, `BOB_PROJECT_CPUS`,
  `BOB_PROJECT_PIDS`, `parseBytes`, the `ReplaceStale` startup block and the `SetSecrets` wiring.

**Acceptance:** `grep -ri docker api/` returns only historical comments. The API starts and runs a
turn with **no `/var/run/docker.sock` present** — check by running it in a container without the
mount. A turn against a stopped project container fails after the health timeout with a message
naming the project.

### A2. Projects come from `projects.yaml`

**Today:** rows created over `POST /api/projects`, changed over `PATCH`, deleted over `DELETE`
(`api/cmd/bob/http.go:262,281,312`), plus `POST /api/projects/{p}/restart` (`:339`), edited in
`web/src/ProjectSettings.tsx`.

**Change:**

- The API reads `BOB_PROJECTS_FILE` at boot and reconciles the `projects` table: insert missing,
  update changed, and set `absent_at` on rows whose project is no longer in the file (new migration).
  An absent project is left in the database — its sessions and events are history — but drops out
  of `GET /api/projects` and its routes 404.
- **Absent projects' schedules must not fire.** `tick()` lists every schedule
  (`api/cmd/bob/schedules.go:45`) and would otherwise fail on each due tick forever. Skip them, and
  log the reason once per schedule.
- Delete `POST /api/projects`, `PATCH /api/projects/{p}`, `DELETE /api/projects/{p}`,
  `POST /api/projects/{p}/restart` and their tests.
- Delete the `image` and `repo_mount` columns (compose owns both now) and `files_root` (→ A5).
- `web/src/ProjectSettings.tsx` keeps a read-only summary of where the project's config comes from,
  plus **Sync git**. Everything else goes.

File shape:

```yaml
projects:
  - name: wolf                                    # ^[a-z0-9][a-z0-9-]{0,40}$ (existing CHECK)
    repo: https://github.com/badcodetv/wolf
    ref: main
    config_dir: bob
```

**Acceptance:** creating a project over the API returns 404/405. A project added to the file and
redeployed appears with its workers. A project removed from the file vanishes from the UI, its rows
remain (checked with `./stack sql`), and its schedules stop firing.

### A3. Delete the encrypted-secrets subsystem

**Today:** `api/internal/secrets` (77 lines, AES-256-GCM), `api/internal/store/secrets.go` (61),
`api/cmd/bob/secrets.go` (118, including `applyWhenIdle`), `web/src/SecretsEditor.tsx` (71),
migration `005_project_secrets.sql`, and `BOB_SECRETS_KEY`.

**Change:** delete all of it. A project's secrets are ordinary environment variables on that
project's compose service, exactly like `BOB_REPO_URL` is today. No prefixes, no parsing, no key.

Keep the *idea* behind `CheckName` (`api/internal/secrets/secrets.go:69`) as documentation in the
ops repository: do not set `BOB_*`, `GIT_*`, `PATH`, `HOME`, `CLAUDE_CONFIG_DIR`, `CODEX_HOME` on a
project service — the image and Bob own those.

Do **not** drop the `project_secrets` table in a migration. Leave it orphaned; dropping it is a
separate, deliberate act once the values are known to be moved.

**Acceptance:** `grep -ri "BOB_SECRETS_KEY\|secrets.Box" api web` returns nothing. A turn in a
project whose service sets `FRED_API_KEY` sees it; a turn in another project does not.

### A4. The API stops running as root

**Today:** `Dockerfile:21-22` — `# Root in the container: Bob talks to the host Docker socket,
which is root-owned.` / `USER 0`.

**Change:** that reason is now gone. Use `gcr.io/distroless/static-debian12:nonroot` and drop the
`USER 0` line. This is the single largest security improvement in the plan and it costs one line.

**Acceptance:** `docker run … id` in the API image is not uid 0, and the API still serves.

### A5. `bob.md`

**New.** `<config_dir>/bob.md` in the project's repository:

```markdown
---
files_root: site          # the folder the Files page opens on. Replaces the DB column.
default_model: ...        # optional
---

Wolf tracks trading hypotheses. …

## Labelling scheme
Memories are labelled `hypothesis=<slug>`, `kind=note|decision|data`, …
```

The body is appended to **every** worker's system prompt. The change is one line:
`runtime/src/claude.ts` already does
`systemPrompt: { type: 'preset', preset: 'claude_code', append: worker.prompt }`; it becomes
`append: bobMd + "\n\n" + worker.prompt`. Same for the Codex driver when it lands. A missing
`bob.md` is not an error.

**Acceptance:** a worker's first turn shows the `bob.md` body in its system prompt (visible in the
stored init event). Changing `bob.md` and pressing **Sync git** changes the next turn with no
restart.

### A6. `./stack` starts project containers locally

**Today:** `./stack` runs Postgres in compose and the API and web app on the host; Bob starts
project containers itself, so there is nothing else to run. That stops being true.

**Change:** `./stack` reads a local `projects.dev.yaml`, generates a compose fragment (the same
generator as B1), and brings the project containers up alongside Postgres. Because the host-run API
cannot resolve Docker network names, dev services publish `8080` on `127.0.0.1` and the API's
`Ensure` keeps today's loopback-port path for that case. A local repository is an ordinary bind
mount in the generated file, replacing the `repo_mount` column (Q8).

`./stack clean` removes the generated file and the project containers. `scripts/import-agent-bob-env`
stops generating `BOB_SECRETS_KEY`.

**Acceptance:** a clean clone, `./stack build && ./stack start`, sign in, chat — following only the
README.

### A7. Documentation

`DESIGN.md`: rewrite decision 1 (Bob no longer starts containers), decision 8's credential passing,
and the diagram. `README.md`: the "Run it locally", "Secrets" and "Running it on a server" sections,
and the line about `BOB_SESSION_SECRET` restarting every project (no longer true — Q3).

---

## Part B — the deploy

### B1. The compose file and its generator

Lives in BadCode's private ops repository. One service per project:

```yaml
services:
  bob-project-wolf:
    image: europe-west1-docker.pkg.dev/webkit-servers/bob/runtime:${TAG}
    container_name: bob-project-wolf          # matches ContainerName() — adopts the existing container
    restart: unless-stopped
    init: true                                # Node as PID 1 does not reap orphans
    networks: [bob]
    volumes: [bob-project-wolf:/project]
    environment:
      BOB_REPO_URL: https://github.com/badcodetv/wolf
      BOB_REPO_REF: main                      # bob-push reads this (runtime/bob-push:7)
      BOB_REPO_SUBFOLDER: bob
      BOB_RUNTIME_TOKEN: ${TOKEN_WOLF}        # derived from BOB_RUNTIME_KEY by the generator
      CLAUDE_CODE_OAUTH_TOKEN: ${CLAUDE_CODE_OAUTH_TOKEN}
      GITHUB_TOKEN: ${WOLF_GITHUB_TOKEN}      # this project's own, may push
      FRED_API_KEY: ${WOLF_FRED_API_KEY}
    mem_limit: 8g
    pids_limit: 4096

volumes:
  bob-project-wolf:
    name: bob-project-wolf                    # matches VolumeName() — adopts the existing volume
    external: true
```

`scripts/compose-projects` (in the ops repo) reads `projects.yaml` plus a per-project secret
mapping and emits this, so the list of projects is edited in **one** place. `init: true`,
`mem_limit` and `pids_limit` are generator defaults, overridable per project.

`scripts/publish` is unchanged.

### B2. Cutover

Because of Q1 this is a restart, not a migration:

1. Generate the compose file from the current `projects` table (a one-off script, or by hand —
   there are four).
2. Stop the API. Compose `up -d` adopts each existing container name and volume.
3. Start the API with `BOB_PROJECTS_FILE` and `BOB_RUNTIME_KEY` set.

Paths inside every container are unchanged, so harness resume state and git worktrees keep working
and no chat breaks. If a container must be recreated, its volume is `external` and survives.

⚠️ Do not rename a project. Container name, volume name and the `projects.name` primary key all
carry it, and git worktrees inside the volume hold absolute paths.

### B3. Backups and the runbook

| What | Where | Lose it and… |
| --- | --- | --- |
| Postgres | its own volume | **the record is gone**: conversations, schedules, memory. `pg_dump` on a timer, off the box. |
| Each `bob-project-<name>` volume | one per project | that project's chats lose their worktrees and native resume. Snapshot; it is a cache by design (DESIGN.md decision 3). |
| `projects.yaml` + the ops `.env` | the private ops repo | the deployment cannot be reproduced. Keep it in git. |

Restore volumes with **numeric owners** (`tar --numeric-owner`) and to **the same path**, because
git worktrees store absolute paths and refuse to work when moved.

Runbook: `GET /healthz` answers 200 when the database does. A shell in a project is
`docker exec -it bob-project-<name> bash` — that is how arbitrary commands get run, by someone with
access to the box, with no web UI for it.

**Draining.** A deploy of one project's service kills that project's running turn; scheduled runs
are then recorded failed with no retry (`api/cmd/bob/schedules.go:103-158`). Keep the existing
`hold`/`active` bookkeeping (`api/cmd/bob/http.go:57`) that A3 would otherwise have removed with
`applyWhenIdle`, expose it as `GET /api/busy` (admin), and have the deploy script wait. Give the
runtime server a SIGTERM drain — stop accepting turns, finish the current one — with a matching
`stop_grace_period`.

---

## Part C — what "retire the old projects" still needs

Not required to deploy. Required before `agent-bob` (162k lines) and `agent-wolf` can be switched off.

### C1. MCP tool configuration

`<config_dir>/tools.yaml` lists MCP servers; credentials are **named**, never written:

```yaml
servers:
  fred:
    command: npx
    args: ["-y", "@example/fred-mcp"]
    env: [FRED_API_KEY]          # names only — inherited from the container's environment
```

🔴 **Never put a resolved secret value in the server config.** The Claude SDK passes MCP
configuration to the CLI as `--mcp-config <json>` **on the command line**
(`node_modules/@anthropic-ai/claude-agent-sdk/sdk.mjs`), and `/proc/<pid>/cmdline` is readable by
any process that can see it — including anyone who `docker exec`s into the container. Name the
variable and let the MCP subprocess inherit it from the harness process instead.

A worker's `tools:` front matter gains the ability to name MCP tools (`mcp__fred__*`).

### C2. The Codex engine (DESIGN.md M2)

`runtime/src/server.ts:38` maps only `claude` to a driver; a `codex` worker returns 501. Needs the
driver, the rollout-file resume path, and one spike first:

> **Spike.** Claude on a subscription is an environment variable (`CLAUDE_CODE_OAUTH_TOKEN`). Codex
> on a ChatGPT subscription is a **file** (`auth.json` in `CODEX_HOME`) that Codex **rewrites on
> refresh**. Each project container has its own copy. Confirm that two projects' Codex sessions
> refreshing independently do not invalidate each other. If they do, the answer is one login per
> project, or a serialising broker.

### C3. Port the memory system

From `agent-bob`: `go/agentdb/memories.go` (992 lines) and `go/cmd/agentd/mcp_memory.go` (557).
Four tools — `memory_create`, `memory_search`, `memory_get`, `memory_current` — append-only, labels
as jsonb with containment selectors, hybrid ranking over a generated `tsvector` column fused with
cosine distance on `vector(1536)`, provenance and a session permalink on every result, and
compare-and-swap via `if_current`. Postgres is already `pgvector/pgvector:pg17`. Skip
`go/httpapi/memories.go` (575 lines) until something needs it.

This is what makes `bob.md`'s labelling scheme (A5) mean anything.

### C4. `on_session_finished`

A hook in `bob.md` front matter naming a worker and a prompt template, fired on the existing
`bob.turn_done` event (`api/cmd/bob/http.go:642`). This turns architect loops, archival loops and
worker-triggers-worker into configuration instead of code — and is what makes Bob replicable by a
plain local Claude Code session, which is the point.

---

## What this deletes

| | Lines (approx.) |
| --- | --- |
| `api/internal/docker` | 165 |
| Container lifecycle in `api/internal/runtime` | ~200 |
| `api/internal/secrets` + `store/secrets.go` + `cmd/bob/secrets.go` | 256 |
| Project create/update/delete/restart routes and their tests | ~150 |
| `web/src/SecretsEditor.tsx` and most of `ProjectSettings.tsx` | ~150 |
| **Total** | **~920** |

Plus: the Docker socket, root in the API container, `BOB_SECRETS_KEY`, seven environment variables,
the stale-container replacement logic, and the "apply this secret when the project stops being
busy" state machine.

**What it writes:** a compose generator (ops repo), a `projects.yaml` reader, the `absent_at`
reconciliation, `bob.md` parsing, and about forty lines of changed `Ensure`.

## Risks

- 🟡 **A deploy interrupts running turns** in the projects it touches. Mitigated by B3's drain;
  smaller than before, because one project's secret change no longer restarts the others.
- 🟡 **A project container that is down stays down** until someone runs compose (Q6). `restart:
  unless-stopped` covers crashes; it does not cover a project missing from the file.
- 🟡 **N idle runtime servers**, roughly 40–60 MB each. At a handful of projects this is noise; at
  thirty it is worth revisiting.
- 🟡 **Every project's agent can read the shared subscription token**, because it needs it to run.
  True today; not made worse.
- 🟢 **Isolation between projects is the container boundary** — stronger than the Unix-user scheme
  the earlier draft proposed, and it needs no new code.
