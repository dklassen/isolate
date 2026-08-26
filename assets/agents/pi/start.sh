#!/bin/sh
set -e

# Pinned by `task agents:bump`; skip the new-release check.
export PI_SKIP_VERSION_CHECK=1

# Extensions are the dependencies in this directory's package.json other than
# pi itself ("isolate.agent"), installed from its lockfile at image build by
# install.sh. Load each with -e, which treats a directory as a local package:
# pi loads it in place and never installs or modifies it. Don't declare them
# in settings.json "packages": pi would install its own copies into
# ~/.pi/agent/npm at startup, outside the lockfile.
dir=$(dirname "$0")
#
# Fail loudly rather than start pi without its extensions: a failure inside a
# for list escapes set -e, and pi ignores an -e path that doesn't exist.
if ! exts=$(jq -re '(.isolate.agent | strings) as $agent | .dependencies | keys[] | select(. != $agent)' "$dir/package.json"); then
  echo "pi/start.sh: error: could not read extensions from $dir/package.json (needs valid JSON, an \"isolate.agent\" field, and at least one other dependency)" >&2
  exit 1
fi
for ext in $exts; do
  if [ ! -d "$dir/node_modules/$ext" ]; then
    echo "pi/start.sh: error: extension $ext is in package.json but not installed at $dir/node_modules/$ext; rebuild the image with: task build" >&2
    exit 1
  fi
  set -- -e "$dir/node_modules/$ext" "$@"
done

exec pi "$@"
