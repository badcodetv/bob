# Bob — design

Bob runs AI agent sessions for BadCode, internally. It replaces `agent-bob`, which grew to ~88k
lines around a problem this design no longer has (snapshotting session containers). The rule for
this repository: **the frontier labs' harnesses own the agent features** (sub-agents, worktrees,
skills, tool loops); Bob only gives them a computer, configuration, memory and a record.

## The shape

```
 browser ──► Bob API (Go) ──► Postgres        conversations, secrets, schedules, memory
               │
               │ host Docker socket (only the API has it)
               ▼
        one container per project   image: bob-runtime (or a project image FROM it)
          ├─ runtime server (TS)     routes a turn to the right harness, streams native events
          ├─ Claude Agent SDK · Codex · OpenCode
          └─ volume /project         repo checkout, per-session work dirs, harness state
```

## Decisions

1. **One container per project**, started by the API through the host Docker socket. No
   Docker-in-Docker, no fleet, no port pool, no snapshots. Isolation beyond "one container" is
   a VM, later, if ever. Project containers never get the socket.
2. **One base image, `bob-runtime`.** A project that needs software (FFmpeg, …) has a Dockerfile
   `FROM bob-runtime` that only installs things. It never changes the entrypoint.
3. **The volume is a cache; Postgres is the record.** Harness state lives on the volume
   (`CLAUDE_CONFIG_DIR`, `CODEX_HOME`, `XDG_DATA_HOME`), so normal turns resume natively. Every
   event is also stored in Postgres, so a session can be cold-booted when the volume is gone.
4. **Native events, no common format.** A session's engine never changes, so each event is
   stored as the harness emitted it, in one envelope: `session, seq, engine, kind, payload`.
   Code that reads events switches on engine. The UI converts per engine, at display time only.
5. **Git is configuration; the database is everything else.** A project points at a repository
   and a subfolder holding `workers/*.md` (engine, model, effort, tools, system prompt) and
   `skills/`. Prompts are written offline, with Claude Code, and pushed. Conversations, secrets,
   schedules and memory live in Postgres. Git never holds a secret or a conversation.
6. **Harnesses mix within a project.** Each worker names its engine.
7. **A schedule invokes a worker.** Workers have no schedules of their own.
8. **No model proxy, no mock model.** Credentials are passed into the project container. This is
   an internal tool; subscription logins are used only by their owner.
9. **Tools are MCP servers:** Bob's core server (memory, `request_human_attention`), a small
   Google Cloud Storage files server (`files_save/load/list`, a folder per session), and the
   Google Drive/Gmail connection carried over from agent-bob.

## Milestones

- **M1** — API starts a project container; a Claude worker defined in git answers a message;
  events are stored in Postgres and streamed back. *(this commit)*
- **M2** — Codex worker in the same project, on a ChatGPT subscription. Cold boot from Postgres
  for Claude (`SessionStore`) and Codex (rollout file).
- **M3** — Secrets table, Google login, UI on assistant-ui (projects, sessions, chat).
- **M4** — Memory (carried over: labels, selectors, hybrid search), files MCP, human attention.
- **M5** — Schedules, Google Drive/Gmail, usage report. Then Wolf.

## Not doing

Config log, revert, git projection, onboarding/charter/architect, topologies, test labs,
embedding for other apps, datasets, skills/images stores, event subscriptions and dispatch gate,
console pages beyond chat. Each can come back if a real use needs it.
