# Bob — design

Bob runs AI agent sessions for BadCode, internally. It replaces `agent-bob`, which grew to ~88k
lines around a problem this design no longer has (snapshotting session containers). The rule for
this repository: **the frontier labs' harnesses own the agent features** (sub-agents, worktrees,
skills, tool loops); Bob only gives them a computer, configuration, memory and a record.

## The shape

```
 browser ──► Bob API (Go) ──► Postgres        conversations, schedules, memory
               │
               │ HTTP, by container name. Bob starts nothing and has no Docker socket.
               ▼
        one container per project   declared in the deploy's compose file
          ├─ runtime server (TS)     routes a turn to the right harness, streams native events
          ├─ Claude Agent SDK · Codex · OpenCode
          └─ volume /project         repo checkout, per-session work dirs, harness state
```

## Decisions

1. **One container per project, declared in the compose file.** Bob does not start it: a project
   is deployment configuration, so its container is declared beside every other service and
   compose keeps it running. Bob reaches it by name and **has no Docker socket at all**. No
   Docker-in-Docker, no fleet, no port pool, no snapshots. Isolation beyond "one container" is a
   VM, later, if ever. (Revised 2026-09-21; see
   [design/2026-09-21-projects-in-compose.md](design/2026-09-21-projects-in-compose.md).)
2. **One base image, `bob-runtime`.** A project that needs software (FFmpeg, …) has a Dockerfile
   `FROM bob-runtime` that only installs things. It never changes the entrypoint.
3. **The volume is a cache; Postgres is the record.** Harness state lives on the volume
   (`CLAUDE_CONFIG_DIR`, `CODEX_HOME`, `XDG_DATA_HOME`), so normal turns resume natively. Every
   event is also stored in Postgres, so a session can be cold-booted when the volume is gone.
4. **Native events, no common format.** A session's engine never changes, so each event is
   stored as the harness emitted it, in one envelope: `session, seq, engine, kind, payload`.
   Code that reads events switches on engine. The UI converts per engine, at display time only.
5. **Git holds configuration and the project's work; the database holds conversations.** A
   project points at a repository and a subfolder holding `bob.md` (what the project is and how
   its workers coordinate, appended to every system prompt), `workers/*.md` (engine, model,
   effort, tools, system prompt) and `skills/`. Prompts are written offline, with Claude
   Code, and pushed. The same repository holds what the project's agents produce — code, data,
   notes, reports — committed and pushed from each chat's worktree (pull before push).
   Conversations, schedules and memory live in Postgres. A project's secrets are environment
   variables on its compose service. Git never holds a secret or a conversation.
6. **Harnesses mix within a project.** Each worker names its engine.
7. **A schedule invokes a worker.** Workers have no schedules of their own. A schedule starts a
   new chat each time it fires, never two at once, and one switch pauses them all.
8. **No model proxy, no mock model.** Credentials reach a project as environment variables on its
   compose service, so each project sees only what it is given. This is an internal tool: every
   run, scheduled ones included, uses Kai's subscription logins, with Kai present. An API-key path
   exists (`ANTHROPIC_API_KEY`) but is not the default.
9. **Tools are MCP servers:** Bob's core server (memory, `request_human_attention`), a small
   Google Cloud Storage files server (`files_save/load/list`, a folder per session), and the
   Google Drive/Gmail connection carried over from agent-bob.

## Milestones

- **M1** — API starts a project container; a Claude worker defined in git answers a message;
  events are stored in Postgres and streamed back. *(done)*
- **M2** — Codex worker in the same project, on a ChatGPT subscription. Cold boot from Postgres
  for Claude (`SessionStore`) and Codex (rollout file).
- **M3** — Google login, UI on assistant-ui (projects, workers, chat), git sync, per-project secrets. *(done)*
- **M4** — Memory (carried over: labels, selectors, hybrid search), files MCP, human attention.
- **M5** — Schedules *(done)*, Google Drive/Gmail, usage report. Then Wolf.

**Projects in compose** (Kai, 2026-09-21): Bob stops talking to Docker; each project's container
is declared in the deploy's compose file, and which projects exist is a file Bob reads at boot.
The encrypted-secrets subsystem is deleted — a secret is a compose environment variable now, and
changing one is a deploy. Plan and tickets:
[design/2026-09-21-projects-in-compose.md](design/2026-09-21-projects-in-compose.md).

**Wolf as a Bob project** (Kai, 2026-09-17): Wolf is not a separate app; it is an ordinary Bob
project. The Bob work it needs is Part A of
[design/2026-09-17-wolf-as-a-bob-project.md](design/2026-09-17-wolf-as-a-bob-project.md) —
A1 per-project access map, A2 the signed-in person in each turn, A3 push from worktrees, A4
schedules (plus a global pause switch), A5 a sandboxed repo file viewer, A6 secrets pass-through
and the per-project secrets table. *(All built 2026-09-17.)* Codex (M2) is next.

## Not doing

Config log, revert, git projection, onboarding/charter/architect, topologies, test labs,
embedding for other apps, datasets, skills/images stores, event subscriptions and dispatch gate,
console pages beyond chat. Each can come back if a real use needs it.
