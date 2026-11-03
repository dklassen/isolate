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

// apply puts c in mode m.
func (m networkMode) apply(c *container) {
	if m != networkOpen {
		c.network = hostOnlyNetwork
	}
	if m == networkOllama {
		// entrypoint.sh needs NET_ADMIN to install the firewall, then drops
		// it before starting the agent so the agent can't remove the rules.
		c.caps = append(c.caps, "NET_ADMIN")
		c.env = append(c.env, "ISOLATE_EGRESS="+c.hostAlias+"="+c.hostAliasIP+":"+strconv.Itoa(ollamaPort))
	}
}
