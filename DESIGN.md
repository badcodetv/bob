# Bob — design

Bob runs AI agent sessions for BadCode, internally. It replaces `agent-bob`, which grew to ~88k
lines around a problem this design no longer has (snapshotting session containers). The rule for
this repository: **the frontier labs' harnesses own the agent features** (sub-agents, worktrees,
skills, tool loops); Bob only gives them a computer, configuration, memory and a record.

## The shape

```
 browser ──► Bob API (Go) ──► Postgres        workers, conversations, schedules, memory
               │  ▲ /mcp: Bob's tools — workers, schedules, memory, Drive, human attention
               │ HTTP, by container name. Bob starts nothing and has no Docker socket.
               ▼
        one container per project   declared in the deploy's compose file
          ├─ runtime server (TS)     routes a turn to the right harness, streams native events
          ├─ Claude Agent SDK · Codex
          └─ volume /project         shared work folder, skills, harness state
```

## Decisions

1. **One container per project, declared in the compose file.** Bob does not start it: a project
   is deployment configuration, so its container is declared beside every other service and
   compose keeps it running. Bob reaches it by name and **has no Docker socket at all**. No
   Docker-in-Docker, no fleet, no port pool, no snapshots. Isolation beyond "one container" is a
   VM, later, if ever. The projects Bob serves are `BOB_PROJECTS`, each with a runtime token of
   its own (`BOB_RUNTIME_TOKEN_<NAME>`), and the deploy is owned by this repository —
   `deploy/compose.yml`, `deploy/env.example`, `./stack deploy <tag>` — not generated elsewhere.
   (Revised 2026-09-21 and 2026-09-28; see
   [design/2026-09-21-projects-in-compose.md](design/2026-09-21-projects-in-compose.md) and
   [design/2026-09-28-foundation-plan.md](design/2026-09-28-foundation-plan.md).)
2. **One base image, `bob-runtime`.** A project that needs software (FFmpeg, …) has a Dockerfile
   `FROM bob-runtime` that only installs things. It never changes the entrypoint.
3. **The volume is a cache; Postgres is the record.** Harness state lives on the volume
   (`CLAUDE_CONFIG_DIR`, `CODEX_HOME`, `XDG_DATA_HOME`), so normal turns resume natively. Every
   event is also stored in Postgres, so a session can be cold-booted when the volume is gone.
4. **Native events, no common format.** A session's engine never changes, so each event is
   stored as the harness emitted it, in one envelope: `session, seq, engine, kind, payload`.
   Code that reads events switches on engine. The UI converts per engine, at display time only.
5. **Postgres holds configuration and conversations; git holds the project's work.** Workers
   (engine, model, effort, tools, labels, system prompt) and each project's shared prompt live in
   the database, and every change is a version with who, when and a required *why* — people edit
   them in the web app, agents through MCP tools. The API composes each turn's system prompt (an
   environment note, the project prompt, the worker's prompt) and hands it to a runtime that reads
   no configuration. What a project's agents produce — code, data, notes, reports — lives in git:
   chats clone repositories into the project's one shared work folder and commit and push from
   there. A project's secrets are environment variables on its compose service. Git never holds a
   secret or a conversation.
6. **Harnesses mix within a project.** Each worker names its engine.
7. **A schedule invokes a worker.** Workers have no schedules of their own. A schedule starts a
   new chat each time it fires, never two at once, and one switch pauses them all.
8. **No model proxy, no mock model.** Credentials reach a project as environment variables on its
   compose service, so each project sees only what it is given. This is an internal tool: every
   run, scheduled ones included, uses Kai's subscription logins, with Kai present. An API-key path
   exists (`ANTHROPIC_API_KEY`) but is not the default.
9. **Tools are MCP servers, and Bob's is in the API.** One endpoint (`/mcp`, JSON-RPC over
   streamable HTTP) gives every chat workers, schedules, memory, read-only Google Drive and
   `request_human_attention`, scoped to the chat's project by a per-chat token the API mints and
   sends with each turn. Memory is hybrid search — labels, full-text and pgvector embeddings fused
   by Reciprocal Rank Fusion — over an append-only store. Drive is a small read-only client in the
   API (`drive.readonly`, one refresh token per project), not a proxy to Google's hosted MCP.
   Files a chat writes go in its project's work folder, not a separate files server.

## Milestones

- **M1** — API talks to a project container; a Claude worker answers a message; events are stored
  in Postgres and streamed back. *(done)*
- **M2** — Google login, UI on assistant-ui (projects, workers, chat), per-project secrets, and
  schedules with a global pause switch. *(done)*
- **M3** — Foundation ([design/2026-09-28-foundation-plan.md](design/2026-09-28-foundation-plan.md)):
  workers and the project prompt in Postgres with history; Bob's MCP server; Codex as a second
  engine; skills; Google Drive; schedule and worker tools; labelled hybrid memory; human attention
  (a badge, an Overview list, an optional webhook); one scheduled run at a time per project; and
  this repository's own `./stack deploy`. *(built and tested; not yet live on the box)*
- **M4** — Live, in order: ENC (Codex, a Drive-fed canon written to its repository), the marketing
  manager, then Wolf (a worker per hypothesis, each on a schedule). Each waits on logins and
  tokens only a person can create, and on the box cutover.

Earlier plans, now folded in: **projects in compose** (2026-09-21: Bob stops talking to Docker and
the encrypted-secrets subsystem goes, so a secret is a compose environment variable and changing
one is a deploy) and **Wolf as a Bob project** (2026-09-17:
[design/2026-09-17-wolf-as-a-bob-project.md](design/2026-09-17-wolf-as-a-bob-project.md) — Wolf is
an ordinary Bob project: per-project access map, the signed-in person in each turn, schedules,
a sandboxed file viewer).

## Not doing

Config log, revert, git projection, onboarding/charter/architect, topologies, test labs,
embedding for other apps, datasets, skills/images stores, event subscriptions and dispatch gate,
console pages beyond chat, OpenCode as a third engine (for now). Each can come back if a real use needs it.
