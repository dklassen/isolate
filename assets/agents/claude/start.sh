#!/bin/sh
set -e

# Auth is a live-mounted OAuth session, not a baked-in key: on first run
# against a fresh --config directory, no credentials exist yet, so claude
# will need an interactive `/login` once it starts. That login persists
# into the mounted --config directory (see agent.go), so it's only needed
# once per --config directory, not once per container run.
if [ ! -f /root/.claude/.credentials.json ] && [ -z "$ANTHROPIC_API_KEY" ] && [ -z "$CLAUDE_CODE_OAUTH_TOKEN" ]; then
  echo "claude/start.sh: no claude credentials found in the mounted --config directory -- run '/login' once claude starts" >&2
fi

# Updates would be lost with the container; bump via `task agents:bump`.
export DISABLE_AUTOUPDATER=1

exec claude "$@"
