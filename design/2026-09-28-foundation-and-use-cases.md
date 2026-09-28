# Foundation decisions and the three use cases — running record

*Started 2026-09-28 in an interview between Kai and a Claude session. Nothing here is built. This
file is filled in as the interview goes; the use-case sections come after the foundation. Where it
disagrees with DESIGN.md or `2026-09-21-projects-in-compose.md`, this file is newer.*

## Foundation decisions (Kai, 2026-09-28)

1. **One container per project stays.** Kai reconsidered a single shared container for all
   projects and rejected it again: per-project containers give each project its own disk,
   secrets, image and GitHub token, and a mistake in one project cannot reach another.
2. **Workers live in the database, not git.** Four core MCP tools: `worker_create`,
   `worker_update`, `worker_list`, `worker_delete`. Every change is kept in a version table (who,
   when, why), which replaces git as the prompt history. Workers can also be edited in the UI.
3. **A project needs no git repository.** Bob clones nothing. A new project starts with an empty
   work folder; chats clone whatever repositories the project needs, one level below that folder.
   All git is the project's own business, taught by worker prompts — including "if you get stuck
   in git, stop and call `request_human_attention`".
4. **No per-chat worktrees.** Chats share the project's work folder. The risk of one chat
   overwriting another is accepted and handled by worker prompts.
5. **The project prompt** (Kai calls it "claude.md" in Bob terms) is stored in the database and
   **prepended by Bob** to every worker's system prompt, for every harness. Bob does not write
   CLAUDE.md or AGENTS.md at the project root; those files stay inside the repositories that own
   them, and each harness finds them natively. Replaces `bob.md`.
6. **Skills are a `skills/` folder on the project's disk**, linked into each harness's lookup
   path (Claude, Codex, OpenCode — verify each path when building). Chats may edit it.
7. **No `projects.yaml`, no compose generator.** The compose file is written by hand. The API
   learns which projects exist from one variable on its service: `BOB_PROJECTS=wolf,enc,...`
   (comma-separated). A project listed there without a running container is a loud error.
8. **Runtime tokens: one random token per project in `.env`** (e.g. `BOB_RUNTIME_TOKEN_WOLF`),
   given to that project's container and to the API. Replaces derivation from `BOB_RUNTIME_KEY`.
9. **The compose file lives in the `bob` repository**, not the ops repo. Every other repository is
   just a project repository. Secret *values* stay in `.env` on the server; the compose file only
   names variables (this repository is public).
10. **Secrets are environment variables** from `.env`, passed per project in the compose file.
11. **Every project has a plain chat with no worker**: the project prompt only, plus the project's
    tools — including `worker_create`, so a brand-new project can use this chat to create its
    first worker. Workers never depend on git: a project with no repository at all still has
    workers and gets work done.

## Foundation work implied (to plan, not yet ticketed)

- Move workers and the project prompt from git into Postgres; worker CRUD tools + version table.
- Remove config-repo cloning, `BOB_REPO_*`, `bob.md`, per-chat worktrees, `projects.yaml`,
  `scripts/compose-projects.mjs`, the ops generator; move the box's compose file into this repo
  and point the ops deploy at it.
- Port from agent-bob: shared memory (labels, selectors, hybrid search), Google connect
  (Drive/Gmail), `request_human_attention`.
- Harnesses: Codex, then OpenCode.
- Skills folder linked for all three harnesses.

## Use cases

*To be filled from the interviews: Agent Wolf, Emperor's New Coin (ENC), BadCode marketing
manager.*

### Agent Wolf (interview done 2026-09-28)

Answers so far (Kai, 2026-09-28):

- The repository stays `badcodetv/wolf` — Wolf is a BadCode project.
- **One worker per hypothesis.** An interviewer worker talks a hypothesis through with a person,
  agrees how it will be judged, then creates the hypothesis's own worker (`worker_create`) and its
  schedule. That worker's prompt knows the hypothesis and how to pull data, process it, remember
  things in memory, and contribute its page to the shared website. Workers are, in effect, Wolf's
  primitive. Replaces the single shared researcher built on 2026-09-17.
- **The method may change.** A hypothesis worker may adjust how it measures as it learns.
- **Users: Kai plus a few invited people.**
- **No ranked leaderboard.** A dashboard giving an overview of every hypothesis.
- **The dashboard is public, on GitHub Pages.** Static HTML plus JSON/CSV data files, rebuilt and
  pushed by each run.
- **Finished hypotheses: delete the worker.** Archiving can come later.
- **A shared low-level toolkit** (the existing `./wolf` tool: fetch a value from Yahoo/FRED, and
  other generic helpers). Each hypothesis worker decides how to grade itself; the toolkit only
  does the reusable parts. Proposed (not yet confirmed): the toolkit stays in the Wolf repo, which
  the project clones; no custom image, since Node is already in `bob-runtime`.

Foundation work this adds:

- **Worker labels**: key/value pairs on each worker, filterable in the sidebar. Bob stamps
  "created by / created at / updated at" itself; the rest is free.
- **A schedule tool** so the interviewer can give the new worker its timetable.
- **One scheduled run at a time per project**: scheduled runs queue rather than overlap, because
  every chat shares one work folder. Human chats are not queued.
- Deleting a worker must keep its past chats readable.

### Emperor's New Coin — ENC (interview done 2026-09-28)

Answers so far (Kai, 2026-09-28):

- **People: Kai and Richard**, the only two on the project.
- **The Google Drive is large**: many folders, likely over 100 documents — Google Docs and
  articles, few voice memos or scripts.
- **Two phases, not a pipeline.**
  1. **Ingest into the canon**: raw material from the world (the Drive first) is taken into a
     central body of research, "the canon".
  2. **Build content on the canon**: separately, people decide to turn parts of the canon into
     content. One piece of the canon can feed many pieces of content.
- **The website does not exist yet.** Richard has designs. Kai may build the first structure in an
  ordinary offline Claude Code session, committed to the same repository Bob will use. After that,
  Bob is the main way the site is updated and redesigned — "Bob is the CMS".
- **Stack and hosting undecided**: a simple static front-end framework, probably Cloudflare Pages.
- **Decided: the canon is markdown files in the git repo.** The repo is ENC's library.
- **Decided: ingesting is interactive, not scheduled.** One large first ingest done together with
  Bob in chat; after that, a person adds something to the Drive and asks a chat to take that
  document into the canon. ENC needs no schedules.
- **Decided: no separate approval step.** A person prompts a worker to build content from the
  canon into the site; the human in the chat is the approval.
- **Decided: Google Drive is read-only for Bob.** Bob never writes to the Drive (so the Drive
  scope can be read-only).
- **Decided: Drive only for now.** Gmail will likely come later; no immediate need. ENC's job:
  read documents from Drive, curate the canon and the website in GitHub. The website is the only
  output for now.
- **Decided: start with no workers.** Kai and Richard create workers by hand in the Bob UI and
  iterate on the org structure themselves.

Foundation work this adds: **a worker editor in the UI** (create, edit prompt/engine/model/tools,
delete) — today workers are git files with no UI. Google Drive connect (read-only) is ENC's
blocker.

- **Decided: `badcodetv/wolf` becomes public**, and GitHub Pages publishes the site from it (the
  organisation is on GitHub's free plan, which cannot publish from a private repo). One repository
  for code, data and site. Pages publishes from a branch only at the root or `/docs`; the site
  lives in `site/`, so either a small GitHub Actions workflow deploys `site/` on each push to
  `main`, or `site/` is renamed `docs/`. Before flipping it public, scan the history for secrets:
  17 commits, all of them become public permanently.
- **Decided: only scheduled runs queue.** People can chat while a run is going.
- **Decided: every hypothesis page shows at minimum** the verdict so far, a chart, the date of the
  last run, and what changed since the previous run. Everything else is the worker's choice, and
  this minimum will change as Wolf is used. 

### BadCode marketing manager (interview done 2026-09-28)

Answers so far (Kai, 2026-09-28):

- **What is marketed: BadCode itself** — a political-economic art collective releasing YouTube
  videos of stories and songs. The goal is BadCode's general presence.
- **Channels: as many as possible** — TikTok, YouTube, Instagram, X, a mailing list, Patreon.
- **Learning is part of the job.** The marketing manager researches other creators of engaging,
  educational content and which channels they use to promote themselves; BadCode learns how to
  do marketing by building this worker.
- **Bob does not post, to start.** Connecting social-media credentials is painful. Bob writes
  content into a repository and people post it by hand. Later: connect credentials and let Bob
  post on a schedule.
- **Email: the badcodetv@gmail.com inbox.** What Bob does with it (drafts or replies) is not
  known yet.
- **Decided: content goes in `badcodetv/core`**, the monorepo that already holds the stories.
- **Decided: no Gmail to start.** Research plus content in the repo is the first milestone.
- **Decided: chat only for now**, no schedules.
- **People: Kai and Jack.**

## What each project needs from the foundation

| Foundation piece | Wolf | ENC | Marketing |
| --- | --- | --- | --- |
| Workers in the database + worker editor UI | yes | **blocker** | yes |
| Plain chat (no worker) | yes | **blocker** (first ingest) | yes |
| Worker tools (`worker_create` …) + labels | **blocker** (interviewer) | later | later |
| Schedule tool + one-scheduled-run-at-a-time | **blocker** | no | no |
| Google Drive connect (read-only) | no | **blocker** | no |
| Gmail connect | no | later | later |
| Shared memory | yes | nice to have | nice to have |
| `request_human_attention` | yes (unattended runs) | nice to have | nice to have |
| Codex harness (ChatGPT subscription) | yes | **blocker** (ENC runs on Codex) | yes |
| OpenCode harness (other models) | yes | yes | yes |
| Remove git config, worktrees, projects.yaml; compose in this repo | all | all | all |

Every project also needs the harness's own web search and a GitHub token scoped to its repos.

## Build decisions (Kai, 2026-09-28)

- **Order: ENC, then marketing, then Wolf.**
- **All three harnesses from day one.** ENC will run on Kai's Codex (ChatGPT) subscription, and
  OpenCode gives access to other models; adding harnesses later is feared to be a large change.
- **`badcodetv/core` being public is fine** — BadCode builds in public, drafts included.
