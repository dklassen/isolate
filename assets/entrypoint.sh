#!/bin/sh
set -e

agent="${1:?usage: entrypoint.sh <agent> [args...]}"
shift

# Only the setup below runs as root. Everything after it runs as the agent
# user with no capabilities and no_new_privs, so it can't change the
# firewall or /etc/hosts, or regain root through a setuid binary.
as_agent="setpriv --reuid=agent --regid=agent --init-groups --inh-caps=-all --bounding-set=-all --no-new-privs --"

# ISOLATE_EGRESS=host:port (--network ollama in network.go) allows outbound
# traffic to that one address only. Any failure here aborts the run rather
# than starting unrestricted.
if [ -n "$ISOLATE_EGRESS" ]; then
  egress_host="${ISOLATE_EGRESS%:*}"
  egress_port="${ISOLATE_EGRESS##*:}"
  egress_ip=$(getent ahostsv4 "$egress_host" | awk 'NR == 1 { print $1 }')
  if [ -z "$egress_ip" ]; then
    echo "entrypoint.sh: error: cannot resolve $egress_host; refusing to start without the egress firewall" >&2
    exit 1
  fi
  # DNS is blocked below, so pin the one name the agent needs.
  echo "$egress_ip $egress_host" >>/etc/hosts

  iptables -F OUTPUT
  iptables -A OUTPUT -o lo -j ACCEPT
  iptables -A OUTPUT -p tcp -d "$egress_ip" --dport "$egress_port" -j ACCEPT
  iptables -P OUTPUT DROP
  if [ -e /proc/net/if_inet6 ]; then
    ip6tables -F OUTPUT
    ip6tables -A OUTPUT -o lo -j ACCEPT
    ip6tables -P OUTPUT DROP
  fi
  echo "entrypoint.sh: network locked to $egress_host ($egress_ip):$egress_port"
fi

# container creates missing parents of mount points (e.g. ~/.pi/agent under
# --sessions alone) as root. find -xdev still lists the mount points, which
# can't be chowned, so only paths on the home's own filesystem are changed.
home_dev=$(stat -c %d /home/agent)
find /home/agent -xdev -user root | while read -r path; do
  if [ "$(stat -c %d "$path")" = "$home_dev" ]; then
    chown agent:agent "$path"
  fi
done
export HOME=/home/agent USER=agent LOGNAME=agent

# Note:: This is a part of a bootstrapping process for Go projects
# the agent would need to build to operate.
# This doesn't really fit here as its likely a conditional on the
# folder being mounted and what the intent is.
if [ -f /workspace/go.mod ]; then
  echo "entrypoint.sh: detected Go project in /workspace, downloading module dependencies..."
  # shellcheck disable=SC2086 # $as_agent is a command prefix to split
  if ! (cd /workspace && $as_agent go mod download); then
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
# shellcheck disable=SC2086 # $as_agent is a command prefix to split
exec $as_agent "$start" "$@"
