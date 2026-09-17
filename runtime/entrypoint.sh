#!/bin/sh
# Boot a project container: point each harness's state at the volume, fetch the project's
# git folder, then start the runtime server.
set -eu
P="${BOB_PROJECT_DIR:-/project}"
mkdir -p "$P/work" "$CLAUDE_CONFIG_DIR" "$CODEX_HOME" "$XDG_DATA_HOME"
/bin/sh /app/sync.sh
exec node /app/dist/server.js
