# Bob foundation for ENC, marketing and Wolf — Design & Implementation Plan

> **EXECUTION RULES (for agents):** Work ONE ticket at a time, in order unless
> dependencies say otherwise. Only the orchestrator changes ticket Status;
> workers may only append to Notes and the Discovered Issues Log. A ticket's
> checkbox is checked only after its Validation commands have been re-run by
> the orchestrator and pass. Do not expand scope; log surprises in the
> Discovered Issues Log instead.
>
> **Two standing rules for every ticket:**
> - **Go tests need a database.** Without `BOB_TEST_DATABASE_URL` every database test *skips* and
>   `go test` still prints `ok`. Always validate with `./stack test`, or set
>   `BOB_TEST_DATABASE_URL=postgres://postgres:local@127.0.0.1:5432/postgres?sslmode=disable`
>   (what `./stack test` sets, `stack` `test)` case).
> - **Runtime tests run from `dist/`** (`runtime/package.json` `test` = `node --test dist/*.test.js`).
>   Always `cd runtime && rm -rf dist && npm run build && npm test`, so deleted tests stop running and
>   new ones start.
> - **Anything destructive or on the box waits for Kai's go-ahead**: schema resets, deleting volumes
>   or containers, deploys, anything needing a human sign-in. Prepare the exact command, then ask.

Status: approved (Kai, 2026-09-28)

**Run decisions (Kai, 2026-09-28, for the unattended run; these override the "ask Kai" rule above):**
- **Pre-authorised:** every destructive and production step exactly as the tickets describe it —
  T16's box cutover (stop old Bob, delete `bob-project-wolf` container and volume, reset the `bob`
  schema, deploy), T25's `CREATE EXTENSION vector` and deploy, T26's local wipe. Still stop for what
  only Kai can do (logins, device codes, creating tokens or OAuth apps, writing emails he has not
  given) and for anything the plan does not cover.
- **Git:** commit each verified ticket onto `main` and push it to `origin main` (`badcodetv/bob`, public).
- **ENC's repository is `emperorsnewcoin/bob`** (https://github.com/emperorsnewcoin/bob): the
  librarian clones it into `/project/work`, and `GITHUB_TOKEN_ENC` is scoped to it.
- **Wolf's attention webhook: none for now.** `BOB_ATTENTION_WEBHOOK_WOLF` stays empty; T25's live
  check skips the webhook part (the badge is still checked).
- **Bob's API port is 8070** everywhere (Discovered Issues Log, T3).
Relates: `design/2026-09-28-foundation-and-use-cases.md` (Kai's decisions — the source of this plan;
where this plan and DESIGN.md disagree, this plan wins), `design/2026-09-21-projects-in-compose.md`
(superseded in part: `projects.yaml`, the compose generator, `BOB_RUNTIME_KEY` and the ops-owned
deploy go away), `DESIGN.md` decisions 1, 5 and 9 (superseded).

Written 2026-09-28 by a Claude session from an interview with Kai, revised the same day after Kai
moved deployment into this repository and after an adversarial review.

---

## Context

Bob runs AI agent sessions for BadCode: a Go API with Postgres, a React web app, and one Docker
container per project in which a small TypeScript "runtime server" drives an agent harness (the
Claude Agent SDK today). Three projects are about to use it: **ENC** (Emperor's New Coin — a
research "canon" in markdown built from a Google Drive, plus a website; Kai and Richard; runs on
Codex), the **BadCode marketing manager** (research and content written into `badcodetv/core`; Kai
and Jack), and **Agent Wolf** (one worker per market hypothesis, each on a schedule, feeding a
public dashboard). The use-case record lists what each needs; this plan builds all of it, in the
order ENC → marketing → Wolf.

**How Bob works today, and what changes:**

- **Config comes from git.** Each project container clones one repository at boot
  (`runtime/sync.sh:1-38`) and reads `bob.md` (`runtime/src/project.ts:37-46`) and
  `workers/*.md` (`runtime/src/workers.ts:41-50`) from a subfolder of it. The API asks the runtime
  for workers over `GET /workers` (`api/internal/runtime/runtime.go:122-139`) and the UI has a
  "Sync git" button. **After:** workers and the project prompt are rows in Postgres, edited in the
  UI or by agents through MCP tools, with every change kept in a version table. The container
  clones nothing.
- **Each chat gets its own git worktree** (`runtime/src/workdir.ts:19-30`) and pushes with
  `runtime/bob-push`. **After:** all chats of a project share one work folder, `/project/work`;
  chats clone whatever repositories they need one level below it. Git is the project's own
  business, taught by worker prompts.
- **Which projects exist is `projects.yaml`**, read by the API (`api/internal/projects/projects.go`)
  and by a compose generator (`scripts/compose-projects.mjs`). Runtime passwords are HMACs of
  `BOB_RUNTIME_KEY` (`api/internal/runtime/runtime.go:43-47`, `runtime/src/auth.ts:18-20`).
  **After:** the compose file is written by hand in this repository (`deploy/compose.yml`); the API
  learns the project list from `BOB_PROJECTS=enc,…`; each project has its own random token.
- **Deployment lives in the ops repository** (`ops/apps/bob/`, `ops/scripts/deploy`), and this
  checkout's `.env` is a symlink into `ops/secrets/bob/.env.local`. **After (Kai):** this repository
  owns its whole deployment, like kai-stack does: `deploy/compose.yml`, `deploy/env.example`, a
  `./stack deploy <tag>` command, and real gitignored secret files in the checkout — `.env`
  (laptop) and `.env.box` (production). The ops repository keeps only its README map row and
  box-wide things: the shared Postgres database and role, `CREATE EXTENSION vector`, the Caddy
  route, the `box-db` network and the box's `app` helper (`ops/scripts/app`). There is still
  exactly one copy of each secret file: the ops copies are deleted after the move (the symlink was
  introduced to stop two copies drifting, `ops/secrets/README.md` "Why the symlink" — one copy in
  the bob checkout keeps that property).
- **The system prompt** is `bob.md`'s body + the worker prompt, appended to Claude Code's preset
  (`runtime/src/claude.ts:41-45`). **After:** the API composes it — a Bob environment note, the
  project prompt, then the worker prompt — and sends it with each turn.
- **Only Claude runs** (`runtime/src/server.ts:53`). **After:** Codex too (ENC runs on Kai's ChatGPT
  subscription). OpenCode is deferred.
- **Bob has no MCP server.** DESIGN.md decision 9 promised one; nothing exists. **After:** the API
  serves one at `/mcp` with worker, schedule, memory, Drive and human-attention tools.

**Fresh start.** Kai decided (2026-09-28) to ignore everything existing: no data migration, no
support for old chats. The migrations are replaced by one baseline, the `bob` database schema is
reset (laptop and box), and the old project containers and volumes are removed.

## Architecture

```
 browser ──► Bob API (Go) ─────────────► Postgres
               │   ▲                       projects · project_prompt_versions · workers · worker_versions
               │   │ POST /mcp             sessions · events · schedules · schedule_runs (queued)
               │   │  Authorization: Bearer <per-chat MCP token, minted by the API>
               │   │                       memories (+ pgvector) · attention_requests
   POST /turns │   │  tools: worker_* · schedule_* · memory_* · drive_* · request_human_attention
   {engine,    │   │                                       └──► Google Drive REST (drive.readonly)
    system_    ▼   │
    prompt,  bob-project-<name>        declared by hand in deploy/compose.yml (this repo)
    mcp_token} ├─ runtime server (TS)  stateless about config; drivers: claude, codex
               └─ volume /project
                    ├─ work/     the ONE shared work folder; chats clone repos inside it
                    ├─ skills/   linked into $CLAUDE_CONFIG_DIR/skills and $CODEX_HOME/skills
                    └─ .bob/     harness state (claude/, codex/ incl. auth.json)

 deploy:  laptop ./stack deploy <tag> ──ssh──► /srv/apps/bob/{compose.yml, .env, release.env}
                                              then `app bob pull && app bob up -d`
```

### Decisions

1. **Workers and the project prompt live in Postgres** (Kai). `workers` holds the current
   definition; `worker_versions` records every create/update/delete with a full snapshot, who, when
   and a required *why*. `projects.prompt` holds the project prompt; `project_prompt_versions`
   records its changes the same way. Worker names are immutable (a "rename" is create + delete).
   Versions are keyed by `worker_id`, so a worker deleted and re-created under the same name has
   two separate histories; the UI shows the current worker's.
2. **The API composes each turn's system prompt**: `bobNote(project)`, the project prompt and the
   worker prompt, joined by blank lines, empty parts skipped. It sends that and the worker's
   engine/model/effort/tools in `POST /turns`. The runtime reads no config.
3. **A plain chat** is a session with `worker = ''` and no `worker_id`. Its engine, model and effort
   are chosen when it is created. It sees every Bob tool, including `worker_create`.
4. **Projects come from `BOB_PROJECTS`** (comma-separated names). For each name the API requires
   `BOB_RUNTIME_TOKEN_<NAME>` (name upper-cased, `-` → `_`, ≥ 32 characters); the container gets
   its own value as `BOB_RUNTIME_TOKEN`. A missing or short token stops the API at boot. The
   `projects` table stays as a mirror (sessions and schedules have foreign keys to it), reconciled
   at boot with `absent_at` as today (`api/internal/store/store.go:87-105`).
5. **Two tokens, one per direction.** API → runtime: basic auth `bob:<runtime token>` (mechanism
   unchanged, `runtime.go:64`); the runtime token never leaves `server.ts` (it is deleted from
   `process.env` at startup, as `BOB_RUNTIME_KEY` is today at `server.ts:34-35`). Runtime → API
   `/mcp`: a **per-chat MCP token** the API mints and sends in each turn:
   `<session id>.<hex HMAC-SHA256(BOB_SESSION_SECRET, "bob-mcp\x00" + session id)>`. `/mcp` verifies
   it and derives the session, and from it the project. An agent can read its own chat's MCP token
   (it is in the harness's MCP config) — that only lets it act as that chat, which it already is.
   It cannot reach its own runtime server's `/turns` (it never sees the runtime token) nor act as
   another chat. Rejected: sending the runtime token + an `X-Bob-Session` header (review finding:
   the agent could read the token and spoof any chat of its project).
6. **Bob's MCP server is in the Go API**, JSON-RPC 2.0 over streamable HTTP (plain JSON responses;
   no SSE), protocol version `2025-06-18`. Port the transport shape from agent-bob's
   `go/cmd/agentd/mcpserver.go` (657 lines, `/home/kai/projects/badcode/agent-bob`) — not its JWT
   code. No tool takes a project argument; the token is the scope. `/mcp` and `/drive/fetch` are
   reachable from the internet through Caddy (it proxies every path); both are protected by HMAC
   tokens, which is accepted. Rejected: a stdio MCP server in each container.
7. **Codex login lives on the project's volume.** Kai runs
   `docker exec -it bob-project-<name> codex login --device-auth` once per project (device-code
   login must be enabled in the ChatGPT account's security settings); `auth.json` stays in
   `$CODEX_HOME` (`/project/.bob/codex`) and Codex refreshes it itself. Rejected: copying
   `auth.json` from `.env` (OpenAI rotates the refresh token, so copies go stale).
8. **Drive: Bob's own read-only tools in the API**, calling the Drive REST API with scope
   `https://www.googleapis.com/auth/drive.readonly`. A refresh token per project is minted once by
   `scripts/drive-token` and stored as `BOB_DRIVE_TOKEN_<NAME>`; the OAuth client is
   `BOB_DRIVE_CLIENT_ID` / `BOB_DRIVE_CLIENT_SECRET`. The client's consent screen must be
   **"In production"**: a client left in "Testing" gets refresh tokens that expire after 7 days.
   A project with no token has no Drive tools. Rejected: agent-bob's proxy to Google's hosted Drive
   MCP (Developer Preview, consumer accounts unconfirmed, full read-write scopes, ~3,000 lines).
9. **Memory is full hybrid from day one** (Kai): agent-bob's labels + selectors + Postgres
   full-text + pgvector embeddings (OpenAI `text-embedding-3-small`, 1536 dimensions,
   `OPENAI_API_KEY` on the API), fused by Reciprocal Rank Fusion (score = Σ 1/(60+rank)). No
   embedding key or no `vector` extension → the API refuses to boot, with the fix in the message.
10. **Human attention**: a stored request, a badge on the chat and project, a "Needs you" list on
    the Overview, and an optional POST to `BOB_ATTENTION_WEBHOOK_<NAME>`. It closes when a person
    replies in that chat or presses Dismiss. No expiry.
11. **Scheduled runs queue per project** (only scheduled runs; people's chats never queue). A
    firing creates a `queued` run; a dispatcher starts a project's oldest queued run only when that
    project has no `running` scheduled run. A schedule that already has a queued or running run
    records the new firing as `skipped`.
12. **Deleting a worker deletes its schedules** and keeps its chats readable; those chats refuse
    new messages. A chat whose worker's engine has changed also refuses new messages.
13. **Agents may manage schedules** through MCP tools even though the HTTP schedule routes are
    admin-only (`api/cmd/bob/http.go:110-112`). This is intended: Wolf's interviewer creates
    schedules. The HTTP policy is unchanged.
14. **Skills** are `/project/skills`, created at boot and symlinked into each harness's lookup path.
15. **This repository owns its deployment** (Kai, 2026-09-28): `deploy/compose.yml`,
    `deploy/env.example`, `./stack deploy <tag>`, and secrets in the gitignored `.env` (laptop) and
    `.env.box` (production) — both already covered by `.gitignore` (`.env`, `.env.*`). The compose
    file only names variables (this repository is public). Rejected: the ops repository fetching
    the compose file from this repository (first draft of this plan) — config for one app split
    across two repositories.
16. **Harnesses: Claude and Codex.** OpenCode is out of scope (Kai: "deal with this later").

### Bob environment note (exact text, first part of every system prompt)

```
You are working inside Bob, BadCode's agent runner, in the project "<project>".
- Your working folder is /project/work. Every chat in this project shares it, so other chats may
  be changing files there at the same time. Clone repositories one level below it
  (/project/work/<repo>).
- Skills live in /project/skills. You may add or edit them; every chat in the project sees them.
- Bob's own tools are on the MCP server named "bob": workers, schedules, memory, Google Drive
  (when connected) and request_human_attention.
```

## File Structure

**Delete (bob)**

| Path | Why |
| --- | --- |
| `projects.dev.yaml`, `scripts/compose-projects.mjs` | no `projects.yaml`, no generator |
| `api/internal/projects/` (both files) | projects come from `BOB_PROJECTS` |
| `api/internal/store/migrations/001_init.sql` … `008_files_root_to_bob_md.sql` | fresh baseline |
| `runtime/sync.sh`, `runtime/bob-push`, `runtime/src/push.test.ts` | no config clone, no worktrees |
| `runtime/src/{project,workers,workdir}.ts` and their `.test.ts` | config comes in the turn |
| `examples/config/` | git-held config is gone |
| `docker-compose.yml` (repo root, `services: {}`) | replaced by `deploy/` |
| `scripts/import-agent-bob-env` | wrote `.env` from agent-bob; obsolete |
| `web/src/ProjectSettings.tsx` | it only showed the git source and Sync |

**Delete (ops, `/home/kai/projects/badcode/ops`, its own commit — T16)**: `apps/bob/` (whole
directory), `secrets/bob/.env`, `secrets/bob/.env.local` (after their contents are in the bob
checkout). `secrets/bob/create-database.sql` stays (box-wide Postgres setup).

**Create**

| Path | Purpose |
| --- | --- |
| `api/internal/store/migrations/001_baseline.sql` | the whole schema for Part 1 |
| `api/internal/store/migrations/002_worker_labels.sql` | `workers.labels` (T18) |
| `api/internal/store/migrations/003_schedule_queue.sql` | `queued` run status (T20) |
| `api/internal/store/migrations/004_memories.sql` | memories + vector column (T21) |
| `api/internal/store/migrations/005_attention.sql` | attention requests (T23) |
| `api/internal/store/store_test.go` | `newTestStore(t)` + `insertWorker` helpers for store tests |
| `api/internal/store/workers.go` (+ `workers_test.go`) | workers, versions, project prompt |
| `api/internal/store/memories.go` (+ test) | memory store and hybrid search |
| `api/internal/store/attention.go` (+ test) | attention store |
| `api/internal/labels/labels.go` (+ test) | label validation, selector parser, selector → SQL |
| `api/internal/embed/embed.go` (+ test) | `Embedder` interface, OpenAI implementation, fake for tests |
| `api/internal/mcp/server.go` (+ test) | JSON-RPC transport, tool registry |
| `api/internal/drive/drive.go` (+ test) | token refresh, search, list, export, download |
| `api/cmd/bob/prompt.go` (+ test) | `bobNote`, `composePrompt` |
| `api/cmd/bob/mcp.go` (+ test) | MCP token mint/verify, `/mcp` auth, tool registration, `bob_whoami` |
| `api/cmd/bob/workers.go` (+ test) | worker and project-prompt HTTP routes, shared validation |
| `api/cmd/bob/tools_{workers,schedules,memory,drive,attention}.go` (+ tests) | MCP tools |
| `api/cmd/bob/attention.go` | attention HTTP routes and webhook |
| `api/cmd/bob/drivefetch.go` | signed `GET /drive/fetch/{token}` |
| `runtime/src/codex.ts` (+ test) | Codex driver |
| `runtime/src/mcp.ts` (+ test) | per-turn MCP server config |
| `runtime/src/skills.ts` (+ test) | skills folder linking |
| `web/src/Workers.tsx`, `WorkerEditor.tsx`, `ProjectPrompt.tsx` | worker and prompt editing |
| `web/src/engines/codex.ts` | Codex native events → assistant-ui messages |
| `web/src/Attention.tsx` | "Needs you" list |
| `deploy/compose.yml` | the box's compose file (API + one service per project) |
| `deploy/compose.dev.yml` | the laptop's project container |
| `deploy/env.example` | every variable either compose file names, with dummy values |
| `scripts/drive-token` | one-off: mint a `drive.readonly` refresh token |
| `.env.box` (gitignored, never committed) | production secrets |

**Modify**: `api/cmd/bob/{main,http,schedules,files}.go` and their tests,
`api/internal/runtime/runtime.go` (+ test), `api/internal/engines/engines.go` (+ test),
`api/internal/store/{store,schedules}.go`, `runtime/src/{server,claude,turn,auth}.ts`,
`runtime/{Dockerfile,entrypoint.sh,package.json}`,
`web/src/{App,Sidebar,Overview,Chat,ChatHeader,Files,Schedules,api,ui}.tsx|ts`, `stack`,
`scripts/dev-api`, `README.md`, `DESIGN.md`, `.env` (becomes a real file). **Ops**: `README.md`
(Bob's map row), `secrets/README.md` (bob no longer there).

## Interfaces

### Environment

**API** (`api/cmd/bob/main.go` header comment must list all of these):

| Variable | Required | Meaning |
| --- | --- | --- |
| `BOB_DATABASE_URL`, `GOOGLE_CLIENT_ID`, `BOB_SESSION_SECRET`, `BOB_PROJECT_MAP` / `_FILE`, `BOB_ADDR`, `BOB_WEB_DIR` | as today | unchanged |
| `BOB_PROJECTS` | yes | `enc,marketing,wolf` |
| `BOB_RUNTIME_TOKEN_<NAME>` | one per project | that project's token (≥ 32 chars; `openssl rand -hex 32`) |
| `BOB_RUNTIME_HOSTS` | laptop only | as today (`main.go:118-132`) |
| `BOB_PUBLIC_URL` | yes | `https://bob.box.badcode.tv` (box), `http://localhost:8080` (laptop); chat links in tool results and webhooks |
| `BOB_DRIVE_CLIENT_ID`, `BOB_DRIVE_CLIENT_SECRET` | if any Drive token is set | OAuth client that minted the tokens |
| `BOB_DRIVE_TOKEN_<NAME>` | optional | that project's Drive refresh token |
| `OPENAI_API_KEY` | yes (from T21) | embeddings |
| `BOB_EMBEDDING_MODEL` | no | default `text-embedding-3-small` |
| `BOB_ATTENTION_WEBHOOK_<NAME>` | optional | POST target for that project's attention requests |

Deleted: `BOB_PROJECTS_FILE`, `BOB_RUNTIME_KEY`. Every API variable must be **named in
`deploy/compose.yml`'s `api` service**, or the box API never sees it.

**Project container**: `BOB_PROJECT_NAME`, `BOB_RUNTIME_TOKEN`, `BOB_API_URL`
(`http://api:8070` on the box, `http://host.docker.internal:8070` on the laptop),
`CLAUDE_CODE_OAUTH_TOKEN`, `GITHUB_TOKEN` (box: from `GITHUB_TOKEN_<NAME>`; laptop: plain
`GITHUB_TOKEN`), plus project-specific ones (`FRED_API_KEY` for wolf). Deleted: `BOB_REPO_URL`,
`BOB_REPO_REF`, `BOB_REPO_SUBFOLDER`, `BOB_RUNTIME_KEY`.

### Runtime HTTP (after T2)

```
GET  /health          → {ok, draining}                                   (unchanged)
GET  /files/<path>    → a file or {entries} under /project/work (was /project/repo)
POST /turns           → NDJSON {engine, event} … then {done, harness_session_id?, error?}
```
`/workers`, `/sync` and `DELETE /sessions/<id>` are deleted.

`POST /turns` body (`runtime/src/turn.ts`; Go mirror `runtime.TurnRequest`):
```ts
interface TurnRequest {
  session_id: string            // ^[\w-]+$
  engine: 'claude' | 'codex'
  model?: string
  effort?: string
  tools?: string[]              // claude only (allowedTools); ignored by codex
  system_prompt: string         // composed by the API
  mcp_token: string             // per-chat MCP bearer token (Decision 5); '' until T9
  text: string
  resume?: string               // harness session / thread id from the previous turn
  user_email?: string
  user_name?: string
}
```
`runtime/src/mcp.ts`:
```ts
export function bobMcp(apiUrl: string, mcpToken: string): { url: string; headers: Record<string, string> }
// → { url: `${apiUrl}/mcp`, headers: { Authorization: `Bearer ${mcpToken}` } }
```

### Database (baseline, T1)

```sql
CREATE TABLE projects (
  name text PRIMARY KEY CHECK (name ~ '^[a-z0-9][a-z0-9-]{0,40}$'),
  prompt text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now(),
  absent_at timestamptz
);
CREATE TABLE project_prompt_versions (
  id bigserial PRIMARY KEY, project text NOT NULL REFERENCES projects(name),
  prompt text NOT NULL, why text NOT NULL, changed_by text NOT NULL,
  changed_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE workers (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  project text NOT NULL REFERENCES projects(name),
  name text NOT NULL CHECK (name ~ '^[a-z0-9][a-z0-9-]{0,40}$'),
  engine text NOT NULL CHECK (engine IN ('claude', 'codex')),
  model text NOT NULL DEFAULT '', effort text NOT NULL DEFAULT '',
  tools jsonb,                                   -- null = harness default
  prompt text NOT NULL DEFAULT '',
  created_by text NOT NULL, created_at timestamptz NOT NULL DEFAULT now(),
  updated_by text NOT NULL, updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (project, name)
);
-- No FK to workers: versions outlive a deleted worker.
CREATE TABLE worker_versions (
  id bigserial PRIMARY KEY, worker_id uuid NOT NULL, project text NOT NULL REFERENCES projects(name),
  name text NOT NULL, action text NOT NULL CHECK (action IN ('create', 'update', 'delete')),
  snapshot jsonb NOT NULL,                       -- after the change; for delete, before it
  why text NOT NULL, changed_by text NOT NULL, changed_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX worker_versions_worker ON worker_versions (worker_id, id DESC);
CREATE TABLE sessions (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  project text NOT NULL REFERENCES projects(name),
  worker_id uuid REFERENCES workers(id) ON DELETE SET NULL,
  worker text NOT NULL DEFAULT '',               -- name at creation; '' = plain chat
  engine text NOT NULL CHECK (engine IN ('claude', 'codex')),
  harness_session_id text NOT NULL DEFAULT '',
  model text NOT NULL DEFAULT '',
  effort text NOT NULL DEFAULT '' CHECK (effort IN ('', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max')),
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX sessions_project ON sessions (project, created_at DESC);
-- events + events_session index: exactly as 001_init.sql:21-29.
-- schedules: as 003_schedules.sql:2-13 plus `changed_at timestamptz NOT NULL DEFAULT now()`
--   (006_schedule_changed_at.sql), EXCEPT `worker text` is replaced by
--   `worker_id uuid NOT NULL REFERENCES workers(id) ON DELETE CASCADE`.
-- schedule_runs + schedule_runs_schedule index: as 003_schedules.sql:16-27.
-- settings: as 003_schedules.sql:33-36.
ALTER TABLE sessions ADD COLUMN schedule_id uuid REFERENCES schedules(id) ON DELETE SET NULL;
```
A session "has lost its worker" when `worker <> '' AND worker_id IS NULL`.

### Go store (T1, T4)

```go
// T1
type Project struct { Name, Prompt string; CreatedAt time.Time }
func (s *Store) ReconcileProjects(ctx context.Context, names []string) error
type Session struct { /* as today */ WorkerID *string `json:"worker_id"`; WorkerRemoved bool `json:"worker_removed,omitempty"` /* list only */ }
func (s *Store) CreateSession(ctx context.Context, project string, workerID *string, worker, engine, model, effort string) (Session, error)
type Schedule struct { /* as today, minus Worker text */ WorkerID string `json:"worker_id"`; Worker string `json:"worker"` /* read-only, joined from workers.name */ }

// T4 — api/internal/store/workers.go
type Worker struct {
    ID, Project, Name, Engine, Model, Effort, Prompt string
    Tools     []string          `json:"tools,omitempty"`   // nil = harness default
    Labels    map[string]string `json:"labels"`            // from T18; always {} before
    CreatedBy, UpdatedBy string
    CreatedAt, UpdatedAt time.Time
}
type WorkerVersion struct { ID int64; WorkerID, Project, Name, Action, Why, ChangedBy string; Snapshot json.RawMessage; ChangedAt time.Time }
type PromptVersion struct { ID int64; Prompt, Why, ChangedBy string; ChangedAt time.Time }
var ErrConflict = errors.New("already exists")

func (s *Store) CreateWorker(ctx context.Context, w Worker, by, why string) (Worker, error)        // ErrConflict on duplicate name
func (s *Store) UpdateWorker(ctx context.Context, w Worker, by, why string) (Worker, error)        // by project+name; ErrNotFound
func (s *Store) DeleteWorker(ctx context.Context, project, name, by, why string) error             // cascades schedules
func (s *Store) Worker(ctx context.Context, project, name string) (Worker, error)
func (s *Store) WorkerByID(ctx context.Context, id string) (Worker, error)
func (s *Store) Workers(ctx context.Context, project string) ([]Worker, error)                     // by name
func (s *Store) WorkerVersions(ctx context.Context, workerID string) ([]WorkerVersion, error)      // newest first
func (s *Store) SetProjectPrompt(ctx context.Context, project, prompt, by, why string) error
func (s *Store) ProjectPromptVersions(ctx context.Context, project string) ([]PromptVersion, error) // newest first
```
Every write and its version row happen in one transaction; an empty `why` is an error.

### API HTTP (all under `requireLogin`, policy `member` unless noted)

```
GET    /api/projects/{project}/workers                     → {workers: Worker[]}
POST   /api/projects/{project}/workers                     {name, engine, model?, effort?, tools?, prompt, why} → Worker
PATCH  /api/projects/{project}/workers/{name}              {engine?, model?, effort?, tools?, prompt?, why} → Worker
DELETE /api/projects/{project}/workers/{name}?why=…        → {ok}
GET    /api/projects/{project}/workers/{name}/versions     → {versions: WorkerVersion[]}
GET    /api/projects/{project}/prompt                      → {prompt, versions: PromptVersion[]}
PUT    /api/projects/{project}/prompt                      {prompt, why} → {prompt}
POST   /api/projects/{project}/sessions                    {worker, model?, effort?} | {worker: "", engine, model?, effort?}
GET    /api/projects/{project}/memories?limit=20           → {memories}                     (T22)
GET    /api/projects/{project}/attention?state=open|all    → {requests}                     (T23)
POST   /api/attention/{attention}/dismiss                  → {ok}                           (T23)
POST   /mcp                                                no cookie; Bearer MCP token      (T8)
GET    /drive/fetch/{token}                                no cookie; signed, 10 minutes    (T15)
```
Deleted: `POST /api/projects/{project}/sync`. `GET /api/projects/{project}/files…` serves
`/project/work`.

### MCP (T8)

```go
// api/internal/mcp
type Caller struct {
    Project, SessionID string
    Worker  string // the session's worker name; '' for a plain chat
    User    string // email of the session's latest bob.user_message, or "schedule:<id>"
    APIBase string // "http://" + the /mcp request's Host header: how this container reaches the API
}
type Tool struct {
    Name, Description string
    InputSchema json.RawMessage
    Available   func(Caller) bool // nil = always
    Call        func(ctx context.Context, c Caller, args json.RawMessage) (any, error)
}
func New() *Server
func (s *Server) Register(t ...Tool)
func (s *Server) Handler(auth func(r *http.Request) (Caller, error)) http.Handler
```

### MCP tools (all scoped to the calling project; `by` = `Caller.User`)

| Tool | Input (required in **bold**) | Result |
| --- | --- | --- |
| `bob_whoami` (T9) | — | `{project, session, worker, user}` |
| `worker_create` (T10) | **name**, **engine** (`claude`\|`codex`), model, effort, tools (string[]), **prompt**, labels (T18), **why** | the worker |
| `worker_update` (T10) | **name**, engine, model, effort, tools, prompt, labels (T18), **why** | the worker |
| `worker_list` (T10) | name, label_selector (T18) | with name: that worker incl. prompt; else `[{name, engine, model, effort, labels, updated_at}]` |
| `worker_delete` (T10) | **name**, **why** | `{ok}` |
| `schedule_create` (T19) | **name**, **worker**, **cron**, timezone (default UTC), **message**, enabled (default true), keep_sessions (default 30) | the schedule + `next_at` |
| `schedule_update` (T19) | **name**, worker, cron, timezone, message, enabled, keep_sessions | the schedule + `next_at` |
| `schedule_list` (T19) | — | schedules with `next_at` and last run |
| `schedule_delete` (T19) | **name** | `{ok}` |
| `memory_create` (T22) | **content** (≤ 24 KiB), labels (object), if_current (id) | the memory |
| `memory_search` (T22) | label_selector, query, limit (≤ 100, default 20), since, until, latest_per, created_by_worker (`self` allowed) | hits `{id, labels, snippet, score, created_at, chat_url}` |
| `memory_get` (T22) | **id** | full memory |
| `memory_current` (T22) | **name** | newest memory labelled `name=<name>`, or `{found:false}` |
| `drive_search` (T15) | **query**, limit (≤ 50, default 20) | `[{id, name, mime_type, modified_at, parents}]` |
| `drive_list` (T15) | folder_id (default: My Drive root and Shared with me), page_token | `{files, next_page_token}` |
| `drive_read` (T15) | **file_id**, offset (chars, default 0), limit (default 50000) | `{name, mime_type, text, offset, total_chars}` |
| `drive_fetch` (T15) | **file_id** | `{url, name, mime_type, expires_at}`; use `curl -fsSL "$url" -o <file>` |
| `request_human_attention` (T23) | **message**, notice (bool) | `{id, chat_url}` + "end your turn now" |

`drive_read` exports: Google Docs → `text/markdown`; Sheets → `text/csv` (**first sheet only** —
say so in the description); Slides → `text/plain`; any `text/*` or `application/json` file → its
bytes; anything else → an error telling the agent to use `drive_fetch`.

## Out of Scope

- **OpenCode** (driver, auth, model ids). Engine checks allow only `claude` and `codex`.
- **Gmail** (any scope), posting to social media, Google Drive writes.
- Migrating any existing data, keeping old chats resumable, importing Wolf's git workers.
- Archiving workers (delete only), attention expiry, a memory browser beyond the Overview list.
- Wolf's own repository work (making `badcodetv/wolf` public, its Pages workflow, its toolkit, its
  worker prompts) and ENC's website. These are the projects' own business.
- Any conflict handling between chats sharing `/project/work`.
- Resource-limit tuning beyond today's `mem_limit: 8g`, `pids_limit: 4096`, `stop_grace_period: 10m`.
- Changing Caddy to block `/mcp` or `/drive/fetch` from the internet (Decision 6).

---

## Tickets

### Part 1 — ENC

### T1: Fresh schema baseline; projects from `BOB_PROJECTS`   [Status: done | Model: opus]
- **Scope:** Replace migrations `001`–`008` with `001_baseline.sql` (Interfaces → Database). Change
  `store.Project`, `Session`, `CreateSession`, `Schedule` and `ReconcileProjects` as in Interfaces →
  Go store (T1). Delete `api/internal/projects/`. `main.go` reads `BOB_PROJECTS`, validates names
  against `^[a-z0-9][a-z0-9-]{0,40}$`, requires `BOB_RUNTIME_TOKEN_<NAME>` (≥ 32 chars) and
  `BOB_PUBLIC_URL` (no trailing slash), and reconciles. `runtime.Config` replaces
  `TokenKey []byte` with `Tokens map[string]string`; delete `runtime.Token`. Keep every existing
  test passing: add `api/internal/store/store_test.go` with `newTestStore(t)` (copy the logic of
  `testStore`, `api/cmd/bob/schedules_test.go:25`) and a raw-SQL helper
  `insertWorker(t, st, project, name, engine) (id string)`. Test helpers cannot cross packages, so
  write the same raw-SQL helper twice: in `api/internal/store/store_test.go` and in
  `api/cmd/bob/schedules_test.go`. Existing schedule/session tests create their worker with it. Tests that exercise git-sync behaviour (the
  runtime `/workers` fake in `schedules_test.go`) keep working unchanged until T2; nothing is skipped.
- **Files:** delete `api/internal/store/migrations/00{1..8}_*.sql`, `api/internal/projects/`;
  create `001_baseline.sql`, `api/internal/store/store_test.go`; modify
  `api/internal/store/{store,schedules}.go`, `api/cmd/bob/{main,http,schedules,files}.go` and
  tests, `api/internal/runtime/{runtime,runtime_test}.go`.
- **Acceptance criteria:** a fresh database migrates to exactly the baseline; `sessions.worker_id`
  is `ON DELETE SET NULL`; `schedules.worker_id` is `ON DELETE CASCADE`; `worker_versions` has no
  FK to `workers`; boot fails naming the exact missing variable; `-` in a project name maps to `_`.
- **TDD:** yes — reconcile-by-names, both cascades, name → variable mapping, first.
- **Validation:** `./stack test` → "all green"; `cd api && go vet ./...` → clean;
  `grep -rn "BOB_RUNTIME_KEY\|internal/projects" api/` → no matches.
- **Depends on:** —
- [x] done
- Notes: (executor) Tests added: store `TestFreshDatabaseIsTheBaseline`, `TestReconcileProjectsByNames`,
  `TestDeletingAWorkerCascades` (both cascades + versions outlive the worker); runtime `TestTokenVar`
  (name → variable; lives in `runtime`, as `runtime.TokenVar`, so `Ensure`'s error and `main` share it),
  `TestEnsureNeedsTheProjectsToken`; main `TestProjectsFromEnv`, `TestPublicURL`. `runtime.Token`'s test
  deleted with it. `Session.WorkerRemoved` is filled by `store.Sessions` already (T5 need only
  pass it through). `cmd/bob`'s `insertWorker` reaches the throwaway database through a
  `testDatabases` map filled by `testStore` (the store keeps its pool private). `go mod tidy` dropped
  `gopkg.in/yaml.v3`. See Discovered Issues Log for interim gaps until T2–T5.

### T2: Runtime becomes stateless about config   [Status: done | Model: opus]
- **Scope:** `server.ts`: read `BOB_RUNTIME_TOKEN` and `BOB_API_URL` (exit with a message if either
  is missing), delete the token from `process.env`, compare the basic-auth password directly (drop
  `runtimeToken` from `auth.ts`); delete `/workers`, `/sync`, `DELETE /sessions/<id>`; `/files`
  serves `/project/work`; `/turns` accepts the new `TurnRequest` (400 when `engine` is not a
  registered driver or `system_prompt` is missing) and dispatches on `body.engine`; cwd is always
  `/project/work`. `claude.ts`: `systemPrompt: {type:'preset', preset:'claude_code', append:
  turn.systemPrompt}`, `allowedTools: turn.tools`, model/effort from the turn. `entrypoint.sh`:
  `mkdir -p /project/work /project/skills` + the harness dirs; no sync. `Dockerfile`: drop
  `sync.sh`, `bob-push`; keep the git credential helper. Delete the runtime files listed under
  Delete. Go: `runtime.TurnRequest` mirrors the body (`MCPToken` left empty until T9); delete
  `runtime.Workers`, `WorkerList`, `RemoveSession`; `removeSession` (`http.go:339-349`) only
  deletes from the store. `runTurn` and `execute` get the worker from the database: since workers
  have no routes yet, this ticket adds only the store read (`WorkerByID`, a minimal version in
  `api/internal/store/workers.go` that T4 extends) and sends `system_prompt` = the worker prompt;
  T5 replaces that with `composePrompt`. Remove the sync calls in `schedules.go:121-128,160-165` and
  update `schedules_test.go` (including the worktree assertions at `:313-318`).
- **Files:** `runtime/src/{server,claude,turn,auth}.ts` (+ tests), `runtime/{entrypoint.sh,Dockerfile}`,
  runtime deletions; `api/internal/runtime/runtime.go` (+ test), `api/internal/store/workers.go`
  (minimal), `api/cmd/bob/{http,schedules}.go` (+ tests).
- **Acceptance criteria:** a turn with an unknown engine → 400; the image contains no `sync.sh` or
  `bob-push`; `grep -rn "BOB_REPO_\|worktree\|bob-push" runtime/src api/` → no matches.
- **TDD:** yes — `turn.test.ts` for body validation, `auth.test.ts` for direct comparison.
- **Validation:** `cd runtime && rm -rf dist && npm run build && npm test` → pass; `./stack test` →
  "all green"; `docker build -t bob-runtime:dev runtime` → succeeds.
- **Depends on:** T1
- [x] done
- Notes: (executor) Tests added: runtime `turn.test.ts` "a turn carries everything it runs with…" and
  "a turn Bob could not have meant is refused…" (for the new `parseTurn(body, engines, cwd)` in
  `turn.ts`, which `server.ts` uses), `auth.test.ts` "the password is BOB_RUNTIME_TOKEN itself…";
  Go `TestReadingAWorker` (store), `TestTurnRequestAlwaysSendsThePrompt` (runtime),
  `TestTurnCarriesTheWorkersSettings`, `TestTurnOnADeletedWorkerFails`,
  `TestCreateSessionFindsTheWorkerInTheDatabase` (cmd/bob, new `http_test.go`); access test now
  expects `POST …/sync` → 404. `schedules_test.go`'s fake runtime records turns and any other
  request as unexpected (asserted empty); sync/missing-worker/worktree cases dropped;
  `TestScheduleNoOverlapAndPostRunSync` → `TestScheduleNoOverlap`. The API side of the turn is
  `app.turnRequest` (`http.go`): engine from the session, model/effort = session override else the
  worker's, tools and `system_prompt` = the worker's; a session whose worker is gone fails with
  "worker … has been deleted" before calling the runtime; a plain chat sends an empty prompt.
  `system_prompt` and `mcp_token` have no `omitempty` (the runtime refuses a turn without the prompt;
  an empty one is accepted). Also: `GET /api/projects/{p}/workers` and `POST …/sync` routes deleted
  (T5 re-adds the first from the store); `yaml` dropped from `runtime/package.json`; the runtime
  requires `BOB_RUNTIME_TOKEN` and `BOB_API_URL` only (`BOB_PROJECT_NAME` is no longer read). See
  the Discovered Issues Log for `store.Worker` (by name) and interim gaps.

### T3: Compose files and secrets in this repo; `./stack` uses them   [Status: done | Model: sonnet]
- **Scope:**
  1. **Secrets become real files here.** Replace the `.env` symlink with its contents:
     `cp --remove-destination "$(readlink -f .env)" .env` (ask Kai first). Do not touch the ops copy
     yet (T16 deletes it). Add `BOB_RUNTIME_TOKEN_DEV=$(openssl rand -hex 32)` and
     `BOB_PUBLIC_URL=http://localhost:8080` to `.env`.
  2. `deploy/compose.dev.yml`: service `bob-project-dev` — `image: bob-runtime:dev`,
     `container_name: bob-project-dev`, `restart: unless-stopped`, `init: true`, environment
     `BOB_PROJECT_NAME: dev`, `BOB_RUNTIME_TOKEN: ${BOB_RUNTIME_TOKEN_DEV}`,
     `BOB_API_URL: http://host.docker.internal:8090`, `CLAUDE_CODE_OAUTH_TOKEN: ${CLAUDE_CODE_OAUTH_TOKEN}`,
     `GITHUB_TOKEN: ${GITHUB_TOKEN}`; `extra_hosts: ["host.docker.internal:host-gateway"]`;
     `ports: ["127.0.0.1:8100:8080"]`; volume `bob-project-dev:/project` declared
     `external: true`; `mem_limit: 8g`, `pids_limit: 4096`, `stop_grace_period: 10m`.
  3. `deploy/compose.yml` (box): `name: bob`; service `api` (image
     `europe-west1-docker.pkg.dev/webkit-servers/bob/api:${TAG:?}`, `restart: unless-stopped`,
     environment naming one by one `BOB_DATABASE_URL`, `GOOGLE_CLIENT_ID`, `BOB_SESSION_SECRET`,
     `BOB_PROJECT_MAP`, `BOB_PUBLIC_URL`, `BOB_PROJECTS: enc` (literal), `BOB_RUNTIME_TOKEN_ENC`,
     each with `${VAR:?}`; networks `box-db` and `bob-projects`, both `external: true`; ports
     `127.0.0.1:8100:8090`; `mem_limit: 1g`) and service `bob-project-enc` (image
     `…/bob/runtime:${TAG:?}`, `container_name: bob-project-enc`, `init: true`,
     `restart: unless-stopped`, env `BOB_PROJECT_NAME: enc`,
     `BOB_RUNTIME_TOKEN: ${BOB_RUNTIME_TOKEN_ENC:?}`, `BOB_API_URL: http://api:8090`,
     `CLAUDE_CODE_OAUTH_TOKEN: ${CLAUDE_CODE_OAUTH_TOKEN:?}`, `GITHUB_TOKEN: ${GITHUB_TOKEN_ENC:?}`;
     volume `bob-project-enc:/project` external; network `bob-projects`; limits as above).
     Keep the explanatory header comments from `ops/apps/bob/compose.yml:1-9` (updated).
  4. `deploy/env.example`: every variable both files name, each with a **dummy non-empty value**
     (e.g. `BOB_DATABASE_URL=postgres://user:pass@postgres:5432/bob`), so
     `docker compose config` can be run against it. Comment which are box-only.
  5. `./stack`: `projects_up` → `docker volume create bob-project-dev` then
     `docker compose --env-file ./.env -f deploy/compose.dev.yml up -d`; the API is started with
     `BOB_PROJECTS=dev BOB_RUNTIME_HOSTS=dev=127.0.0.1:8100`; `test` builds the runtime before
     testing it (`(cd runtime && rm -rf dist && npm run build && npm test --silent)`); `status`
     selects `name, coalesce(absent_at::text,'served')`; `clean` lists and removes every
     `bob-project-*` container and volume as today; `need_env` message → "no .env — copy
     deploy/env.example to .env and fill it in". Update the header comment. `scripts/dev-api`: same
     message.
  6. Delete `projects.dev.yaml`, `scripts/compose-projects.mjs`, root `docker-compose.yml`,
     `examples/config/`, `scripts/import-agent-bob-env`.
- **Files:** `.env`, `deploy/{compose.yml,compose.dev.yml,env.example}`, `stack`, `scripts/dev-api`,
  deletions.
- **Acceptance criteria:** `./stack start` brings up `bob-project-dev` and the API; the project list
  in the UI shows only `dev`; `.env` is a regular file and `git status` does not show it.
- **TDD:** no (config/wiring).
- **Validation:** (each destructive step after asking Kai)
  `echo y | ./stack clean` (removes the old `bob-project-{bob-examples,wolf,local}` containers and
  volumes); reset the local schema **as `app_bob`**, so it owns it:
  `./stack sql "DROP SCHEMA public CASCADE; CREATE SCHEMA public;"`;
  `./stack build && ./stack start` → "ready";
  `(set -a; . ./.env; curl -s -u "bob:$BOB_RUNTIME_TOKEN_DEV" http://127.0.0.1:8100/health)` →
  `{"ok":true,"draining":false}`;
  `TAG=x docker compose --env-file deploy/env.example -f deploy/compose.yml config >/dev/null` → exit 0;
  `test -L .env` → exit 1.
- **Depends on:** T2
- [x] done
- Notes: (executor) `./stack clean` removed containers `bob-project-{bob-examples,local,wolf}` and
  all five volumes (`bob-project-{bob-examples,demo,local,marketing,wolf}`) in one pass — it filters
  by the `bob-project-` name prefix, not a fixed list, so no leftover volumes needed a manual
  `docker volume rm`. Schema reset as `app_bob` dropped 8 objects (events, project_secrets,
  projects, schedule_runs, schedules, schema_migrations, sessions, settings) — the old pre-baseline
  tables. `.env` is now a real file (`cp --remove-destination`) with `BOB_RUNTIME_TOKEN_DEV` and
  `BOB_PUBLIC_URL=http://localhost:8080` appended; the ops symlink target
  (`ops/secrets/bob/.env.local`) was read but not modified. All Validation commands pass: Go +
  runtime + web tests "all green"; compose config dummy-value check exits 0; `test -L .env` exits 1;
  `git status --short` does not list `.env`; the runtime `/health` curl (port 8100, basic auth)
  returns `{"ok":true,"draining":false}`; `select name from projects` returns only `dev`.
  `./stack build` and the API code itself boot correctly (verified by running it directly with an
  alternate `BOB_ADDR` — `/healthz` answered `ok`). See the Discovered Issues Log: `./stack start`
  cannot bind `:8090` while Kai's Platinum project (`/home/kai/projects/bayesprice/Platinum`,
  container `platinum-development-carbon`) is running, because it publishes `0.0.0.0:8090` on the
  same laptop. Orchestrator correction: an earlier note called this another tenant of a sandbox; it
  is Kai's own project. `bob-project-dev` and
  the web app (`:8080`) are left running for Kai; the host API process is not, since it cannot bind.

### T4: Workers and project prompt in the store   [Status: done | Model: sonnet]
- **Scope:** Complete `api/internal/store/workers.go` exactly as in Interfaces → Go store (T4),
  replacing T2's minimal version. Tools are stored as jsonb `null` when nil; `Labels` is always
  `{}` for now.
- **Files:** `api/internal/store/{workers.go,workers_test.go}`.
- **Acceptance criteria:** create → 1 version; update → 2; delete → 3, and `WorkerVersions(id)`
  still returns all three; duplicate name → `ErrConflict`; deleting a worker nulls
  `sessions.worker_id` and removes its schedules; the project prompt round-trips with versions;
  empty `why` → error.
- **TDD:** yes.
- **Validation:** `./stack test` → "all green".
- **Depends on:** T1
- [x] done
- Notes: Implemented as scoped in `api/internal/store/workers.go` with tests in
  `workers_test.go`. `Labels` has no column yet (T18); `scanWorker` always sets it to `map[string]string{}`
  so callers never see nil. `DeleteWorker` locks the worker row (`FOR UPDATE`) before snapshotting
  it, so the version row always reflects what was actually deleted. Unique-violation detection
  uses `errors.As` against `*pgconn.PgError` (code `23505`), matching store.go's existing error
  style. `./stack test` all green; `go vet ./...` clean.

### T5: API — worker routes, prompt composition, plain chats   [Status: done | Model: sonnet]
- **Scope:** Add the worker and prompt routes (Interfaces → API HTTP) in `api/cmd/bob/workers.go`,
  registered in `http.go:92-116` with policy `member`; the signed-in email is `by`. Shared
  validation `validWorker(store.Worker) error`: name regex; engine `claude|codex`; effort per
  engine (`claude`: low, medium, high, xhigh, max; `codex`: minimal, low, medium, high, xhigh;
  empty always allowed); model `modelName` regex (`http.go:351`); `tools` only for claude. Make
  `validSettings` (`http.go:353-363`) engine-aware: `validSettings(w, engine, model, effort)`, used
  by `createSession` and `updateSession`. `api/cmd/bob/prompt.go`: `bobNote(project string) string`
  (exact text in Architecture) and `composePrompt(parts ...string) string` (joins non-empty parts
  with a blank line). `createSession`: with `worker` → look it up (400 if unknown), engine from it;
  with `worker: ""` → require `engine`. `runTurn` (`http.go:510`): plain chat → prompt =
  `composePrompt(bobNote, projectPrompt)`; worker chat → load by `worker_id`; refuse with a
  `bob.turn_failed` event, without calling the runtime, when the worker was deleted ("this chat's
  worker was deleted; start a new chat") or its engine differs from the session's ("this chat's
  worker now runs on <engine>; start a new chat"); otherwise prompt =
  `composePrompt(bobNote, projectPrompt, worker.Prompt)`, model = session override || worker
  model, effort likewise, tools = worker tools. `execute` (`schedules.go:103`) loads the worker by
  `sch.WorkerID`. Delete `syncProject` and `a.workers`. The session list fills `worker_removed`.
  Schedule create/update accept a worker **name** and resolve it to `worker_id` (400 if unknown).
- **Files:** `api/cmd/bob/{workers.go,workers_test.go,prompt.go,prompt_test.go,http.go,schedules.go}`,
  `access_test.go` (guards for the new routes).
- **Acceptance criteria:** composed prompts as above; a message to a removed-worker chat yields
  `bob.turn_failed` and no runtime call; `POST /api/projects/dev/sync` → 404; a codex session with
  effort `minimal` is accepted.
- **TDD:** yes — using the fake `containers` the tests already use.
- **Validation:** `./stack test` → "all green".
- **Depends on:** T2, T3, T4
- [x] done
- Notes: Implemented as scoped. `api/cmd/bob/workers.go` holds `validWorker`, the moved
  `validSettings(w, engine, model, effort)` and `modelName`-sharing worker/session validation, plus
  the worker and project-prompt HTTP handlers; `api/cmd/bob/prompt.go` holds `bobNote` and
  `composePrompt`. `turnRequest` (`http.go`) now composes `bobNote + project prompt (+ worker
  prompt)` every turn and refuses (`bob.turn_failed`, no runtime call) both when the worker was
  deleted ("this chat's worker was deleted; start a new chat") and when the worker's engine no
  longer matches the session's ("this chat's worker now runs on <engine>; start a new chat").
  `createSession` takes `worker: ""` + required `engine` for a plain chat, otherwise resolves the
  worker and validates settings against its engine. Schedule create/update accept `worker` (a
  name, replacing `worker_id` in the request body) and resolve it via `findWorkerID`, 400 on an
  unknown name. `reply` maps `store.ErrConflict` to 409. Updated the two existing T2/T4 turn tests
  in `http_test.go` to the new composed prompt and refusal wording. New tests:
  `cmd/bob/prompt_test.go` (bobNote exact text, composePrompt), `cmd/bob/workers_test.go` (CRUD +
  versions, duplicate → 409, validation matrix incl. per-engine effort and claude-only tools,
  project prompt GET/PUT, plain chat requires engine, codex effort `minimal` accepted, engine-changed
  refusal, schedule worker-name resolution incl. 400 on unknown), and guard rows in
  `access_test.go` for every new route. Validation: `go vet ./...` clean; `./stack test` → "all
  green"; full `go test ./... -v` (with `BOB_TEST_DATABASE_URL`) shows no SKIP/FAIL.

### T6: Web — workers page, editor, history, project prompt   [Status: done | Model: sonnet]
- **Scope:** Remove git from the UI: `SyncStatus` and the "No workers yet… git" text
  (`Sidebar.tsx:47,59-62`); "Sync from git" / "Last synced" / sync error
  (`Overview.tsx:31-71`) and "defined in workers/<name>.md" (`Overview.tsx:124-127`); the sync
  banner and `files_root` in `Files.tsx:36-37,82,98,147` (Files opens on the work folder root);
  `Chat.tsx`'s Welcome text "This chat gets its own branch of the project repository."; the
  "its git branch are removed" text at `ChatHeader.tsx:75`; `api.sync`, `Activity 'syncing'`;
  delete `ProjectSettings.tsx` and its use at `Overview.tsx:7,49`. In `web/src/api.ts`: `Project`
  becomes `{ name: string; prompt: string; created_at: string }`, `Worker` gains
  `id, labels, created_by, created_at, updated_by, updated_at`, `WorkerList` is deleted and
  `api.workers` returns `Worker[]`; update every user of `WorkerList` (`App.tsx`, `Sidebar.tsx`,
  `Overview.tsx`, `Files.tsx`, `Schedules.tsx:8,17,177`). Remove `p.repo_url` at `App.tsx:133`.
  New routes (`App.tsx` hash parsing): `#/p/<p>/workers` (Workers.tsx: list + project prompt card +
  "New worker"), `#/p/<p>/workers/new` and `#/p/<p>/workers/<name>` (WorkerEditor.tsx: name on
  create only; engine select; model; effort select filtered by engine; tools as a comma list,
  claude only; prompt textarea; a required "why" on save; delete asking for a why; version history
  showing who/when/why with each snapshot's prompt expandable), `#/p/<p>/prompt`
  (ProjectPrompt.tsx: textarea + why + history). Sidebar gains a "Workers" nav link. The worker
  list refetches when the window regains focus, so workers created by agents appear without a
  reload.
- **Files:** `web/src/{App,Sidebar,Overview,Files,Chat,ChatHeader,Schedules,api}.tsx|ts`, new
  `Workers.tsx`, `WorkerEditor.tsx`, `ProjectPrompt.tsx`; delete `ProjectSettings.tsx`.
- **Acceptance criteria:** a worker can be created, edited and deleted from the UI, each needing a
  why; history shows every version; `grep -rni "git\b\|sync" web/src --include=*.tsx` finds no
  user-facing text about git sync or branches.
- **TDD:** no (UI).
- **Validation:** `cd web && npx tsc -b` → clean; `cd web && npx oxlint` → no errors; manual on
  `./stack start`: create worker `helper` (claude) on `dev`, edit its prompt, see 2 versions.
- **Depends on:** T5
- [x] done
- Notes: Implemented as scoped: `App.tsx` now loads `workers: Worker[] | null` (refetched on
  project change and on window `focus`), routes `#/p/<p>/workers`, `#/p/<p>/workers/new`,
  `#/p/<p>/workers/<name>` and `#/p/<p>/prompt`. New `Workers.tsx` (list + project-prompt card +
  "New worker"), `WorkerEditor.tsx` (name locked after create, engine select, model text field,
  effort select filtered by engine — a local `codexEfforts` list beside the existing
  `claudeEfforts`/`claudeModels` from `engines/claude.ts`, since there is no codex client module
  yet — tools as a comma list for claude only, prompt textarea, required "why", delete with its own
  required "why", and an expandable version history from `GET …/workers/{name}/versions`), and
  `ProjectPrompt.tsx` (textarea + why + history). `ProjectSettings.tsx` deleted; `api.ts`'s
  `Project` and `Worker` match the Interfaces exactly (confirmed live, see below); `WorkerList` is
  gone; `api.workers` returns `Worker[]`. Sidebar/Overview/Files/Schedules/Chat/ChatHeader updated
  per scope; `Activity` ('starting'/'syncing') and the sync/`files_root` plumbing are removed
  outright rather than just their text, since nothing else used them once `api.sync` was deleted.
  Manual check (no browser click-through — a signed-in session needs Google sign-in, which this
  agent cannot do): minted a `bob_session` cookie for an admin email from `.env`'s
  `BOB_PROJECT_MAP` with a throwaway `api/cmd/t6mint` program using `auth.Auth.SetSession`
  (removed after use, confirmed by `git status`), then against the running `./stack start` API on
  `127.0.0.1:8070`: `POST /api/projects/dev/workers` created `helper` (claude), `PATCH
  …/workers/helper` changed its prompt, and `GET …/workers/helper/versions` returned exactly 2
  versions (`create` then `update`), each with `why`/`changed_by`/`changed_at` and a full
  snapshot — the same shape `WorkerEditor.tsx`'s `VersionList` renders. `GET /api/projects` also
  confirmed the new `Project` shape (`name`, `prompt`, `created_at`, no repo fields) live.
  `cd web && npx tsc -b` clean; `npx oxlint` reports 0 errors (warnings only, matching the
  pre-existing baseline pattern in this repo, e.g. `set-state-in-effect` on the same lines Schedules
  and App already had it); `./stack test` → "all green"; `grep -rni "git\b\|sync" web/src
  --include=*.tsx` matches only incidental "async"/"synchronously" substrings, no user-facing git
  or sync text (also fixed two the ticket's line refs didn't call out: `Schedules.tsx`'s "Bob pulls
  from git and starts a new chat" dialog text, and a stale "for sync status" doc-comment on `ui.tsx`'s
  `ago()`).

### T7: Web — plain chat and read-only removed-worker chats   [Status: done | Model: sonnet]
- **Scope:** Route `#/p/<p>/new/` (empty worker) opens a plain new chat: in `App.tsx:43-44` test
  `mode === 'new'` (not the truthiness of `id`) for the `chat` page, and at `App.tsx:103` render
  `NewChat` in plain mode when the worker is `''`. Sidebar: a "Chat" group above the workers with a
  + linking to `#/p/<p>/new/`; sessions with `worker === ''` are listed there and are **excluded**
  from the "removed workers" groups built at `Sidebar.tsx:28-32`. `NewChat` (`Chat.tsx:92-150`) in
  plain mode shows an engine select (claude/codex), model and effort, and creates the session with
  `{worker: "", engine, model, effort}`. Sessions with `worker_removed` keep the "removed" label
  (`Sidebar.tsx:73`) and the composer is disabled with "This chat's worker was deleted. Its history
  stays readable." `ChatHeader.tsx` shows "Plain chat" for plain sessions.
- **Files:** `web/src/{App,Sidebar,Chat,ChatHeader,api}.tsx|ts`.
- **Acceptance criteria:** a plain Claude chat starts and answers; a chat whose worker was deleted
  cannot send.
- **TDD:** no.
- **Validation:** `cd web && npx tsc -b` → clean; manual on `./stack start`: plain chat on `dev`,
  "say hi" → reply; delete worker `helper` (T6) → its chat's composer is disabled.
- **Depends on:** T6
- [x] done
- Notes: Fixed `App.tsx`'s `mode === 'new'` test and the `NewChat` render condition to test
  `mode === 'new'` directly rather than the truthiness of `newWorker` (empty for a plain chat, so
  the old `session || newWorker` and `project && newWorker` checks both silently dropped the plain
  route). `Chat`'s single-session GET (`api.session`) doesn't carry `worker_removed` (list-only per
  `store.go`), so `App.tsx`'s `SessionView` now takes the loaded `workers` list too and derives
  "removed" itself (`session.worker !== '' && workers !== null && !findWorker(...)`), passed to
  `Chat` as a `removed` prop. Composer disabling uses `useExternalStoreRuntime`'s own `isDisabled`
  option (it already flows into `ComposerPrimitive.Input`'s `disabled`, confirmed by reading
  `@assistant-ui/react`'s `useComposerInputState.js`), with the required text as the composer's
  `placeholder` (visible while disabled) plus a guard in `onNew`. Sidebar's per-chat `<ul>` was
  factored into a `ChatList` component, reused by both the new "Chat" (plain) group and each
  worker group, since both now need it. A plain new chat (`NewChat`'s `plain` prop) picks its own
  engine (claude/codex) and settings via a small `PlainChatBar` in `Chat.tsx`; per the task's note,
  **codex has no model list yet (T12), so its model is free text — `WorkerEditor.tsx`'s existing
  `codexEfforts` precedent is followed for effort (a select), and claude keeps the existing
  `claudeModels`/`claudeEfforts` selects**. `api.createSession` gained an `engine` parameter (the
  server already accepted and required it for `worker: ''`, and ignores it when a worker is given,
  per `cmd/bob/http.go`'s `createSession`). Manual check (browser UI not clicked through; API only,
  cookie minted the way T6 did, deleted from the scratchpad after use): plain claude chat on `dev`
  → POST `.../sessions {worker:"",engine:"claude"}`, sent "say hi", polled events → `bob.turn_done`
  with the assistant's reply "Hi! What can I help you with today?" (no `bob.turn_failed`). Created
  a session on worker `helper`, then `DELETE .../workers/helper?why=...`; the project's session
  list then showed that chat with `worker_removed: true`; sending it a message returned the
  `bob.user_message` event as usual but the turn then produced `bob.turn_failed` with
  `{"error":"this chat's worker was deleted; start a new chat"}`, confirming the server-side half
  T7 relies on (already true before this ticket — T7 only makes the web read it).

### T8: MCP server in the API   [Status: done | Model: opus]
- **Scope:** `api/internal/mcp/server.go` per Interfaces → MCP: JSON-RPC 2.0 over POST —
  `initialize` (protocolVersion `2025-06-18`, capabilities `{tools:{}}`, serverInfo
  `{name:"bob"}`), `notifications/initialized` (202, empty body), `ping`, `tools/list` (only tools
  whose `Available(caller)` is true), `tools/call` (result `{content:[{type:"text",text:<JSON of
  the result>}]}`; a tool error → `{isError:true, content:[{type:"text",text:<message>}]}`; unknown
  or unavailable tool → JSON-RPC error `-32602`); body limit 8 MiB; `GET` → 405. Read agent-bob
  `go/cmd/agentd/mcpserver.go` for structure. `api/cmd/bob/mcp.go`: `mcpToken(secret []byte,
  sessionID string) string` = `sessionID + "." + hex(HMAC-SHA256(secret, "bob-mcp\x00"+sessionID))`;
  `verifyMCPToken`; the auth func (Bearer → verify, constant-time → load session (404 if gone) →
  `Caller` with `Project`, `SessionID`, `Worker`, `User`, `APIBase: "http://"+r.Host`; bad token →
  401). Mount `POST /mcp` outside `requireLogin` in `http.go:118-129`. `runTurn` sets
  `TurnRequest.MCPToken`.
- **Files:** `api/internal/mcp/{server.go,server_test.go}`, `api/cmd/bob/{mcp.go,mcp_test.go,http.go}`.
- **Acceptance criteria:** wrong or tampered token → 401; token for a deleted session → 404;
  `tools/list` lists only available tools; unknown tool → `-32602`.
- **TDD:** yes.
- **Validation:** `./stack test` → "all green".
- **Depends on:** T1
- [x] done
- Notes: (executor) Implemented as scoped. `internal/mcp/server.go`: `Caller`, `Tool`, `New`,
  `Register` (panics on a nameless/Call-less or duplicate tool), `Handler`; auth errors are
  `mcp.ErrUnauthorized` → 401 (+ `WWW-Authenticate: Bearer`) and `mcp.ErrNoSession` → 404, others
  500. Also: malformed JSON → -32700 (id null), unknown method → -32601, body over 8 MiB → -32600,
  any request without an id → 202. `serverInfo` also carries `version: "1"` (the MCP spec requires
  it; strict clients reject its absence). `/mcp` is mounted as `"/mcp"` (every method) on the outer
  mux so the handler itself answers GET with 405. `app.mcp` holds the server (`mcp.New()` in
  `main.go`; `mux` falls back to an empty one for tests). `turnRequest` sets `MCPToken`; the
  expectation in `TestTurnCarriesTheWorkersSettings` changed from `""` to the minted token, and
  `newScheduleApp` now sets `a.auth` (a secret to mint with). Tests: `internal/mcp/server_test.go`
  (initialize, notification 202, ping, tools/list filtering, call result/isError, -32602 for
  unknown and unavailable tools, -32601/-32700/-32600, 401/404/405, duplicate register) and
  `cmd/bob/mcp_test.go` (`TestMCPToken`, `TestMCPCaller`, `TestMCPRefusesBadTokens`,
  `TestTurnCarriesTheMCPToken`). After `./stack restart`, `curl -X POST :8070/mcp -d '{}'` → 401;
  `GET /mcp` → 405.

### T9: Runtime gives Claude the Bob MCP server   [Status: done | Model: sonnet]
- **Scope:** `runtime/src/mcp.ts` exports `bobMcp` (Interfaces). `claude.ts` passes
  `mcpServers: { bob: { type: 'http', ...bobMcp(API_URL, turn.mcpToken) } }` to `query()`. Register
  `bob_whoami` in `api/cmd/bob/mcp.go` returning `{project, session, worker, user}`.
- **Files:** `runtime/src/{mcp.ts,mcp.test.ts,claude.ts,server.ts,turn.ts}`, `api/cmd/bob/mcp.go`.
- **Acceptance criteria:** in a dev chat, "call the bob_whoami tool" returns `dev` and that chat's id.
- **TDD:** yes for `mcp.ts`; manual end to end.
- **Validation:** `cd runtime && rm -rf dist && npm run build && npm test` → pass; `./stack build &&
  ./stack restart`; the manual chat above.
- **Depends on:** T3, T7, T8
- [x] done
- Notes: (executor) `runtime/src/mcp.ts` + `mcp.test.ts` exactly as scoped (node:test, matching the
  runtime's style — this repo doesn't use vitest). `runtime/src/claude.ts`'s `runClaudeTurn` now
  takes `apiUrl` and passes `mcpServers: { bob: { type: 'http', ...bobMcp(apiUrl, turn.mcpToken) } }`
  to `query()` (confirmed against the installed SDK's `sdk.d.ts`: `McpHttpServerConfig` is exactly
  `{type:'http', url, headers?, tools?, timeout?, alwaysLoad?}`, so `{type:'http', ...bobMcp(...)}`
  matches). `server.ts`'s `Driver` type and the one call site thread `API_URL` through to the driver
  (it wasn't reaching `claude.ts` before). `api/cmd/bob/mcp.go`: `bobWhoami` (`mcp.Tool`) returns
  `{project, session, worker, user}` from `Caller`; registered in `main.go` right after `mcp.New()`.
  Go test `TestBobWhoami` (`cmd/bob/mcp_test.go`) calls it through `POST /mcp` with a minted token
  and checks the JSON result. Manual e2e (no Google sign-in): a throwaway `/tmp` program signed a
  `bob_session` cookie the same way `auth.Auth.sign` does (reading `BOB_SESSION_SECRET` from `.env`,
  never printed), used it to create a plain claude chat on `dev` via the real API, sent "Call the
  bob_whoami tool and tell me exactly what it returned.", and polled `GET /api/sessions/{id}/events`
  until `bob.turn_done`. The transcript shows `mcp__bob__bob_whoami` called with `{}` and returning
  `{"project":"dev","session":"<that chat's id>","user":"kaiyadavenport@gmail.com","worker":""}`,
  which the model then quoted back verbatim — acceptance criterion met.

### T10: Worker MCP tools   [Status: done | Model: sonnet]
- **Scope:** `api/cmd/bob/tools_workers.go`: `worker_create`, `worker_update`, `worker_list`,
  `worker_delete` (Interfaces, without labels until T18), using `validWorker` from T5. Descriptions
  tell the agent: names are permanent; `why` is recorded; the prompt is the worker's whole job
  description; the Bob note and project prompt are prepended automatically.
- **Files:** `api/cmd/bob/{tools_workers.go,tools_workers_test.go}`.
- **Acceptance criteria:** a plain chat creates a worker that appears in the UI (after focus) with
  one version whose `changed_by` is the person who sent the message.
- **TDD:** yes.
- **Validation:** `./stack test` → "all green"; manual: plain chat on `dev`, "create a claude worker
  named poet that writes haiku; why: test" → it appears under Workers with 1 version.
- **Depends on:** T5, T9
- [x] done
- Notes: implemented `api/cmd/bob/tools_workers.go` (`(a *app) workerTools() []mcp.Tool`, registered
  in `main.go` next to `bob_whoami`) and `api/cmd/bob/tools_workers_test.go`. `worker_create` and
  `worker_update` share a `workerToolArgs` struct and `validWorker`/the store methods from T5/T4;
  `worker_list` returns a `workerSummary` (no prompt) unless called with `name`, which returns the
  full `store.Worker`; `worker_delete` requires `why` itself (the store also enforces it, but a
  tool-side check gives a clearer message before the store round-trip). All four scope to
  `Caller.Project` only — no argument names a project — so another project's worker is invisible to
  `worker_list` (by name or in the listing) and cannot be updated or deleted. TDD: failing tests
  first in `tools_workers_test.go` covering create+version(changed_by), duplicate name (tool
  error), bad engine/effort (tool error), update, list (summary vs. full-with-name), delete
  (missing why → tool error; with why → gone), and cross-project isolation (list by name, plain
  list, delete all refuse/omit the other project's worker, which is left untouched). Validation:
  `go vet ./...` clean; `BOB_TEST_DATABASE_URL=... go test ./... -count=1 -v` — all pass, no
  SKIP/FAIL; `./stack test` → "all green". Manual check after `./stack restart`: minted a
  `bob_session` cookie for kaiyadavenport@gmail.com with a throwaway `api/cmd/minttmp` program
  (deleted afterwards, along with the cookie file), created a plain chat on `dev` via
  `POST /api/projects/dev/sessions`, and sent "Create a claude worker named poet that writes haiku
  about whatever it is given. why: test of worker_create" via `POST /api/sessions/{id}/messages`.
  `GET /api/projects/dev/workers` shows `poet` with the haiku prompt the agent wrote; its
  `/versions` shows exactly one version, `action: "create"`, `changed_by:
  "kaiyadavenport@gmail.com"`, `why: "test of worker_create"`.

### T11: Codex driver in the runtime   [Status: in progress — code committed; live check waits on Kai's codex login | Model: opus]
- **Scope:** Add `@openai/codex-sdk` to `runtime/package.json`. In `runtime/Dockerfile` put the
  bundled CLI on the PATH: `RUN ln -s /app/node_modules/.bin/codex /usr/local/bin/codex`.
  **First verify** against the installed SDK's types and Codex docs, recording each answer in
  Notes: (a) per-run config overrides (expected `new Codex({ config: {...} })`); (b) the key for
  extra instructions (expected `developer_instructions`); (c) HTTP MCP config (expected
  `mcp_servers.bob.url` + `mcp_servers.bob.http_headers`, possibly also
  `features.rmcp_client = true`); (d) web search (expected `web_search = "live"` or thread option
  `webSearchEnabled`); (e) thread options `workingDirectory`, `skipGitRepoCheck`,
  `sandboxMode: 'danger-full-access'`, `approvalPolicy: 'never'`, `model`, `modelReasoningEffort`;
  (f) the SDK's `env` option replaces the environment, so pass `{...process.env, ...turnEnv(turn)}`
  (`turn.ts:20-34`). Where an expectation is wrong, use what the SDK supports and log it in the
  Discovered Issues Log. `runtime/src/codex.ts`: `runCodexTurn(turn, emit, signal)` —
  `startThread` / `resumeThread(turn.resume)`, `runStreamed(turn.text, {signal})`, `emit` every
  event unchanged; `harnessSessionId` = the `thread.started` event's `thread_id` (or `turn.resume`);
  `turn.failed` / `error` → `TurnResult.error`. If `$CODEX_HOME/auth.json` is missing, return the
  error exactly: `Codex is not logged in for this project. On the box run: docker exec -it
  bob-project-<name> codex login --device-auth`. Register `codex: runCodexTurn` in `server.ts`.
- **Files:** `runtime/{package.json,package-lock.json,Dockerfile}`, `runtime/src/{codex.ts,codex.test.ts,server.ts}`.
- **Acceptance criteria:** a codex turn streams events and returns a thread id; the next turn with
  `resume` continues it; a missing login gives the exact message.
- **TDD:** yes for missing-login and event pass-through (inject a fake Codex client).
- **Validation:** `cd runtime && rm -rf dist && npm run build && npm test` → pass;
  `./stack build && ./stack restart`; `docker exec bob-project-dev codex --version` → prints a
  version; Kai runs `docker exec -it bob-project-dev codex login --device-auth`; T12's manual check.
- **Depends on:** T9
- [ ] done
- Notes: (executor) SDK verification against `@openai/codex-sdk` 0.158.0 (`dist/index.d.ts`,
  `dist/index.js`, README) and the bundled `codex-cli 0.158.0` (`--help`, and probes in a throwaway
  `CODEX_HOME`): **(a)** yes — `new Codex({ config })`; the SDK flattens the object to dotted
  `--config key=<TOML>` flags on `codex exec` (per run; nothing is written to `config.toml`).
  **(b)** yes — `developer_instructions` is a top-level config key (`codex exec --strict-config`
  accepts it and rejects a made-up key). **(c)** yes — `mcp_servers.bob.url` +
  `mcp_servers.bob.http_headers.Authorization` (`codex mcp get bob --json` with those overrides
  shows `transport: streamable_http` with the header); `features.rmcp_client` is **not** needed and
  no longer exists (not in `codex features list`). **(d)** both exist: thread option
  `webSearchMode: 'live'` (→ `--config web_search="live"`) or `webSearchEnabled: true` (same);
  used `webSearchMode: 'live'`. **(e)** all six are `ThreadOptions` exactly as named;
  `sandboxMode` → `--sandbox`, `approvalPolicy` → `--config approval_policy=…`,
  `modelReasoningEffort` → `--config model_reasoning_effort=…` (SDK type: `minimal | low | medium |
  high | xhigh | max | ultra | persistent`). **(f)** confirmed — with `env` set the SDK copies only
  it (plus its own `CODEX_INTERNAL_ORIGINATOR_OVERRIDE`), so the driver passes
  `{...process.env (defined keys), ...turnEnv(turn)}`. Also: `resumeThread(id, options)` runs
  `codex exec … resume <id>` with the same thread options, and the SDK finds the CLI through its own
  `node_modules` resolution (not `PATH`), so the Dockerfile symlink is only for `docker exec`.
  Implementation: `runtime/src/codex.ts` `runCodexTurn(turn, apiUrl, emit, signal, deps?)` — same
  shape as `runClaudeTurn` (the `apiUrl` argument is what `server.ts`'s `Driver` passes); `deps`
  injects a fake client, the codex home and the project name for tests. Empty `system_prompt` →
  no `developer_instructions`. `server.ts` registers `codex: runCodexTurn`; Dockerfile symlinks
  `/usr/local/bin/codex`. TDD (`runtime/src/codex.test.ts`, 5 tests, failing first against a stub):
  missing `auth.json` → the exact message with the project name and the client is never built;
  events passed through unchanged + thread id from `thread.started` + config/env/thread options;
  `resume` → `resumeThread`; `turn.failed` → error while a recovered retry notice is not; a stream
  ending in `error` or a dying CLI fails the turn but keeps the thread id. Validation: runtime
  build + `npm test` → 15 pass; `./stack test` → all green; after `./stack build && ./stack
  restart`, `docker exec bob-project-dev codex --version` → `codex-cli 0.158.0`;
  `/project/.bob/codex` is on the `bob-project-dev` volume (mounted at `/project`), owned
  `node:node` (uid 1000, the container user); a plain codex chat on `dev` (minted cookie, program
  deleted) sent "say hi" → events `bob.user_message, bob.turn_failed` with
  `{"error":"Codex is not logged in for this project. On the box run: docker exec -it
  bob-project-dev codex login --device-auth"}`. Stopped there: Kai runs
  `docker exec -it bob-project-dev codex login --device-auth`; the resume/streaming acceptance
  criteria are checked by T12's manual check after that.

### T12: Codex in the API and the web   [Status: in progress — code committed; live check waits on Kai's codex login | Model: sonnet]
- **Scope:** `api/internal/engines/engines.go`: for `codex`, `kind` = event `type`; ephemeral when
  the type is `item.started` or `item.updated`. `web/src/engines/codex.ts`:
  `codexMessages(events, live)` → assistant-ui messages: `agent_message` → text; `reasoning` →
  reasoning; `command_execution`, `mcp_tool_call`, `file_change`, `web_search` → tool-call parts
  with their output; `todo_list` → a text list; `error` / `turn.failed` → error text; plus
  `bob.user_message` and `bob.turn_failed` handled as in `claude.ts`. Export `codexModels` and
  `codexEfforts = ['minimal','low','medium','high','xhigh']`; take the model ids from
  `docker exec bob-project-dev codex --help` / the Codex docs and record them in Notes. `Chat.tsx`
  picks the converter by `session.engine`; the model and effort pickers in `NewChat`,
  `ChatHeader` and `WorkerEditor` use the engine's lists.
- **Files:** `api/internal/engines/{engines.go,engines_test.go}`, `web/src/engines/codex.ts`,
  `web/src/{Chat,ChatHeader,WorkerEditor}.tsx`.
- **Acceptance criteria:** a Codex plain chat on `dev` shows its reply, its commands and a Bob tool
  call; reloading shows the same conversation from stored events.
- **TDD:** yes for `engines_test.go`; UI manual.
- **Validation:** `./stack test` → "all green"; `cd web && npx tsc -b` → clean; manual: Codex plain
  chat on `dev`, "run `ls /project` and call bob_whoami" → both shown, and still shown after reload.
- **Depends on:** T7, T11
- [ ] done
- Notes: (executor) `engines.go`: `codex`'s `kind` is the event `type` (no `subtype` in Codex's
  events); ephemeral (streamed live, not stored) for `item.started`/`item.updated` — the in-progress
  snapshot of a running item — and stored for everything else, `item.completed` in particular.
  TDD: `engines_test.go` gained 5 cases (`thread.started`/`item.started`/`item.updated`/
  `item.completed`/`turn.completed`), failing first on the two ephemeral ones, then passing.
  `web/src/engines/codex.ts` mirrors `claude.ts`'s shape: `bob.user_message` opens a new turn/reply
  id (same flicker-avoidance reasoning); `item.completed` items become message parts —
  `agent_message` → text, `reasoning` → reasoning, `command_execution`/`mcp_tool_call`/
  `file_change`/`web_search` → tool-call parts (result = aggregated output / MCP result-or-error
  text / changed paths / empty, `isError` from the item's `status`), `todo_list` → a `[x]/[ ]` text
  list, item-level `error` → `⚠️` text. Also handles the driver's own `error` (T11's Notes: a
  non-fatal retry notice or, if nothing recovers it, the last one shown) and `turn.failed` events as
  `⚠️` text, plus `bob.turn_failed` as `claude.ts` does. No `codexDelta`/live-typing accumulator was
  added — `item.started`/`item.updated` are ephemeral and not wired into `Chat.tsx`'s `live` state
  for codex (only `claude`'s `stream_event` deltas are); a Codex reply appears whole per
  `item.completed`, not token-by-token. `codexModels`: ran `docker exec bob-project-dev codex debug
  models` (a JSON list on `codex-cli 0.158.0`) and kept the ids with `"visibility":"list"` — the set
  Codex's own picker offers — in the order given: `gpt-6-astra, gpt-6-sol, gpt-6-luna, gpt-5.6-sol,
  gpt-5.6-terra, gpt-5.6-luna, gpt-5.5`; left out ids marked `"visibility":"hide"`
  (`gpt-daybreak-blue-latest`, `gpt-daybreak-red-latest`, `codex-auto-review`). `codexEfforts` is
  exactly the ticket's fixed list (`minimal, low, medium, high, xhigh`), unchanged from T6/T11's
  `workers.go` list — logged as a Discovered Issue below since `codex debug models` shows no model
  actually supporting `minimal` (all list `low…xhigh`, most also `max`/`ultra`) and T11's Notes
  already flagged the SDK allowing `max`/`ultra`/`persistent` too. `Chat.tsx`: `toMessages` now
  routes `codex` to `codexMessages`; the plain-chat engine bar's per-engine free-text codex
  model/effort inputs are replaced with the same `Menu` pickers Claude uses, driven by a
  `plainOptions` map (`claude`/`codex` → their model+effort lists); the old local `codexEfforts`
  duplicate is gone. `ChatHeader.tsx`'s `options` map gained a `codex` entry (`codexModels`,
  `codexEfforts`) so a chat's per-chat override menu offers real choices instead of showing an empty
  list. `WorkerEditor.tsx`: added `modelsFor(engine)`; the Model field, free text before, is now a
  `<select>` like Effort, offering `modelsFor(form.engine)` plus the worker's current value as an
  extra option when it is not one of the list (so an out-of-list or legacy value is never silently
  dropped); its own duplicate `codexEfforts` constant is gone in favor of importing from
  `engines/codex.ts`. Validation: `go test ./internal/engines/...` red then green on the TDD cases;
  `./stack test` → "all green"; `cd web && npx tsc -b` → clean (after fixing `args` typing in the
  new tool-call parts — assistant-ui's `ReadonlyJSONObject` doesn't accept `Record<string,
  unknown>`, cast as `any` like the rest of the codebase does for tool `args`); `./stack build &&
  ./stack restart` → API on :8070, web on :8080. The manual check (a live Codex plain chat on `dev`)
  was not run: Codex is not logged in yet in `bob-project-dev` (T11 stopped there for Kai's
  `codex login --device-auth`), and logging in was out of scope here.

### T13: Skills folder for both harnesses   [Status: in progress — code committed, Claude half checked; Codex half waits on Kai's codex login | Model: sonnet]
- **Scope:** `runtime/src/skills.ts`: `linkSkills(skillsDir: string, targets: string[])` makes each
  target a symlink to `skillsDir` (replaces a stale symlink; leaves a real directory alone and logs
  it). `server.ts` calls it at startup with `[$CLAUDE_CONFIG_DIR/skills, $CODEX_HOME/skills]`.
  Verify Codex's skills lookup path in its docs (expected `$CODEX_HOME/skills`) and record it in
  Notes. `claude.ts` keeps `skills: 'all'`.
- **Files:** `runtime/src/{skills.ts,skills.test.ts,server.ts}`.
- **Acceptance criteria:** a skill in `/project/skills/haiku/SKILL.md` is listed by a Claude chat
  and a Codex chat.
- **TDD:** yes.
- **Validation:** `cd runtime && rm -rf dist && npm run build && npm test` → pass; manual:
  `docker exec bob-project-dev sh -c 'mkdir -p /project/skills/haiku && printf -- "---\nname: haiku\ndescription: Write a haiku\n---\nWrite one haiku.\n" > /project/skills/haiku/SKILL.md'`,
  `./stack build && ./stack restart`, ask each engine "what skills do you have?" → both name `haiku`.
- **Depends on:** T11
- [ ] done
- Notes: (executor) Confirmed `$CLAUDE_CONFIG_DIR`/`$CODEX_HOME` from `runtime/Dockerfile:25-26`
  (`/project/.bob/claude`, `/project/.bob/codex`); `entrypoint.sh` already `mkdir -p`s both plus
  `$P/skills` before starting the server, so `linkSkills`'s own `mkdirSync(dirname(target), {
  recursive: true })` is belt-and-braces. `linkSkills(skillsDir, targets, log?)` implemented exactly
  as scoped; wired into `server.ts` right after `WORK_DIR`, building targets from
  `[CLAUDE_CONFIG_DIR, CODEX_HOME].filter(Boolean).map(h => join(h, 'skills'))` so a missing env var
  (e.g. in a future non-Docker run) is skipped rather than crashing. `claude.ts` untouched — already
  `skills: 'all'`.
  Codex's skills lookup path: verified by grepping printable strings in the bundled
  `codex-linux-x64` binary inside `bob-project-dev`
  (`@openai/codex-linux-x64/vendor/x86_64-unknown-linux-musl/bin/codex`, no `strings` binary in the
  container so used `grep -a -o`): the string table contains literal `CODEX_HOME/skills`,
  `HOME/.codex}/skills` and `CODEX_HOME/skills/.system/imagegen/scripts/image_gen.py`, plus a
  `skills_config.rs`/`skills.rs` config surface with `allow_symlinked_codex_home` — confirming both
  the expected path and that Codex is fine with `$CODEX_HOME` itself (or its `skills` subfolder)
  being a symlink.
  Validation: `cd runtime && rm -rf dist && npm run build && npm test` → `# pass 20` / `# fail 0`
  (16 pre-existing + this ticket's 5 new `skills.test.ts` cases). `./stack test` → ends `── all
  green`. Manual: created `/project/skills/haiku/SKILL.md` in `bob-project-dev`, `./stack build &&
  ./stack restart`; `docker exec bob-project-dev ls -la $CLAUDE_CONFIG_DIR $CODEX_HOME` shows
  `skills -> /project/skills` in both. Asked a **Claude** plain chat on project `dev` "What skills do
  you have? List their names." (via a minted `bob_session` cookie — HMAC-signed the same way
  `api/internal/auth/auth.go`'s `SetSession` does, in a throwaway Go program in the scratchpad,
  deleted after use; session created and deleted via the API, never printed
  `BOB_SESSION_SECRET`/`BOB_PROJECT_MAP`). The `system.init` event's `skills` array led with
  `"haiku"`, and Claude's reply led with `1. **haiku**` among its skill list — acceptance criterion
  met for the Claude half. **Codex's half was skipped**: Codex is not logged in for the `dev`
  project (`bob-project-dev` has no `auth.json` in `$CODEX_HOME`), and logging in requires Kai's own
  device-auth flow, which this run does not attempt. The `CODEX_HOME/skills` symlink itself was
  confirmed to exist and resolve correctly, so once Kai runs `docker exec -it bob-project-dev codex
  login --device-auth`, a Codex chat asking the same question should be the only remaining check.

### T14: Drive client, OAuth client and token script   [Status: in progress — code committed; waits on Kai's OAuth client + drive-token run | Model: sonnet]
- **Scope:** **Human step (Kai)**: in a Google Cloud project, create an OAuth client of type
  "Desktop app", enable the Drive API, set the OAuth consent screen's publishing status to
  **"In production"** (an unverified app is fine for under 100 users; people click through the
  warning). Put its id and secret in `.env` as `BOB_DRIVE_CLIENT_ID` / `BOB_DRIVE_CLIENT_SECRET`.
  `scripts/drive-token` (Node 22, no dependencies): loopback OAuth with PKCE, scope
  `drive.readonly`, `access_type=offline&prompt=consent`; prints the refresh token and the account
  email. `api/internal/drive/drive.go`: `New(clientID, secret, refreshToken string) *Client` using
  `golang.org/x/oauth2` and `google.golang.org/api/drive/v3`; `Search(ctx, q string, limit int)`
  (`fullText contains '<escaped q>' and trashed = false`, `corpora=allDrives`,
  `supportsAllDrives`, `includeItemsFromAllDrives`); `List(ctx, folderID, pageToken string)`
  (`'<id>' in parents and trashed = false`; empty folder → `('root' in parents or sharedWithMe)`);
  `Read(ctx, fileID string) (name, mime, text string, err error)` with the export rules in
  Interfaces; `Download(ctx, fileID) (io.ReadCloser, name, mime string, err error)`. An
  `invalid_grant` error → "the Drive connection for this project was revoked or expired; re-run
  scripts/drive-token and update BOB_DRIVE_TOKEN_<NAME>". `main.go` builds a client for each
  project with a `BOB_DRIVE_TOKEN_<NAME>` (fatal if the client id/secret are missing then).
- **Files:** `scripts/drive-token`, `api/internal/drive/{drive.go,drive_test.go}`,
  `api/{go.mod,go.sum}`, `api/cmd/bob/main.go`, `deploy/env.example`, `.env`.
- **Acceptance criteria:** tests against an `httptest` fake Drive cover query escaping, the export
  MIME per file type, and the revoked-token message.
- **TDD:** yes.
- **Validation:** `./stack test` → "all green"; manual: `node scripts/drive-token` prints a refresh
  token for Kai's test account; put it in `.env` as `BOB_DRIVE_TOKEN_DEV`.
- **Depends on:** T1
- [ ] done
- Notes:
  `golang.org/x/oauth2@v0.30.0` and `google.golang.org/api/drive/v3@v0.244.0` added by `go get`
  (letting it pick `@latest` bumped `go.mod`'s `go` directive to 1.26.0, which `golang:1.25-bookworm`
  — this repo's build image — cannot compile; pinned to these versions instead, which keep `go
  1.25.0`). `api/internal/drive/drive.go`: `New(ctx, clientID, secret, refreshToken string, opts
  ...Option)` (the `ctx` param and variadic `Option`s are not in the ticket's exact signature —
  needed so tests can override the token and API endpoints without a package-level var; production
  callers just pass no options). `Option`s: `WithTokenEndpoint`, `WithAPIEndpoint` (test-only,
  documented as such). A revoked/expired token is caught two ways: `errors.As` for
  `*oauth2.RetrieveError{ErrorCode: "invalid_grant"}` (the token-exchange failure) and for
  `*googleapi.Error{Code: 401}` (an access token that stops working mid-session) — both produce the
  ticket's exact message. `drive_test.go`'s fake serves the OAuth token endpoint and the Drive REST
  API from the same `httptest.Server`, covering: query escaping (backslash, `'`, `"`), the `corpora
  allDrives` / `supportsAllDrives` / `includeItemsFromAllDrives` params, `List`'s parent and
  empty-folder queries, `Read`'s three export MIME types (Docs→markdown, Sheets→CSV,
  Slides→plain text) plus a `text/*` file read as bytes and an unreadable MIME type erroring with
  "drive_fetch", `Download`, and both revoked-token cases above. `main.go`:
  `driveClientsFromEnv(ctx, getenv, names)` builds a `map[string]*drive.Client`, one entry per
  project with `BOB_DRIVE_TOKEN_<NAME>` set (`drive.TokenVar`, mirroring
  `runtime.TokenVar`); fatal (naming both `BOB_DRIVE_CLIENT_ID`/`BOB_DRIVE_CLIENT_SECRET`) if a
  token is set but the OAuth client id/secret is not. Stored on `app.drive` (a plain map, no
  mutex — built once at boot and never written after, same as `app.runtime`), for T15's MCP tools
  to key by the caller's project. No project has a Drive token today, so `driveClientsFromEnv`
  returns an empty map and startup is exactly as before (`./stack build && ./stack restart` and
  `curl http://127.0.0.1:8070/healthz` → 200, confirmed).
  `scripts/drive-token`: no dependencies beyond Node 22's own `http`/`crypto`/`child_process`;
  `node --check` on it directly refuses the shebang (no `.js` extension), so checked a copy in the
  scratchpad instead (removed after). Not run against a real Google OAuth client — that needs
  Kai's own Cloud project and consent screen (human step, skipped per this ticket's scope) — so the
  manual "prints a refresh token for Kai's test account" step in Validation was not performed.
  Validation: `./stack test` → ends `── all green`; `go vet ./...` → clean; `go test ./... -count=1
  -v | grep -cE -- "--- (SKIP|FAIL)"` → `0`; `./stack build && ./stack restart` →
  `curl http://127.0.0.1:8070/healthz` → `200`.

### T15: Drive MCP tools and signed fetch   [Status: in progress — code committed; live check waits on a Drive token (T14) | Model: sonnet]
- **Scope:** `api/cmd/bob/tools_drive.go`: `drive_search`, `drive_list`, `drive_read`,
  `drive_fetch` (Interfaces), each with `Available` = the caller's project has a Drive client.
  `drive_read` pages text by `offset`/`limit`. `drive_fetch` builds
  `url = c.APIBase + "/drive/fetch/" + token`, where `APIBase` is the scheme `http` plus the `Host`
  header of the `/mcp` request (so `api:8070` on the box, `host.docker.internal:8070` on the
  laptop) — never `BOB_PUBLIC_URL`. Token = base64url(`project|fileID|expUnix`) + `.` +
  hex(HMAC-SHA256(BOB_SESSION_SECRET, `"drive-fetch\x00"` + payload)), expiry 10 minutes.
  `api/cmd/bob/drivefetch.go`: `GET /drive/fetch/{token}` outside `requireLogin` — verify the HMAC
  (constant time) and expiry (403 otherwise), stream `Download` with
  `Content-Disposition: attachment; filename="<name>"`.
- **Files:** `api/cmd/bob/{tools_drive.go,tools_drive_test.go,drivefetch.go,http.go}`.
- **Acceptance criteria:** a project without a Drive token does not see `drive_*` in `tools/list`;
  a tampered or expired fetch token → 403.
- **TDD:** yes.
- **Validation:** `./stack test` → "all green"; manual (with `BOB_DRIVE_TOKEN_DEV` set and
  `./stack restart`): a dev chat lists a Drive folder, reads a Google Doc as markdown, and
  `drive_fetch` + `curl` saves a PDF into `/project/work`.
- **Depends on:** T8, T9, T14
- [ ] done
- Notes: (executor) `api/cmd/bob/tools_drive.go` registers `drive_search`, `drive_list`,
  `drive_read`, `drive_fetch` with `Available` = `a.drive[c.Project]` present (same pattern as
  T10's worker tools). `drive_fetch` calls `Client.Download` once to get `name`/`mime_type` (no
  metadata-only method exists on `drive.Client`), closes the body immediately without reading it,
  then mints the token — the real bytes are only streamed by `GET /drive/fetch/{token}`
  (`drivefetch.go`), which calls `Download` again. `drive_read`'s `offset`/`limit` page over
  `[]rune(text)`, not bytes, so multi-byte UTF-8 text pages correctly; default `limit` 50000,
  `offset`/`limit` clamped to `[0, total_chars]`. `drivefetch.go`: `signDriveFetchToken` /
  `verifyDriveFetchToken` implement the ticket's exact token shape; `verifyDriveFetchToken` returns
  the same `ok=false` for a malformed token, a bad HMAC (checked with `hmac.Equal`, constant time)
  and an expired one, so `GET /drive/fetch/{token}` always answers 403 without distinguishing why.
  `GET /drive/fetch/{token}` is registered on the top-level mux next to `/api/view/{token}`, outside
  `a.requireLogin(api)`, so it takes no cookie. `mcpCaller` already builds `Caller.APIBase` as
  `"http://" + r.Host` (from T8/T9); `drive_fetch` uses it unchanged, never `BOB_PUBLIC_URL`.
  `api/cmd/bob/tools_drive_test.go`: a fake Drive server (`httptest.Server`, OAuth token endpoint +
  a `/files`/`/files/{id}` REST fake) built the same way as `internal/drive/drive_test.go`'s, but a
  real `*drive.Client` is pointed at it with `drive.WithTokenEndpoint`/`drive.WithAPIEndpoint` (T14)
  instead of adding a fakeable interface — no separate interface was needed since the Drive
  dependency (`a.drive map[string]*drive.Client`) was already swappable per test. Covers: `drive_*`
  absent from `tools/list` for a project with no Drive client and present for one that has one; an
  unavailable tool named directly is a JSON-RPC `-32602` error (not a tool-call `isError`, since
  `mcp.Server.call` rejects an unlisted tool before invoking it — the first version of this test
  wrongly expected `isError`; fixed to check the RPC `error` envelope instead); `drive_read` paging
  by offset/limit (and the whole-file default case); `drive_fetch` producing a URL under
  `http://api:8070/drive/fetch/…` (the test's fixed `Host` header) that a bare `GET` (no auth)
  actually streams, with `Content-Disposition: attachment; filename="report.pdf"`; a tampered token
  (last signature hex digit flipped) → 403; an expired token (signed with `exp` in the past) → 403;
  a garbage token (no `.`) → 403; sign/verify round-trip and wrong-secret cases as plain function
  tests. No real Drive OAuth token exists yet (Kai has not created the Google Cloud OAuth client —
  T14's human step is still open), so the manual check in Validation (`BOB_DRIVE_TOKEN_DEV` against
  a live Drive) was skipped, as this ticket's instructions anticipated; everything else in
  Validation was run against the real box/dev stack. Validation: `./stack test` → ends
  `── all green`; `cd api && go vet ./...` → clean; `BOB_TEST_DATABASE_URL=...
  go test ./... -count=1 -v | grep -cE -- "--- (SKIP|FAIL)"` → `0`; `./stack build && ./stack
  restart` → `curl http://127.0.0.1:8070/healthz` → `ok`; `curl -o /dev/null -w '%{http_code}'
  http://127.0.0.1:8070/drive/fetch/garbage` → `403`.

### T16: `./stack deploy`, secrets moved, ENC goes live   [Status: done | Model: sonnet]
- **Scope:**
  1. **`./stack deploy <tag>`** (new case in `stack`; `BOX=${BOX:-ubuntu@box.badcode.tv}`):
     refuse without a tag, and refuse unless `git cat-file -e <tag>:deploy/compose.yml` succeeds
     and `.env.box` exists; then over ssh: `sudo install -d -m 755 /srv/apps/bob`; write
     `git show <tag>:deploy/compose.yml` to `/srv/apps/bob/compose.yml`; write `.env.box` to
     `/srv/apps/bob/.env` with mode 600 (umask 077, write `.env.new` then `mv`, as
     `ops/scripts/deploy:34` does); write `TAG=<tag>` to `/srv/apps/bob/release.env`; remove
     leftovers `compose.projects.yml` and `projects.yaml` there; `docker network inspect
     bob-projects || docker network create bob-projects`; `docker volume create` each
     `bob-project-*` volume named in the new compose file; `sudo app bob pull --quiet && sudo app
     bob up -d --remove-orphans`; wait up to 60 s for `curl -sf http://127.0.0.1:8100/healthz`;
     print `docker ps --filter name=bob-project-`. Document it in the `stack` header and README.
     `./stack publish` runs `scripts/publish` (unchanged).
  2. **Production secrets move here** (ask Kai): `cp ../ops/secrets/bob/.env .env.box`; remove
     `BOB_RUNTIME_KEY`, `BOB_PROJECTS_FILE`, `FRED_API_KEY` (wolf's comes back in T25); add
     `BOB_PUBLIC_URL=https://bob.box.badcode.tv`,
     `BOB_RUNTIME_TOKEN_ENC` (`openssl rand -hex 32`), `GITHUB_TOKEN_ENC` (fine-grained, `emperorsnewcoin/bob` only, contents read/write —
     Kai creates it), `BOB_DRIVE_CLIENT_ID`, `BOB_DRIVE_CLIENT_SECRET`,
     `BOB_DRIVE_TOKEN_ENC` (from `scripts/drive-token` run against the ENC Drive account). Add
     Richard as a member of `enc` in `BOB_PROJECT_MAP`. Mind the quoting difference documented in
     `ops/secrets/README.md` ("The quoting trap"): `.env.box` is read by `docker compose
     --env-file` (literal), `.env` by the shell (`./stack` sources it).
  3. **`deploy/compose.yml`**: add `BOB_DRIVE_CLIENT_ID`, `BOB_DRIVE_CLIENT_SECRET`,
     `BOB_DRIVE_TOKEN_ENC` to the `api` service environment.
  4. **Box cutover** (each step after Kai's go-ahead; it is live): `sudo app bob stop` (the old
     API and `bob-project-wolf`); `sudo docker rm -f bob-project-wolf && sudo docker volume rm
     bob-project-wolf`; reset the schema as `app_bob` so it owns it — `sudo docker exec -it
     <postgres container> psql -U app_bob -d bob -c 'DROP SCHEMA public CASCADE; CREATE SCHEMA
     public;'` (find the container with `sudo docker ps --filter name=postgres`); `./stack publish`
     then `./stack deploy <tag>`; Kai runs `sudo docker exec -it bob-project-enc codex login
     --device-auth` (device-code login enabled in the ChatGPT account's security settings).
  5. **Ops repo** (`/home/kai/projects/badcode/ops`, its own commit, after the deploy works):
     delete `apps/bob/`, `secrets/bob/.env`, `secrets/bob/.env.local`; README.md's Bob row says it
     is deployed from `badcodetv/bob` with `./stack deploy`, and lists what ops still provides
     (database `bob` / role `app_bob`, `box-db`, Caddy route, `app` helper, the vector extension
     from T25); `secrets/README.md` notes that Bob's secrets live in its own checkout (`.env`,
     `.env.box`). The bob `.env` is already a real file (T3), so nothing points at the deleted files.
- **Files:** `stack`, `deploy/compose.yml`, `deploy/env.example`, `.env.box` (not committed),
  `README.md`; ops: `README.md`, `secrets/README.md`, deletions.
- **Acceptance criteria:** ENC live check — Kai opens `enc`, starts a **Codex plain chat**, asks it
  to create a worker `librarian`; a librarian chat lists a Drive folder, reads one Doc, clones
  `emperorsnewcoin/bob` into `/project/work`, writes the Doc as markdown into it, commits and pushes.
- **TDD:** no.
- **Validation:** `bash -n stack` → ok; `./stack deploy` with no tag → refuses with usage;
  `ssh ubuntu@box.badcode.tv 'curl -sf http://127.0.0.1:8100/healthz'` → `ok`;
  `git status --ignored --short .env.box` → `!! .env.box`; `ls /home/kai/projects/badcode/ops/apps/bob`
  → no such directory; the live check, observed by Kai.
- **Depends on:** T10, T12, T13, T15
- [x] done
- Notes: (executor, scope items 1 and 3 only) Items 1 and 3 done; items 2 (secrets move), 4 (box
  cutover) and 5 (ops repo) are pending — not touched, per this run's instructions (no ssh to the
  box, no `.env.box` created, no tagged `./stack deploy`).
  Item 1: added a `deploy` case and a `publish` case (`exec scripts/publish`) to `stack`, matching
  the exact sequence in the ticket (tag check via `git cat-file -e <tag>:deploy/compose.yml`,
  `.env.box` existence check, both before any ssh; then install dir, compose.yml, `.env.box` →
  `.env` with the umask 077 + `.env.new` + `mv` pattern from `ops/scripts/deploy:34`,
  `release.env`, leftover removal, `bob-projects` network, `bob-project-*` volumes parsed from the
  new compose file's `volumes:` block, `sudo app bob pull --quiet && sudo app bob up -d
  --remove-orphans`, a 60s healthz wait on `127.0.0.1:8100`, then `docker ps --filter
  name=bob-project-`). `./stack publish` did not previously exist as a `stack` case (only the
  standalone `scripts/publish`); added a one-line case so the ticket's "`./stack publish` runs
  `scripts/publish`" is literally true, without changing `scripts/publish` itself. Documented both
  in the `stack` header comment and in README.md's "Running it on a server" section (added a
  note there that items 2/4/5 are still pending and `apps/bob/` in ops is still what actually
  runs).
  Item 3: `deploy/compose.yml`'s `api` service gets `BOB_DRIVE_CLIENT_ID`, `BOB_DRIVE_CLIENT_SECRET`,
  `BOB_DRIVE_TOKEN_ENC` as plain `${VAR:-}` (not `:?required`) — T14's `main.go` treats an empty
  `BOB_DRIVE_TOKEN_<NAME>` as "no Drive client for that project" and only fails if a token is set
  without a client id/secret, so the api must start fine with these unset for a box with no Drive
  project configured yet; `:?` would break every deploy until item 2 sets real values. Uncommented
  the matching block in `deploy/env.example` (it was previously commented out and noted as "not
  yet wired into deploy/compose.yml"; now that it is wired, the file's own header rule — every
  variable the compose file names needs a dummy-but-non-empty value here — applies).
  Validation run: `bash -n stack` → ok; `./stack deploy` (no tag) → `!! usage: ./stack deploy
  <tag>`, exit 1; `./stack deploy nonexistent-tag-xyz` → `!! no deploy/compose.yml at
  nonexistent-tag-xyz — publish it first (./stack publish)`, exit 1, no ssh attempted (checked by
  inspection — the tag check runs before any `ssh` line); `docker compose --env-file
  <scratchpad>/dummy.env -f deploy/compose.yml config -q` → exit 0, both with the three
  `BOB_DRIVE_*` dummy vars set and with them absent (confirming the `${VAR:-}` default); did not
  touch `api/`, `runtime/`, `web/`, or run `./stack build/restart/test`.
  (orchestrator, 2026-09-29) Item 2 prepared: `.env.box` written (mode 600, gitignored) from the ops copy minus `BOB_RUNTIME_KEY`/`BOB_PROJECTS_FILE`/`FRED_API_KEY`, plus `BOB_PUBLIC_URL`, a new `BOB_RUNTIME_TOKEN_ENC`, and `OPENAI_API_KEY` (required since T21). Still empty, for Kai: `GITHUB_TOKEN_ENC`, `BOB_DRIVE_CLIENT_ID`, `BOB_DRIVE_CLIENT_SECRET`, `BOB_DRIVE_TOKEN_ENC`; Richard not yet in `BOB_PROJECT_MAP`. `docker compose config` with it fails only on `GITHUB_TOKEN_ENC`. Box reachable over ssh; old `bob-api-1` + `bob-project-wolf` still running (cutover not started).
  (orchestrator, 2026-09-30) Cutover done (items 2 and 4): Kai filled `.env.box` (GitHub tokens for enc/marketing/wolf, Drive client, Richard + Jack in `BOB_PROJECT_MAP`); `BOB_DRIVE_TOKEN_ENC` reuses Kai's own Drive token (`kaiyadavenport@gmail.com`, Kai's choice — ENC agents see what that account sees). Backup first: `/home/ubuntu/bob-cutover-backup-20260930/` on the box (old `bob` db dump, 8 near-empty tables; `bob-project-wolf` volume tar). Then `sudo app bob stop`, removed `bob-project-wolf` container + volume, `DROP SCHEMA public CASCADE; CREATE SCHEMA public;` as `app_bob`, and `CREATE EXTENSION vector` as postgres (needed now, not at T25: T21 makes the API refuse to boot without it). `./stack publish` → `3a21a19`; `./stack deploy 3a21a19` → healthy; migrations 001–005 applied; serving `enc`; public site 200, `/api/me` 401. Box still has old ops hook files in `/srv/apps/bob` (`gen-projects.sh`, `pre-deploy.sh`, `post-deploy.sh`) — unused by `app`/`./stack deploy`, safe to delete with item 5. Remaining: Kai's box codex login, the ENC live check, then item 5 (ops repo).
  (orchestrator, 2026-09-30) Done. Kai ran the box codex login (survives a container restart and a recreate — tested) and the ENC live check passed: Codex plain chat created `librarian`; librarian listed Drive, read a Doc, cloned `emperorsnewcoin/bob`, wrote the Doc as markdown, committed and pushed. Item 5: ops commit `7144379` removed `apps/bob/`, README's Bob row rewritten; `secrets/bob/.env{,.local}` deleted (untracked; `FRED_API_KEY` copied into `.env.box` first for T25); ops `secrets/README.md` note added (git-ignored there). Validation: `bash -n stack` ok; `./stack deploy` without a tag refuses; box healthz `ok`; `.env.box` ignored; `ops/apps/bob` gone.

### Part 2 — marketing

### T17: Marketing project   [Status: in progress — deployed at 99decbb; live check waits on Kai | Model: sonnet]
- **Scope:** `deploy/compose.yml`: add `bob-project-marketing` (as enc: `BOB_PROJECT_NAME:
  marketing`, `BOB_RUNTIME_TOKEN: ${BOB_RUNTIME_TOKEN_MARKETING:?}`,
  `GITHUB_TOKEN: ${GITHUB_TOKEN_MARKETING:?}`), set `BOB_PROJECTS: enc,marketing` and add
  `BOB_RUNTIME_TOKEN_MARKETING` to the `api` service; add both to `deploy/env.example`. In
  `.env.box`: `BOB_RUNTIME_TOKEN_MARKETING`, `GITHUB_TOKEN_MARKETING` (scoped to `badcodetv/core`),
  Jack as a member of `marketing` in `BOB_PROJECT_MAP`. Publish and deploy.
- **Files:** `deploy/{compose.yml,env.example}`, `.env.box` (not committed).
- **Acceptance criteria:** Kai starts a **Claude** plain chat in `marketing`; it clones
  `badcodetv/core` into `/project/work`, does a web search, and writes a draft file.
- **TDD:** no.
- **Validation:** `TAG=x docker compose --env-file deploy/env.example -f deploy/compose.yml config >/dev/null`
  → exit 0; the live check, observed by Kai.
- **Depends on:** T16
- [ ] done
- Notes:

### Part 3 — Wolf

### T18: Labels package and worker labels   [Status: done | Model: sonnet]
- **Scope:** `api/internal/labels/labels.go`: port agent-bob `go/agentdb/labels.go` (448 lines) —
  key/value regex `^[A-Za-z0-9]([-A-Za-z0-9_.]*[A-Za-z0-9])?$`, ≤ 63 characters, ≤ 32 labels;
  selectors `k=v`, `k!=v`, `k in (a,b)`, `k notin (a)`, `k` / `exists k`, `!k`, comma = AND;
  `Parse(string) (Selector, error)`, `Selector.SQL(column string, argStart int) (sql string, args []any)`
  using jsonb `@>` and `jsonb_exists`. Keys starting `bob.` are rejected. Migration
  `002_worker_labels.sql`: `ALTER TABLE workers ADD COLUMN labels jsonb NOT NULL DEFAULT '{}'` +
  `CREATE INDEX workers_labels ON workers USING gin (labels)`. Store: `Worker.Labels`,
  `Workers(ctx, project string, sel labels.Selector)` (zero selector = all; update T5's callers).
  MCP: `labels` on create/update, `label_selector` on `worker_list`. HTTP: `labels` in POST/PATCH;
  `GET …/workers?selector=`. Web: a labels field in the editor; a selector box above the sidebar's
  worker list that filters it.
- **Files:** `api/internal/labels/{labels.go,labels_test.go}`, migration,
  `api/internal/store/workers.go`, `api/cmd/bob/{tools_workers.go,workers.go}` (+ tests),
  `web/src/{Sidebar,WorkerEditor,api}.tsx|ts`.
- **Acceptance criteria:** `worker_list {label_selector:"kind=hypothesis"}` returns only matching
  workers; an invalid selector returns a readable error.
- **TDD:** yes.
- **Validation:** `./stack test` → "all green"; `cd web && npx tsc -b` → clean.
- **Depends on:** T10
- [x] done
- Notes: implemented `api/internal/labels/labels.go` (+ `labels_test.go`, TDD, failing tests
  first): `Validate(map[string]string) error` (key/value regex, ≤63 chars, ≤32 labels, `bob.`
  prefix reserved) and `Parse(string) (Selector, error)` for the selector grammar (`k=v`, `k!=v`,
  `k in (a,b)`, `k notin (a)`, `k`/`exists k`, `!k`, comma = AND; commas inside `( )` don't split
  terms). `Selector.SQL(column string, argStart int) (string, []any)` renders `@>` for
  equals/in/notin (one `{"key":"value"}` jsonb literal per value, OR'd for in/notin, negated for
  notEquals/notin) and `jsonb_exists` for exists/notExists; a zero-length Selector returns
  `("TRUE", nil)` so callers can always append its SQL. Migration `002_worker_labels.sql` adds
  `workers.labels jsonb NOT NULL DEFAULT '{}'` and a gin index. `store.Worker.Labels` (already
  declared since T4) is now backed by the column — `workerCols`/`scanWorker` include it, never
  nil (defaults to `{}`); `CreateWorker`/`UpdateWorker` persist `w.Labels` (nil normalised to
  `{}` via `emptyIfNil`); `Workers(ctx, project, sel labels.Selector)` replaces the old
  `Workers(ctx, project)` (updated the two callers: `worker_list` and `GET .../workers`) and
  applies `sel.SQL("labels", 2)` in the WHERE clause. MCP: `worker_create`/`worker_update` take
  `labels` (object); `worker_list` takes `label_selector`, parsed with `labels.Parse` and
  returned as a tool error (readable message) on a bad selector — ignored when `name` is given.
  HTTP: `labels` in the `workerBody` for POST/PATCH; `GET .../workers?selector=` parsed the same
  way, 400 with the parse error on a bad selector. `validWorker` now also calls
  `labels.Validate(w.Labels)`, so both HTTP and MCP paths reject bad label sets (bad key/value,
  too many, `bob.` prefix) the same way session/tool validation errors already work. Web:
  `WorkerEditor.tsx` gets a "Labels" field (comma-separated `k=v`, parsed client-side with a
  readable error via `onError` before saving — same shape as the existing Tools field);
  `api.ts`'s `WorkerInput` carries `labels?`. `Sidebar.tsx` gets a selector box above the worker
  groups (client-side, over the `workers` prop it already has, matching the same grammar as
  `internal/labels` re-implemented in TS since the sidebar already holds the full worker list and
  a round trip isn't needed) — parse errors show inline and the list is left unfiltered; a
  "removed worker" group (still in old chats, but the worker is gone) has no labels to check
  against, so a non-empty selector always excludes it. TDD: labels_test.go written and run failing
  before labels.go; store/HTTP/MCP tests added alongside the existing worker tests (label
  round-trip through create/update, selector-filtered list, invalid selector as a readable tool
  error / 400, reserved `bob.` prefix rejected) — all written before the implementing changes and
  verified failing, then passing. Deviation: `TestFreshDatabaseIsTheBaseline`
  (`internal/store/store_test.go`) hard-coded the migration list to `[001_baseline.sql]`; updated
  it to expect `002_worker_labels.sql` too (unavoidable — any new migration ticket touches this
  test). Validation: `go vet ./...` clean; `BOB_TEST_DATABASE_URL=... go test ./... -count=1 -v`
  — 0 SKIP/FAIL; `./stack test` → "all green"; `cd web && npx tsc -b` clean; `./stack build &&
  ./stack restart` then `curl -s http://127.0.0.1:8070/healthz` → `ok`. Manual check: minted a
  throwaway `bob_session` cookie for kaiyadavenport@gmail.com (script read `BOB_SESSION_SECRET`
  from `.env`, deleted after use, nothing printed), created `t18-scout` (labels
  `{"kind":"hypothesis"}`) and `t18-scraper` (`{"kind":"scraper"}`) on project `dev` via `POST
  .../workers`; `GET .../workers?selector=kind=hypothesis` returned only `t18-scout`; `GET
  .../workers?selector=kind in (a` (unterminated paren) returned 400 with `selector: unterminated
  (`. Both test workers deleted afterward (`DELETE .../workers/<name>?why=cleanup`) — `dev`'s
  worker list is back to just the pre-existing `poet` from T10's manual check.

  (orchestrator) Checked against agent-bob `go/agentdb/labels.go` (it does exist at `/home/kai/projects/badcode/agent-bob`; the executor missed it): same key/value regex, 63-char limit, 32 labels, operators `= != in notin exists !`. Validation re-run: vet clean, 0 SKIP/FAIL, all green, tsc clean.

### T19: Schedule MCP tools   [Status: done | Model: sonnet]
- **Scope:** `api/cmd/bob/tools_schedules.go`: `schedule_create`, `schedule_update`,
  `schedule_list`, `schedule_delete` (Interfaces), addressed by schedule **name** within the
  project, reusing `validSchedule` (`schedules.go:247`) and `scheduleBody.apply`
  (`schedules.go:224`). Enabling or retiming sets `ChangedAt` as `updateSchedule` does
  (`schedules.go:293-295`). Results include `next_at`. Intentionally not admin-gated (Decision 13).
- **Files:** `api/cmd/bob/{tools_schedules.go,tools_schedules_test.go}`.
- **Acceptance criteria:** a chat creates a worker and a daily schedule for it; the schedule shows
  on the Schedules page.
- **TDD:** yes.
- **Validation:** `./stack test` → "all green".
- **Depends on:** T10
- [x] done
- Notes: implemented `api/cmd/bob/tools_schedules.go` (`(a *app) scheduleTools() []mcp.Tool`,
  registered in `main.go` next to `workerTools`) and `api/cmd/bob/tools_schedules_test.go`.
  `schedule_create`/`schedule_update` share a `scheduleToolArgs` struct (pointer fields, so
  `schedule_update` can tell "left out" from a zero value) whose `.body()` converts to the
  existing `scheduleBody` for `.apply()`; both then run `validSchedule` and, on retime/enable,
  bump `ChangedAt` exactly as `updateSchedule` (`schedules.go`) does. All four tools resolve the
  schedule by **name** within `Caller.Project` — no argument names a project or takes an id — via
  a new store method `ScheduleByName(ctx, project, name)` (schedules.go, `schedules` has
  `UNIQUE (project, name)` already) added alongside the existing id-keyed `Schedule`. Results are
  a `scheduleToolView` (name aliases `scheduleView` from `schedules.go`: the schedule plus
  `next_at` and `last_run`, exactly what the Schedules page and `listSchedules` show). Found and
  fixed a real gap while writing the duplicate-name test: `store.CreateSchedule` did not turn a
  unique-constraint violation on `(project, name)` into `store.ErrConflict` the way
  `CreateWorker` does for workers — it just returned the raw pg error. Fixed in
  `api/internal/store/schedules.go` (same `isUniqueViolation` pattern as `workers.go`); worth a
  look for T20+ if any other schedule/run write assumes constraint violations already map to
  `ErrConflict`. Not admin-gated (Decision 13): any chat in a project can create and change its
  own schedules, matching worker tools. TDD: failing tests first in `tools_schedules_test.go`
  covering create (+`next_at`, defaults, duplicate name, unknown worker, bad cron), update (fields
  left out kept, `changed_at` bumped only on retime/enable), list, delete (unknown name), and
  cross-project isolation (a `dev` chat cannot see, update or delete `wolf`'s schedule — list is
  empty, update/delete both error, and the schedule is left untouched). Validation: `go vet ./...`
  clean; `BOB_TEST_DATABASE_URL=... go test ./... -count=1 -v` — all pass, no SKIP/FAIL;
  `./stack test` → "all green". Manual check after `./stack build && ./stack restart`
  (`curl .../healthz` → `ok`): minted a `bob_session` cookie for kaiyadavenport@gmail.com with a
  throwaway `api/cmd/minttmp` program (deleted afterwards, along with the cookie file), created a
  plain chat on `dev` via `POST /api/projects/dev/sessions`, and sent "Using worker_create, create
  a claude worker named haiku-writer …; then use schedule_create to create a daily schedule named
  haiku-daily for that worker, cron 0 9 * * *, message …" via `POST /api/sessions/{id}/messages`.
  `GET /api/projects/dev/workers` shows `haiku-writer`; `GET /api/projects/dev/schedules` (what
  the Schedules page reads) shows `haiku-daily` with `worker: "haiku-writer"`, `next_at:
  "2026-09-29T09:00:00Z"`. Cleaned up afterwards: deleted the `haiku-daily` schedule, the
  `haiku-writer` worker and the test chat session; the pre-existing `poet` worker (from T10) and
  its earlier chat were left untouched.

### T20: One scheduled run at a time per project   [Status: done | Model: opus]
- **Scope:** Migration `003_schedule_queue.sql`: drop and re-add the `schedule_runs.status` CHECK
  to allow `queued`. `StartRun` (`store/schedules.go:110`): if this schedule has a `queued` or
  `running` run → insert `skipped` ("previous run still queued or running"); else insert `queued`.
  New `store.NextQueuedRun(ctx, project string) (Run, bool, error)`: in one transaction take
  `pg_advisory_xact_lock(hashtext('bob-schedule:' || project))`; if the project has a `running`
  run return false; else flip its oldest `queued` run to `running` (`started_at = now()`) and
  return it. `app.dispatch(ctx, project)` calls it and runs `execute` for the result in a
  goroutine (tracked by `a.scheduled`); it is called after `fire`, when each run finishes, and at
  the start of each `tick` for every project. `FailInterruptedRuns` fails `running` runs only
  (queued ones survive a restart). "Run now" (`runScheduleNow`) queues too. `Schedules.tsx` shows
  `queued`; `api.ts`'s `ScheduleRun.status` gains it.
- **Files:** migration, `api/internal/store/schedules.go`, `api/cmd/bob/schedules.go` (+ tests),
  `web/src/{Schedules,api}.tsx|ts`.
- **Acceptance criteria:** two schedules of one project due in the same minute run one after the
  other; another project's schedule runs concurrently; a person's chat is never delayed.
- **TDD:** yes — including two concurrent `NextQueuedRun` callers, only one of which gets the run.
- **Validation:** `./stack test` → "all green"; `cd web && npx tsc -b` → clean.
- **Depends on:** T19
- [x] done
- Notes: migration `api/internal/store/migrations/003_schedule_queue.sql` drops and re-adds
  `schedule_runs_status_check` with `queued`, plus a partial index `schedule_runs_active (status, id)
  WHERE status IN ('queued','running')` for the dispatcher. `store.StartRun` now inserts `queued`
  (or `skipped`, detail "previous run still queued or running", when the schedule has a queued or
  running run; still under the schedule's `FOR UPDATE`). New `store.NextQueuedRun(ctx, project)
  (Run, bool, error)`: one transaction, `pg_advisory_xact_lock(hashtext('bob-schedule:' ||
  project))`, returns false if the project has a `running` run, else flips its oldest (`ORDER BY
  id`) `queued` run to `running` and returns it. `FailInterruptedRuns` unchanged in SQL (it already
  only touched `running`); comment updated. App (`api/cmd/bob/schedules.go`): `fire` = `StartRun`
  then `a.dispatch(ctx, sch.Project)` and returns the run as recorded (`queued`/`skipped`); new
  `a.dispatch` calls `NextQueuedRun` and runs `execute` in a goroutine tracked by `a.scheduled`
  (re-reading the schedule by the run's `schedule_id`; if that fails the run is finished `failed`),
  then dispatches the project again once the run has been finished — so `a.scheduled.Wait()` still
  covers a whole chain. `tick` dispatches every present project (`store.Projects`) first, **before
  the paused check** (a queued run was already accepted, same as Run now works while paused).
  `runScheduleNow` goes through `fire`, so it queues. Web: `api.ts` `ScheduleRun.status` gains
  `queued`; `Schedules.tsx` shows "Queued" (faint), polls every 5s while any last run is queued or
  running, and disables Run now with "Queued…"/"Running…". **Deviation:** `NextQueuedRun` stamps
  `started_at` from a new `Store.Now func() time.Time` field (nil = `time.Now`, so production is
  exactly `now()`) instead of SQL `now()`: `LastCronRun` reads `max(started_at)`, and in the
  fake-clock app tests a DB `now()` put the "last firing" in real time, before the fake firing, so
  ticks re-fired (e.g. `TestScheduleMissedFirings`'s double tick). `newScheduleApp` sets
  `st.Now = c.now`. TDD: failing tests first — `api/internal/store/schedules_test.go` (new):
  `TestStartRunQueues`, `TestNextQueuedRunOneAtATimePerProject` (oldest first, other project not
  held, next after finish), `TestNextQueuedRunConcurrentCallers` (two goroutines, 5 rounds with one
  and with two queued runs; exactly one caller gets a run — confirmed it fails with 2 winners when
  the advisory lock is removed), `TestFailInterruptedRunsKeepsQueued`; `store_test.go`'s
  migration list gains `003_schedule_queue.sql`; `api/cmd/bob/schedules_test.go`:
  `TestScheduleNoOverlap` detail text updated, new `TestScheduledRunsQueuePerProject` (two wolf
  schedules due in the same minute: one runs, one `queued`; enc's runs concurrently; Run now on the
  queued one is skipped; a person's chat in wolf reaches the runtime while wolf's run is held; the
  queued run starts only after the first is `ok`) and `TestQueuedRunsStartOnTheNextTick` (a
  left-over queued run starts on a paused tick; Run now returns `queued`). Also passes `-race
  -count=3`. Validation: `go vet ./...` clean; `BOB_TEST_DATABASE_URL=... go test ./... -count=1 -v
  | grep -cE -- "--- (SKIP|FAIL)"` → `0`; `./stack test` → "── all green"; `cd web && npx tsc -b`
  clean; `./stack build && ./stack restart`, `/healthz` → `ok`, local `bob` db has
  `003_schedule_queue.sql` applied and the new CHECK. Manual (minted cookie via a throwaway stdlib
  program in the scratchpad, deleted after): on `dev`, created disabled schedules `t20-a`/`t20-b`
  on the existing `poet` (claude) worker, then Run now on a, b, b: responses `queued`, `queued`,
  `skipped` ("previous run still queued or running"); runs table afterwards: a `ok` 19:42:02.56 →
  19:42:09.583, b `ok` started 19:42:09.587 (4ms after a finished) → 19:44:32, both with
  `bob.turn_done`. Cleaned up: deleted both runs' sessions and both schedules; `schedules`,
  `schedule_runs` and scheduled sessions are all 0 rows; `poet` untouched.

### T21: Memory store and hybrid search   [Status: done | Model: opus]
- **Scope:** Port agent-bob `go/agentdb/memories.go` (992 lines) and
  `go/extension/embedding/{embedding,openai}.go`, leanly. **Pre-migration check:** in
  `store.Open` (`store.go:25-35`), before `migrate`, if migration `004_memories.sql` is not yet
  recorded and `SELECT 1 FROM pg_extension WHERE extname = 'vector'` finds nothing, try
  `CREATE EXTENSION IF NOT EXISTS vector` and, if that fails, return: "memory needs pgvector: as the
  postgres superuser, run CREATE EXTENSION vector; in the bob database". (Test databases are
  created by the superuser, so tests create it themselves.) Migration `004_memories.sql`:
  `CREATE EXTENSION IF NOT EXISTS vector;` then `memories (id uuid PRIMARY KEY DEFAULT
  gen_random_uuid(), project text NOT NULL REFERENCES projects(name), labels jsonb NOT NULL DEFAULT
  '{}', content text NOT NULL, content_tsv tsvector GENERATED ALWAYS AS (to_tsvector('english',
  content)) STORED, embedding vector(1536) NOT NULL, created_by_worker text NOT NULL DEFAULT '',
  created_by_session uuid REFERENCES sessions(id) ON DELETE SET NULL, created_at timestamptz NOT
  NULL DEFAULT now())`, indexes `(project, created_at DESC, id DESC)`, `gin (labels)`,
  `gin (content_tsv)`, `hnsw (embedding vector_cosine_ops)`, and `CREATE INDEX memories_retracts ON
  memories (project, (labels->>'retracts')) WHERE labels ? 'retracts'`. `retracts` is a **label**
  (`retracts=<memory id>`, as agent-bob `memories.go:545`); compare with `m.id::text`.
  `api/internal/embed`: `type Embedder interface { Embed(ctx context.Context, text string) ([]float32, error) }`,
  `OpenAI{Key, Model string}` (POST `https://api.openai.com/v1/embeddings`, `dimensions: 1536`,
  30 s timeout), `Fake` (deterministic hash-based vector, for tests). Store: `CreateMemory`
  (content ≤ 24 KiB; `if_current` compare-and-swap under `pg_advisory_xact_lock`), `Memory`,
  `CurrentMemory(name)`, `SearchMemories(opts)` = agent-bob's CTE: filter (project, selector via
  `labels.Selector.SQL`, not retracted, since/until, created_by_worker, latest_per) → keyword top
  200 by `ts_rank_cd(plainto_tsquery('english', q))` and vector top 200 by `<=>` → RRF k = 60 →
  recency tiebreak; no query → newest first; snippet = first 500 characters. `main.go` requires
  `OPENAI_API_KEY` and builds `embed.OpenAI`.
- **Files:** migration, `api/internal/embed/{embed.go,embed_test.go}`,
  `api/internal/store/{memories.go,memories_test.go,store.go}`, `api/cmd/bob/main.go`,
  `deploy/env.example`.
- **Acceptance criteria:** with `Fake`, a memory matching only by keyword and one matching only by
  vector both appear, fused; `retracts=<id>` hides the target; `latest_per=name` returns one per
  name value.
- **TDD:** yes.
- **Validation:** `./stack test` → "all green"; once on the laptop:
  `docker compose -f ~/projects/badcode/ops/apps/postgres/compose.local.yml exec -T postgres psql -U postgres -d bob -c 'CREATE EXTENSION IF NOT EXISTS vector'`,
  then `./stack restart` → "ready".
- **Depends on:** T18
- [x] done
- Notes: implemented TDD (failing tests first, each run red before the code). `api/internal/embed/embed.go`:
  `Dim = 1536`, `DefaultModel`, `Embedder` interface, `OpenAI{Key, Model}` (POST
  `/v1/embeddings` with `dimensions: 1536`, 30 s client, OpenAI's error message surfaced, width and
  finiteness checked; an unexported `endpoint` field lets the test point it at httptest), `Fake`
  (agent-bob's hashed bag of words, unigrams only, unit length). Migration `004_memories.sql`
  exactly as scoped (index names `memories_project`, `memories_labels`, `memories_content_tsv`,
  `memories_embedding`, `memories_retracts`). `store.migrate` calls `needVector` right after
  creating `schema_migrations`: skipped once 004 is recorded or the extension exists, else tries
  `CREATE EXTENSION IF NOT EXISTS vector` and on failure returns the ticket's message plus
  Postgres's own error in parentheses. `api/internal/store/memories.go`: `Memory` (labels never
  nil; `CreatedBySession` is "" for none / a deleted chat), `MaxMemoryBytes = 24*1024`,
  `CreateMemory(ctx, m, embedding, ifCurrent) (Memory, error)` — the caller embeds, the store never
  calls out (as agent-bob); `created_at` is the store's clock (`Store.Now`), so tests can move
  time; with `ifCurrent` it needs a `name` label, takes
  `pg_advisory_xact_lock(hashtext('bob-memory:'||project||'\n'||name))` (same style as T20's
  schedule lock), reads the current memory inside the lock and returns `ErrNotCurrent{Name,
  IfCurrent, Current}` (a struct, as agent-bob, so the loser learns who won) instead of agent-bob's
  guarded `INSERT … SELECT … WHERE`; `Memory(ctx, project, id)` (a non-uuid id is `ErrNotFound`,
  retracted memories still readable), `CurrentMemory(ctx, project, name)`,
  `SearchMemories(ctx, MemorySearch{Project, Selector labels.Selector, Query, QueryEmbedding, Limit,
  Since, Until time.Time, CreatedByWorker, LatestPer}) ([]MemoryHit, error)` — the caller passes
  an already-parsed selector and an already-resolved worker name (T22 resolves `self` and parses
  `label_selector`). Search is agent-bob's CTE with pgx `$n` placeholders: `filtered` (project,
  selector, not retracted, since/until, worker, `DISTINCT ON` for latest_per — key checked with
  `labels.Validate` before being written into the SQL) → `kw` top 200 by `ts_rank_cd` → `sem` top
  200 by `<=>` (skipped when `QueryEmbedding` is nil) → RRF k = 60 → recency tiebreak; no query →
  newest first with score 0; snippet `left(content, 500)`. Dropped from agent-bob as not needed:
  nullable embeddings / keyword-only degrade on write, `IncludeRetracted`/`retracted_by` audit view,
  the vector-column probe. `main.go`: `embedderFromEnv` requires `OPENAI_API_KEY` (boot fails with a
  message naming it), `BOB_EMBEDDING_MODEL` optional; the embedder is on `app.embed` for T22; header
  comment lists both. `deploy/compose.yml` api: `OPENAI_API_KEY: ${OPENAI_API_KEY:?}` and
  `BOB_EMBEDDING_MODEL: ${BOB_EMBEDDING_MODEL:-}`; `deploy/env.example` has `OPENAI_API_KEY`. The
  laptop needed no wiring: `./stack start` sources the whole `.env` into the API, and `.env`
  already had the key. Tests: `embed_test.go` (Fake, OpenAI against httptest, and
  `TestOpenAILive`, which makes one real call only with `BOB_TEST_OPENAI=1` + `OPENAI_API_KEY`
  and otherwise logs and passes rather than skipping, so it doesn't count as a SKIP; run once
  with the laptop key: PASS); `memories_test.go` covers the three acceptance criteria
  (keyword-only "Ferries run hourly" and vector-only "the the the zebra" both returned for
  "the ferry", the both-legs match first with 1/61 < score ≤ 2/61; `retracts=<id>` hides the
  memory from search and `CurrentMemory` but not from `Memory`, and a retraction in another project
  doesn't reach across; `latest_per=name` gives one per name and leaves out unnamed memories),
  plus filters, validation, compare-and-swap, project isolation, and
  `TestOpenWithoutPgvectorSaysHowToFix` (a throwaway non-superuser role owning a throwaway
  database; role and database dropped afterwards). `TestFreshDatabaseIsTheBaseline` now expects
  `004_memories.sql` and the `memories` table; `TestEmbedderFromEnv` in `main_test.go`.
  Validation: `go vet ./...` clean; `BOB_TEST_DATABASE_URL=… go test ./... -count=1 -v | grep -cE
  -- "--- (SKIP|FAIL)"` → `0`; `./stack test` → "all green". Laptop: `./stack build && ./stack
  restart` *before* the extension existed stopped with `memory needs pgvector: as the postgres
  superuser, run CREATE EXTENSION vector; in the bob database (ERROR: permission denied to create
  extension "vector" (SQLSTATE 42501))` (the API connects as `app_bob`); then the ticket's
  `CREATE EXTENSION IF NOT EXISTS vector` → `CREATE EXTENSION`, `./stack restart` → "ready",
  `/healthz` → `ok`, `bob` has `memories`, `004_memories.sql` recorded, vector 0.8.6.

### T22: Memory MCP tools and the Overview list   [Status: done | Model: sonnet]
- **Scope:** `api/cmd/bob/tools_memory.go`: `memory_create`, `memory_search`, `memory_get`,
  `memory_current` (Interfaces). Port descriptions from agent-bob
  `go/cmd/agentd/mcp_memory.go:132-221` and time parsing from `go/cmd/agentd/timearg.go` (RFC3339,
  unix ms, or `7d` / `12h`). `created_by_worker` = `Caller.Worker`; `chat_url` =
  `BOB_PUBLIC_URL + "/#/p/<project>/s/<session>"`. HTTP `GET /api/projects/{project}/memories?limit=20`
  (newest). The Overview's memories section (`Overview.tsx:105`) lists them with labels.
- **Files:** `api/cmd/bob/{tools_memory.go,tools_memory_test.go,http.go}`, `web/src/{Overview,api}.tsx|ts`.
- **Acceptance criteria:** two chats of one project share memories; another project sees none.
- **TDD:** yes.
- **Validation:** `./stack test` → "all green"; `cd web && npx tsc -b` → clean.
- **Depends on:** T21
- [x] done
- Notes: implemented TDD. `api/cmd/bob/tools_memory.go`: `memory_create` (content required and
  <= `store.MaxMemoryBytes`, labels validated, embeds with `app.embed` and fails the create if the
  embedder fails, `created_by_worker` = `Caller.Worker`, `created_by_session` = the chat, `if_current`
  passed to `CreateMemory`; `store.ErrNotCurrent` comes back as its own message, naming the winner),
  `memory_search` (label_selector via `labels.Parse`, `since`/`until` as RFC3339, unix ms or `7d`/`12h`/
  `90m`/`30s` via `parseMemTime`, `created_by_worker` with `self` resolved from `Caller.Worker` and
  refused for a plain chat, query embedding degrades to keyword-only on embedder error; returns
  `{results, count, note}`), `memory_get` (project-scoped, other project = "no memory with id"),
  `memory_current` (`{found:false}` when none). Descriptions ported from agent-bob
  `mcp_memory.go:132-221`, trimmed for what T21 dropped (no `embed:false`). Results carry `chat_url` =
  `BOB_PUBLIC_URL + "/#/p/<project>/s/<session>"` ("" once the chat is deleted). HTTP
  `GET /api/projects/{project}/memories?limit=20` (member; newest first, `{memories:[hit…]}`).
  Registered in `main.go`. Web: `Memory` type and `api.memories` (`api.ts`), the Overview Memory
  section lists them with label chips, snippet, worker and a "view chat" link. Tests
  (`tools_memory_test.go`, `embed.Fake`): create/get, worker from session, validation, keyword+vector
  fusion, retracts, latest_per, `self`, current, if_current conflict, two chats of one project share
  memories while `dev` sees none (acceptance), the HTTP list, schema JSON validity, `parseMemTime`.
  Test gotcha: `newScheduleApp` freezes the store clock, so tests that depend on order advance it
  (`clk.set`). Bug found by the manual check and fixed: the `since`/`until` schema text contained raw
  double quotes, making `memory_search`'s InputSchema invalid JSON; `tools/list` then answered 200 with
  an EMPTY body (json encode failed) and the agent saw "bob MCP failed to connect" for every tool.
  `TestMemoryToolSchemasAreValidJSON` now guards it. Validation: `go vet` clean, `gofmt -l .` empty,
  SKIP/FAIL count `0`, `./stack test` "all green", `npx tsc -b` clean, `./stack build && ./stack
  restart`, `/healthz` `ok`. Manual (project `dev`, plain Claude chat): the agent called
  `memory_create` then `memory_search "manual check"` -> count 1, snippet "T22 manual check memory";
  the memory showed in `GET /api/projects/dev/memories` with labels and `chat_url`. Test chats
  (3) and the memory were deleted afterwards; the Overview page itself was not viewed in a browser.

### T23: Human attention — store, tool, webhook   [Status: done | Model: sonnet]
- **Scope:** Migration `005_attention.sql`: `attention_requests (id uuid PRIMARY KEY DEFAULT
  gen_random_uuid(), project text NOT NULL REFERENCES projects(name), session_id uuid NOT NULL
  REFERENCES sessions(id) ON DELETE CASCADE, worker text NOT NULL, message text NOT NULL, kind text
  NOT NULL CHECK (kind IN ('ask','notice')), created_at timestamptz NOT NULL DEFAULT now(),
  closed_at timestamptz, closed_by text, close_reason text CHECK (close_reason IN
  ('answered','dismissed')))` + `CREATE INDEX attention_open ON attention_requests (project,
  created_at) WHERE closed_at IS NULL`. Tool `request_human_attention` (description adapted from
  agent-bob `go/cmd/agentd/mcp_management.go:561-583`, without expiry): stores the request; if
  `BOB_ATTENTION_WEBHOOK_<NAME>` is set, POSTs `{project, worker, kind, message, chat_url}` with a
  10 s timeout in a goroutine (failure is only logged). `startTurn` (`http.go:461`) closes the
  session's open requests as `answered` when the sender is a person (email not starting
  `schedule:`). Routes `GET /api/projects/{project}/attention` and
  `POST /api/attention/{attention}/dismiss`; extend `guard` (`http.go:140-149`) with
  `case r.PathValue("attention") != ""` → `a.projectOf(ctx, "attention", id)`, and
  `storeProjectOf` with that kind. The session list adds `attention: bool`.
- **Files:** migration, `api/internal/store/{attention.go,attention_test.go}`,
  `api/cmd/bob/{tools_attention.go,attention.go,http.go}` (+ tests incl. `access_test.go`),
  `deploy/env.example`.
- **Acceptance criteria:** a request stores a row and POSTs the webhook (tested with `httptest`);
  replying closes it; dismissing closes it; a member of another project gets 404 on dismiss.
- **TDD:** yes.
- **Validation:** `./stack test` → "all green".
- **Depends on:** T5, T9
- [x] done
- Notes: (executor) implemented TDD. Migration `005_attention.sql` as scoped; `TestFreshDatabaseIsTheBaseline`
  now expects it and the `attention_requests` table. `store/attention.go`: `CreateAttention(session, worker,
  message, kind)` (project taken from the session), `Attention(id)` (compares `id::text`, so a non-uuid is
  ErrNotFound not a 500), `Attentions(project, openOnly)` (newest first, 100), `CloseAttention(id, by, reason)`
  (idempotent: closing a closed request returns it unchanged), `CloseSessionAttention(session, by)`;
  `Session.Attention bool` (list only, `json:"attention"`, always present). `cmd/bob/tools_attention.go`:
  `request_human_attention` (`message` required, `notice`; description adapted with expiry removed; result
  `{id, chat_url, note}`; `kind` ask|notice; `worker` = `Caller.Worker`), registered in `main.go`.
  `cmd/bob/attention.go`: webhook `BOB_ATTENTION_WEBHOOK_<NAME>` (name upper-cased, `-` as `_`, read at
  call time via `os.Getenv`; tests override `app.attentionWebhook`), POST of `{project, worker, kind, message,
  chat_url}` with a 10 s timeout in a goroutine tracked by `app.webhooks` (failure only logged), plus
  `GET /api/projects/{project}/attention[?state=open]` (`{attention:[...+chat_url]}`; without `state=open` the
  latest 100 incl. closed) and `POST /api/attention/{attention}/dismiss` (member; `closed_by` = the person's
  email). `guard` gained the `attention` path value and `storeProjectOf` the `"attention"` kind. `startTurn`
  closes the chat's open requests as `answered` (closed_by = sender) when the sender's email does not start
  `schedule:`; it does this after recording the message and before the turn runs. `deploy/compose.yml` api
  service gained `BOB_ATTENTION_WEBHOOK_ENC: ${...:-}` (optional) and `deploy/env.example` documents it (T25
  adds the wolf one). Tests: store lifecycle; tool stores a row and hits an `httptest` webhook with the exact
  body; notice/blank message/unreachable webhook; a person's reply closes, a schedule's does not; HTTP list
  (open vs all), dismiss, a member of another project gets 404 and the row stays open; `access_test.go` rows;
  `TestAttentionToolSchemasAreValidJSON`. Validation: vet clean, `gofmt -l .` empty, SKIP/FAIL count 0,
  `./stack test` "all green", compose config exit 0, `./stack build && ./stack restart`, `/healthz` ok.
  Manual (project `dev`, plain Claude chat via a throwaway cookie-minting script, deleted): the agent called
  the tool; `GET /api/projects/dev/attention?state=open` returned the row with a `chat_url`, the session list
  had `attention: true`; a reply left 0 open and the row read `answered` by the sender. Dismiss and the
  cross-project 404 are covered by tests only. The test chat was deleted (its row cascaded); no rows left.

### T24: Human attention — UI   [Status: done | Model: sonnet]
- **Scope:** Sidebar: a dot on a chat with `attention`, and a count next to the project name in the
  switcher (poll `GET …/attention?state=open` every 30 s, alongside the existing 30 s tick at
  `Sidebar.tsx:25-26`). `web/src/Attention.tsx`: a "Needs you" section on the Overview listing open
  requests (worker, message, age, link to the chat, Dismiss).
- **Files:** `web/src/{Attention,Sidebar,Overview,api}.tsx|ts`.
- **Acceptance criteria:** a request raised in a chat shows within 30 s; replying or dismissing
  clears it.
- **TDD:** no.
- **Validation:** `cd web && npx tsc -b` → clean; manual on `./stack start`: ask a dev chat to call
  `request_human_attention`; see the dot and the Overview entry; reply; both clear.
- **Depends on:** T23
- [x] done
- Notes: (executor) `api.ts`: `Attention` type, `Session.attention?`, `api.openAttention` and
  `api.dismissAttention`. `App.tsx` owns the open list (`attention` state): loaded on project change and
  re-polled every 30 s together with the session list (so the chat dots update; sessions were not polled
  before); it passes the count to `Sidebar` and the list plus an `onAttentionChanged` (reloads both) to
  `Overview`. `Sidebar.tsx`: red count pill next to the project name in the switcher trigger (current
  project only, not other projects in the menu), red dot on a chat row with `attention`. New
  `web/src/Attention.tsx` `NeedsYou` section (message, worker or "a chat", notice marker, age, "Open chat"
  link, Dismiss button); `Overview.tsx` shows it at the top of the left column only when there are open
  requests. Replying in a chat clears the badge on the next 30 s poll (no immediate refresh on send).
  Validation: `npx tsc -b` clean, `./stack test` "all green", `./stack build && ./stack restart`,
  `/healthz` ok. Manual: NO browser tool was available, so the UI was not viewed; verified through the
  endpoints the UI calls (throwaway cookie minter, deleted): a plain Claude chat in `dev` called
  `request_human_attention`; `?state=open` returned the row and the session list had `attention: true`;
  a reply left 0 open; a second request was dismissed via `POST /api/attention/{id}/dismiss`
  (`close_reason` dismissed, `closed_by` set) and open went to 0. Test chats deleted, 0 attention rows left.
  Browser check (later, headless Chromium via a throwaway Playwright script, since deleted; cookie minted in
  the script): on `#/p/dev` with one open request the sidebar showed the red dot on the chat row, the
  count pill "1" by the project name and the "Needs you" section on the Overview (screenshot
  `t24-before.png`); after a reply and a reload all three were gone (`t24-after.png`); a second request
  appeared on the 30 s poll without a reload, and clicking Dismiss in the UI cleared the section and
  pill with 0 open in the API. No UI fixes were needed. Test chat deleted, 0 rows left.

### T25: Wolf goes live   [Status: pending | Model: sonnet]
- **Scope:** `deploy/compose.yml`: add `bob-project-wolf` (`BOB_PROJECT_NAME: wolf`,
  `BOB_RUNTIME_TOKEN: ${BOB_RUNTIME_TOKEN_WOLF:?}`, `GITHUB_TOKEN: ${GITHUB_TOKEN_WOLF:?}` scoped
  to `badcodetv/wolf`, `FRED_API_KEY: ${FRED_API_KEY:?}`); `BOB_PROJECTS: enc,marketing,wolf`;
  add `BOB_RUNTIME_TOKEN_WOLF`, `OPENAI_API_KEY` and `BOB_ATTENTION_WEBHOOK_WOLF` (optional:
  `${BOB_ATTENTION_WEBHOOK_WOLF:-}`) to the `api` service; mirror in `deploy/env.example` and
  `.env.box`. **Before deploying** (Kai's go-ahead), as the postgres superuser on the box:
  `sudo docker exec -it <postgres container> psql -U postgres -d bob -c 'CREATE EXTENSION IF NOT EXISTS vector'`,
  and record it in the ops README (box-wide change). `./stack publish && ./stack deploy <tag>`;
  unpause schedules in the UI.
- **Files:** `deploy/{compose.yml,env.example}`, `.env.box` (not committed), `README.md`; ops `README.md`.
- **Acceptance criteria:** Wolf live check — in `wolf`, a plain chat creates an `interviewer`
  worker; the interviewer creates a hypothesis worker labelled `kind=hypothesis` and a schedule for
  it; two such schedules started together with "Run now" run one after the other; a hypothesis run
  writes a memory that another chat finds with `memory_search`; `request_human_attention` reaches
  the webhook and the UI.
- **TDD:** no.
- **Validation:** `TAG=x docker compose --env-file deploy/env.example -f deploy/compose.yml config >/dev/null`
  → exit 0; `ssh ubuntu@box.badcode.tv 'curl -sf http://127.0.0.1:8100/healthz'` → `ok`; the live
  check, observed by Kai.
- **Depends on:** T17, T20, T22, T24
- [ ] done
- Notes:

### T26: End-to-end verification and docs   [Status: in progress — docs done; clean-slate run + live checks wait on T25 | Model: sonnet]
- **Scope:** On the laptop from a clean state (Kai's go-ahead): `echo y | ./stack clean`;
  `./stack sql "DROP SCHEMA public CASCADE; CREATE SCHEMA public;"`; re-create the vector
  extension (the schema drop removed it) with the T21 superuser command; `./stack build &&
  ./stack start`. Run the three live checks (T16, T17, T25) against `dev`, on both engines.
  Update `README.md` (setup from `deploy/env.example`, env vars, Codex login, Drive token and
  OAuth client, `./stack deploy`) and `DESIGN.md` (decisions 1, 5 and 9 and the milestones
  rewritten to this plan; "Not doing" gains OpenCode for now).
- **Files:** `README.md`, `DESIGN.md`.
- **Acceptance criteria:** all gates green; no stale references.
- **TDD:** no.
- **Validation:** `./stack test` → "all green"; `cd web && npm run build` → succeeds;
  `grep -rn "projects.yaml\|BOB_RUNTIME_KEY\|bob-push\|sync.sh\|bob\.md\|BOB_REPO_\|import-agent-bob-env" --exclude-dir=node_modules --exclude-dir=dist --exclude-dir=design --exclude-dir=.stack --exclude-dir=.git .`
  → no matches; the three live checks pass.
- **Depends on:** T25
- [ ] done
- Notes: docs part done (orchestrator-assigned executor): `README.md` and `DESIGN.md` rewritten to the plan as built (decisions 1, 5 and 9, milestones, diagram now Claude Agent SDK · Codex, "Not doing" gains OpenCode for now). Clean-slate wipe, the three live checks and the box cutover are pending on Kai's tokens; the acceptance grep still matches comments in `stack`, `scripts/dev-api` and `deploy/compose.yml` (see the Discovered Issues Log).

## Discovered Issues Log
(appended by executors during implementation)

- **T1 (interim, fixed by later tickets):** `./stack start` no longer boots the API until T3 — it
  still sets `BOB_PROJECTS_FILE` and not `BOB_PROJECTS` / `BOB_RUNTIME_TOKEN_<NAME>` /
  `BOB_PUBLIC_URL`. `./stack test` is unaffected.
- **T1 (interim):** schedule create/update over HTTP take `worker_id` (not a name) until T5 resolves
  names; `web/src/Schedules.tsx` still sends `worker`, so creating a schedule from the UI returns
  400 "worker_id is required" until T5. There are no worker rows before T5 anyway.
- **T1 (interim):** `createSession` (`http.go`) still finds workers in git, so it passes a nil
  `worker_id`; such chats read as `worker_removed: true` on the session list until T5 looks workers
  up in the database. The web does not read `worker_removed` until T7.
- **T1:** the baseline follows the plan's `sessions.project REFERENCES projects(name)` (no
  `ON DELETE CASCADE`, which 001_init.sql had); `schedules.project` keeps its cascade as in
  003_schedules.sql. Projects are never deleted (only marked absent), so neither matters today.
- **T1:** an existing `bob` database (laptop or box) will not boot this API until its schema is
  reset: `001_baseline.sql` is a new migration name there, and its `CREATE TABLE projects` fails on
  the old table. Expected (the reset is planned); not done by the executor (needs Kai's go-ahead).
- **T2 (small scope addition):** `createSession` found workers through the runtime's `/workers`,
  which T2 deletes, so it now reads them from the database with `store.Worker(ctx, project, name)`
  — T4's exact signature, added beside `WorkerByID` in the minimal `store/workers.go`. Sessions
  created this way get their `worker_id` (fixing T1's interim `worker_removed: true`). T4 keeps
  both and adds the rest.
- **T2 (interim, fixed by later tickets):** the web still calls `GET /api/projects/{p}/workers`
  and `POST …/sync` (`web/src/api.ts:49-50`), now 404 — no workers list or Sync button works until
  T5 (route) / T6 (web). `README.md:93-95,210-211` still describe `bob-push` and those routes (T26).
- **T2 (interim):** `scripts/compose-projects.mjs` still gives project containers
  `BOB_RUNTIME_KEY` and `BOB_REPO_*`, not `BOB_RUNTIME_TOKEN` / `BOB_API_URL`, so a container from
  the new `bob-runtime` image exits at boot ("BOB_RUNTIME_TOKEN and BOB_API_URL are required") until
  T3's `deploy/compose.dev.yml` replaces it.
- **T2:** the executor inspected the built image with one `docker run --rm --entrypoint sh
  bob-runtime:dev -c 'ls …'` (a throwaway container, removed on exit) to confirm `sync.sh` and
  `bob-push` are gone and the git credential helper is kept.
- **T3 — port clash with Kai's Platinum project:** `platinum-development-carbon` (compose project
  `platinum`, `/home/kai/projects/bayesprice/Platinum`) publishes `0.0.0.0:8090`, the port
  `./stack start` runs the Go API on, so `./stack start` fails with "address already in use" while
  Platinum runs. The code is fine: run with `BOB_ADDR=:18090` the API booted and `/healthz`
  answered `ok`. Open question for Kai: stop Platinum while working on Bob, or move Bob's dev API off
  8090 (it is also the port `BOB_API_URL` in `deploy/compose.dev.yml` and the Vite proxy use).
  (Orchestrator correction of the executor's first wording, which called this another tenant of a
  sandbox and said Kai's laptop would not have it.)
  **Resolved (Kai, 2026-09-28):** Bob's API port is 8070 everywhere — the `BOB_ADDR` default, the
  API image, `deploy/compose.yml` (`127.0.0.1:8100:8070`, `BOB_API_URL: http://api:8070`),
  `deploy/compose.dev.yml`, `./stack` and the Vite proxy. Caddy is unaffected (it targets host
  port 8100). T3's text above still says 8090; read it as 8070.
- **T5 (small scope note):** `GET /api/projects/{project}/workers/{name}/versions` resolves the
  worker by name first (`a.store.Worker`), so it 404s once the worker is deleted, even though
  `store.WorkerVersions` itself works after deletion (no FK to `workers`, by design). T6's worker
  history UI reads versions of a *live* worker only, so this is fine for T5's and T6's scope as
  written; flagging in case a later ticket wants a deleted worker's history reachable (e.g. by
  worker id instead of by project+name).
- **T6:** the ticket's scope lines named specific git/sync text to remove but missed two spots the
  acceptance-criteria grep still caught: `Schedules.tsx`'s new-schedule dialog description ("Bob
  pulls from git and starts a new chat…") and a doc-comment on `ui.tsx`'s `ago()` ("for sync
  status"). Both fixed in T6's own scope, not left for later — the grep is T6's own acceptance
  criterion and these were plainly in scope (chat/schedule/worker UI text), not new work.
- **T6:** `Activity` ('starting'/'syncing') and the `files_root`/`WorkerList` plumbing weren't just
  text to delete — once `api.sync` and `WorkerList` were gone nothing else read them, so they were
  removed outright (`App.tsx`, `Sidebar.tsx`, `Overview.tsx`, `Files.tsx`) rather than left as dead
  state. `Files.tsx`'s Refresh button now just reloads the current folder's listing (no more sync
  status to key a cache-bust off of); the work folder's root is always `path ?? ''`.
- **T6:** no codex client module exists yet (only `engines/claude.ts`), so `WorkerEditor.tsx`
  defines its own local `codexEfforts` list (`minimal, low, medium, high, xhigh`, matching
  `workers.go`'s `codexEfforts` map) rather than importing one; model stays a free-text field for
  both engines since there's no codex model list to offer either.
- **T8 (small scope addition):** `Caller.User` needs the email on a session's latest
  `bob.user_message`; rather than read every event of the chat on each tool call, the store gained
  one read, `store.LastUserEmail(ctx, sessionID)` (`api/internal/store/store.go`), a single
  `ORDER BY id DESC LIMIT 1` query. Scheduled turns already record `user_email: "schedule:<id>"`
  on their `bob.user_message` (`schedules.go` → `startTurn`), so no special case was needed. A
  chat with no message yet gives `User: ""` (cannot happen in practice: a turn records its message
  before it runs).
- **T11:** Codex's `error` event is not always fatal: `codex exec` emits
  `{"type":"error","message":"Reconnecting... 1/5"}` while it retries, then may complete. The driver
  keeps the last `error` message but clears it on `turn.completed`; `turn.failed` always fails the
  turn. (The ticket said `error` → `TurnResult.error` outright.) T12's `codexMessages` should show
  such notices as transient, not as the turn's failure.
- **T11:** the SDK passes config as `--config` argv, so the chat's MCP token is on the `codex`
  process's command line (visible to `ps` inside that container). That is the same exposure
  Decision 5 accepts (the agent can read its own chat's token). Codex also supports
  `bearer_token_env_var` if that is ever preferred.
- **T11:** the SDK's `ModelReasoningEffort` also allows `max`, `ultra` and `persistent` beyond the
  API's `codexEfforts` (`minimal…xhigh`); left as is — T12 may want to revisit the list.
- **T12:** `docker exec bob-project-dev codex debug models` (codex-cli 0.158.0) shows no model
  actually supporting `minimal` reasoning effort — every listed model's
  `supported_reasoning_levels` starts at `low` (most going up through `xhigh`/`max`, some `ultra`).
  `codexEfforts` was kept exactly as the ticket specified (`minimal, low, medium, high, xhigh`,
  matching `workers.go`'s existing list from T6/T11) rather than changed to match; picking
  `minimal` for a Codex chat will presumably be rejected or ignored by the CLI itself, unverified
  since Codex isn't logged in yet. Left as-is per the ticket's literal text; flagging for whoever
  next touches Codex's effort list in case it's worth trimming `minimal` (or adding `max`/`ultra`,
  which every "list"-visibility model does support) once a live Codex chat can be checked.
- **T14:** plain `go get golang.org/x/oauth2@latest google.golang.org/api/drive/v3@latest` bumps
  `api/go.mod`'s `go` directive to 1.26.0, which the `golang:1.25-bookworm` image in this repo's
  `Dockerfile` cannot build. Fixed by pinning to `golang.org/x/oauth2@v0.30.0` and
  `google.golang.org/api/drive/v3@v0.244.0` (both current enough for `drive.readonly` and PKCE;
  `go.mod`'s `go` directive stayed `1.25.0`). Worth remembering for any later `go get` in this repo
  until the `Dockerfile`'s Go image is bumped on purpose.
- **T16 (interim, scope items 1/3 only):** README.md's "Running it on a server" section still
  described the old ops-generated deployment (`projects.yaml`, `scripts/compose-projects.mjs`,
  `BOB_PROJECTS_FILE`) as if it were current; updated the top of that section to describe
  `./stack publish`/`./stack deploy <tag>` instead and added a note that the rest (secrets in
  `.env.box`, the box cutover, retiring `apps/bob/` in ops) is T16 items 2/4/5, still pending —
  `apps/bob/` in the private ops repository is still what actually deploys today. Whoever does
  items 2/4/5 should finish rewriting that section (drop the `compose-projects.mjs` walkthrough
  entirely once nothing generates from `projects.yaml` anymore).
- **T20:** the Schedules page's "Last run" (and `schedule_list`'s `last_run`) is the schedule's
  newest run by id, so a Run now pressed on a schedule that already has a run queued records a
  newer `skipped` row that hides the queued one: the row shows "Skipped" and Run now is enabled
  again even though a run is still waiting (the History list shows both). Harmless (another press
  is skipped too), but `listSchedules` could prefer an active (queued/running) run as `last_run`.
  Also: after a restart the first tick dispatches a left-over queued run before looking at cron,
  and `NextQueuedRun` moves its `started_at` to now, so `LastCronRun` then counts firings missed
  during the outage from that start — they are neither caught up nor recorded as skipped (the
  schedule was busy anyway). Not changed.
- **T21:** `.env.box` does not exist on the laptop yet, and `deploy/compose.yml` now requires
  `OPENAI_API_KEY` (`:?`), so whoever writes `.env.box` (T16) must include it, and the box needs
  `CREATE EXTENSION vector` in `bob` as the superuser before the first deploy with T21 (T25 already
  plans that) — otherwise the API refuses to boot with the fix in its log. Also noticed: a stray
  throwaway database `bob_test_1790624190133788648` on the local Postgres, created before this
  ticket's first test run (not dropped — not mine; harmless, `DROP DATABASE … WITH (FORCE)` clears
  it); and `gofmt -l` flags `api/cmd/bob/tools_drive_test.go` (pre-existing, untouched).
- T22: `internal/mcp` `writeRPC` ignores the JSON encode error, so one tool with an invalid
  `InputSchema` makes `tools/list` answer 200 with an empty body and takes down ALL of Bob's tools for
  that chat (the agent reports "bob MCP failed to connect"). Not fixed (out of scope); worth a
  registration-time `json.Valid(InputSchema)` check in `Server.Register`.
- (orchestrator, T24 screenshots) Overview "Recent conversations": a plain chat (no worker) shows its subtitle as ", 1 message" — the worker name is empty and the comma stays. Cosmetic; pre-dates T24.
- **T26 (docs):** the acceptance grep still matches three code comments, none needing behaviour change: `stack` (header and the `deploy` leftover-removal line: `sudo rm -f .../compose.projects.yml .../projects.yaml` on the box is real, safe cleanup of the old ops deployment and can go once the cutover has run), `scripts/dev-api:2` (comment says "see scripts/import-agent-bob-env", a script that no longer exists; safe to reword) and `deploy/compose.yml:6` (comment naming `projects.yaml` as what ops used to generate from; safe to reword). Also: `runtime/Dockerfile` still sets `XDG_DATA_HOME=/project/.bob/opencode`, an OpenCode leftover the entrypoint still creates.
