#!/bin/sh
set -e

agent="${1:?usage: entrypoint.sh <agent> [args...]}"
shift

# Note:: This is a part of a bootstrapping process for Go projects
# the agent would need to build to operate.
# This doesn't really fit here as its likely a conditional on the
# folder being mounted and what the intent is.
if [ -f /workspace/go.mod ]; then
  echo "entrypoint.sh: detected Go project in /workspace, downloading module dependencies..."
  if ! (cd /workspace && go mod download); then
    echo "entrypoint.sh: warning: 'go mod download' failed; continuing without prefetched dependencies" >&2
  fi
fi

# Everything agent-specific (package reconciliation, credential checks, the
# final exec) lives in that agent's own start script, copied from
# assets/agents/<agent>/ at image build time. The directory names match the
# agent registry in agent.go.
start="/opt/isolate/agents/$agent/start.sh"
if [ ! -x "$start" ]; then
  echo "entrypoint.sh: unknown agent '$agent'" >&2
  exit 1
fi
exec "$start" "$@"
