#!/bin/sh
set -eu

# $1 is iptables or ip6tables. The limit stops a retry loop flooding the log.
log_dropped() {
  $log_drops || return 0
  if ! "$1" -A OUTPUT -m limit --limit 10/s --limit-burst 20 -j NFLOG --nflog-group 1 --nflog-prefix "$1-drop"; then
    echo "entrypoint.sh: warning: cannot add the $1 NFLOG rule; dropped packets won't be logged" >&2
  fi
}

# lock_egress host=ip:port allows outbound traffic to that one address only.
# Any failure aborts the run rather than starting unrestricted.
lock_egress() {
  case "$1" in
  ?*=?*:?*) ;;
  *)
    echo "entrypoint.sh: error: ISOLATE_EGRESS=$1 is not host=ip:port; refusing to start without the egress firewall" >&2
    exit 1
    ;;
  esac
  egress_host="${1%%=*}"
  egress_addr="${1#*=}"
  egress_ip="${egress_addr%:*}"
  egress_port="${egress_addr##*:}"

  # Host-only networks have no working DNS, so the IP is passed in rather
  # than resolved, and pinned for the agent.
  echo "$egress_ip $egress_host" >>/etc/hosts

  # Dropped packets go to ulogd (assets/ulogd.conf) rather than the kernel
  # log, so they land in its firewall.json and nowhere else. Logging is
  # best-effort: without it the firewall still holds.
  log_drops=true
  if ! ulogd -d -c /etc/ulogd.conf; then
    echo "entrypoint.sh: warning: ulogd failed to start (see the logfile in /etc/ulogd.conf); dropped packets won't be logged" >&2
    log_drops=false
  fi

  iptables -F OUTPUT
  iptables -A OUTPUT -o lo -j ACCEPT
  iptables -A OUTPUT -p tcp -d "$egress_ip" --dport "$egress_port" -j ACCEPT
  log_dropped iptables
  iptables -P OUTPUT DROP
  if [ -e /proc/net/if_inet6 ]; then
    ip6tables -F OUTPUT
    ip6tables -A OUTPUT -o lo -j ACCEPT
    log_dropped ip6tables
    ip6tables -P OUTPUT DROP
  fi
  echo "entrypoint.sh: network locked to $egress_host ($egress_ip):$egress_port"
}

agent="${1:?usage: entrypoint.sh <agent> [args...]}"
shift

# Only the setup below runs as root. Everything after it runs as the agent
# user with no capabilities and no_new_privs, so it can't change the
# firewall or /etc/hosts, or regain root through a setuid binary.
as_agent="setpriv --reuid=agent --regid=agent --init-groups --inh-caps=-all --bounding-set=-all --no-new-privs --"

# ISOLATE_EGRESS is set by --network ollama (network.go).
if [ -n "${ISOLATE_EGRESS:-}" ]; then
  lock_egress "$ISOLATE_EGRESS"
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
