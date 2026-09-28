#!/bin/sh
# Boot a project container: make the project's folders on the volume and point each harness's
# state at it, then start the runtime server. Nothing is fetched: config comes with each turn, and
# chats clone whatever repositories they need into the work folder themselves.
set -eu
P="${BOB_PROJECT_DIR:-/project}"
mkdir -p "$P/work" "$P/skills" "$CLAUDE_CONFIG_DIR" "$CODEX_HOME" "$XDG_DATA_HOME"
exec node /app/dist/server.js
