# Bob

Runs AI agent sessions for BadCode: one container per project, the labs' own harnesses inside it,
configuration from git, conversations in Postgres. Read [DESIGN.md](DESIGN.md) first.

```
api/        Go API — projects, sessions, turns, stored events, Docker control
runtime/    bob-runtime image — the small server inside each project container
examples/   an example project config folder (workers/, skills/)
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
docker compose up -d postgres
(cd runtime && npm ci && docker build -t bob-runtime:dev .)
(cd api && BOB_DATABASE_URL=postgres://bob:bob@127.0.0.1:5433/bob \
   BOB_PASS_ENV=CLAUDE_CODE_OAUTH_TOKEN,ANTHROPIC_API_KEY,GITHUB_TOKEN go run ./cmd/bob)
```

Then, with a config repo (a GitHub URL plus `GITHUB_TOKEN`, or a local repo for development):

```sh
curl -XPOST localhost:8090/api/projects \
  -d '{"name":"demo","repo_url":"file:///seed","repo_mount":"/abs/path/to/a/git/repo"}'
curl localhost:8090/api/projects/demo/workers
curl -XPOST localhost:8090/api/projects/demo/sessions -d '{"worker":"assistant"}'   # → id
curl -XPOST localhost:8090/api/sessions/<id>/messages -d '{"text":"hello"}'
curl -N localhost:8090/api/sessions/<id>/stream                                     # SSE
```

Changed a project's settings or pushed new config? `POST /api/projects/<name>/restart` recreates
its container (the volume, and so every session, is kept).

## API

| | |
| --- | --- |
| `GET/POST /api/projects` | list, create |
| `POST /api/projects/{p}/restart` | recreate the container, keep the volume |
| `GET /api/projects/{p}/workers` | workers read from git |
| `GET/POST /api/projects/{p}/sessions` | list, create `{worker}` |
| `GET /api/sessions/{id}` | one session |
| `POST /api/sessions/{id}/messages` | `{text}` → 202; the turn runs in the background |
| `POST /api/sessions/{id}/interrupt` | stop the running turn |
| `GET /api/sessions/{id}/events?after=` | stored events |
| `GET /api/sessions/{id}/stream` | SSE: stored events, then live ones (token deltas are live-only) |

Events are stored exactly as the harness emitted them, with `engine` and `kind` beside the
payload. Bob's own events are `bob.user_message`, `bob.turn_done` and `bob.turn_failed`.

## Tests

```sh
(cd api && go vet ./... && go test ./...)
(cd runtime && npx tsc -p . && npm test)
```
