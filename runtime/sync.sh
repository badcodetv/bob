#!/bin/sh
# Bring /project/repo to the tip of the project's branch. Used at boot and by POST /sync.
# Never fails the container: the outcome is written to $P/.bob/sync.json for /workers to report.
P="${BOB_PROJECT_DIR:-/project}"
status="$P/.bob/sync.json"
mkdir -p "$P/.bob"
ref="${BOB_REPO_REF:-main}"

if [ -z "${BOB_REPO_URL:-}" ]; then
  printf '{"ok":false,"error":"this project has no repo_url"}\n' > "$status"; exit 0
fi
auth=""
[ -n "${GITHUB_TOKEN:-}" ] && auth="http.extraHeader=Authorization: Basic $(printf 'x-access-token:%s' "$GITHUB_TOKEN" | base64 -w0)"
git_() { if [ -n "$auth" ]; then git -c "$auth" "$@"; else git "$@"; fi; }

out=$(
  if [ -d "$P/repo/.git" ]; then
    git -C "$P/repo" remote set-url origin "$BOB_REPO_URL" &&
    git_ -C "$P/repo" fetch --quiet origin "$ref" && git -C "$P/repo" reset --quiet --hard FETCH_HEAD
  else
    rm -rf "$P/repo" && git_ clone --quiet --branch "$ref" "$BOB_REPO_URL" "$P/repo"
  fi 2>&1
)
if [ $? -eq 0 ]; then
  commit=$(git -C "$P/repo" log -1 --format='%h %s' | sed 's/\\/\\\\/g; s/"/\\"/g')
  printf '{"ok":true,"commit":"%s"}\n' "$commit" > "$status"
else
  msg=$(printf '%s' "$out" | tr '\n' ' ' | sed 's/\\/\\\\/g; s/"/\\"/g')
  printf '{"ok":false,"error":"%s"}\n' "$msg" > "$status"
fi

config="$P/repo/${BOB_REPO_SUBFOLDER:-}"
if [ -d "$config/skills" ]; then ln -sfn "$config/skills" "$CLAUDE_CONFIG_DIR/skills"; fi
exit 0
