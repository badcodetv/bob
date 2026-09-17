#!/bin/sh
# Boot a project container: fetch the project's git folder, point each harness's state at the
# volume, link the project's skills in, then start the runtime server.
set -eu
P="${BOB_PROJECT_DIR:-/project}"
mkdir -p "$P/work" "$CLAUDE_CONFIG_DIR" "$CODEX_HOME" "$XDG_DATA_HOME"

if [ -n "${BOB_REPO_URL:-}" ]; then
  auth=""
  [ -n "${GITHUB_TOKEN:-}" ] && auth="http.extraHeader=Authorization: Basic $(printf 'x-access-token:%s' "$GITHUB_TOKEN" | base64 -w0)"
  git_() { if [ -n "$auth" ]; then git -c "$auth" "$@"; else git "$@"; fi; }
  if [ -d "$P/repo/.git" ]; then
    git_ -C "$P/repo" fetch --quiet origin && git -C "$P/repo" reset --quiet --hard "origin/${BOB_REPO_REF:-main}" \
      || echo "bob-runtime: git refresh failed, keeping the existing checkout" >&2
  else
    git_ clone --quiet --branch "${BOB_REPO_REF:-main}" "$BOB_REPO_URL" "$P/repo"
  fi
fi

config="$P/repo/${BOB_REPO_SUBFOLDER:-}"
if [ -d "$config/skills" ]; then
  ln -sfn "$config/skills" "$CLAUDE_CONFIG_DIR/skills"
fi

exec node /app/dist/server.js
