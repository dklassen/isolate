package main

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	// hostOnlyNetwork is created once by `go tool task system:network`.
	hostOnlyNetwork = "isolate-host-only"
	ollamaPort      = 11434
	// firewallLogDir is where entrypoint.sh's ulogd writes the packets the
	// networkOllama firewall drops; rendered into assets/ulogd.conf by
	// `isolate generate`.
	firewallLogDir = "/var/log/isolate"
)

// networkMode is what the container may reach over the network.
type networkMode int

const (
	// networkOpen is container's default network: internet and host.
	networkOpen networkMode = iota
	// networkHost attaches to a host-only network: the host, no internet.
	networkHost
	// networkOllama is networkHost narrowed by an in-container firewall to
	// the one host port Ollama listens on.
	networkOllama
)

var networkModeNames = [...]string{
	networkOpen:   "open",
	networkHost:   "host",
	networkOllama: "ollama",
}

func (m networkMode) String() string { return networkModeNames[m] }

func parseNetworkMode(s string) (networkMode, error) {
	for m, name := range networkModeNames {
		if name == s {
			return networkMode(m), nil
		}
	}
	return 0, fmt.Errorf("unknown network mode %q (known: %s)", s, strings.Join(networkModeNames[:], ", "))
}

// network is the container network m attaches to, or "" for the default.
func (m networkMode) network() string {
	if m == networkOpen {
		return ""
	}
	return hostOnlyNetwork
}

// runArgs returns the `container run` arguments that put the container in
// mode m.
func (m networkMode) runArgs() []string {
	var args []string
	if n := m.network(); n != "" {
		args = append(args, "--network", n)
	}
	if m == networkOllama {
		// entrypoint.sh needs NET_ADMIN to install the firewall, then drops
		// it before starting the agent so the agent can't remove the rules.
		args = append(args,
			"--cap-add", "NET_ADMIN",
			"--env", "ISOLATE_EGRESS="+hostAlias+"="+hostAliasIP+":"+strconv.Itoa(ollamaPort),
		)
	}
	return args
}
