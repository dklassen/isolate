package main

import (
	"fmt"
	"sort"
	"strings"
)

// Agent describes a coding agent that can run inside the container: its
// exec name (also the dispatch key entrypoint.sh uses to choose a binary)
// and the container-side paths its config mount flags target. A zero value
// for ConfigFile or SessionsDir means the agent has no such mount point, and
// run.go rejects the corresponding flag for that agent.
type Agent struct {
	Name        string
	ConfigDir   string
	ConfigFile  string
	SessionsDir string
}

// agentHome is the home of the unprivileged user entrypoint.sh runs agents as;
// keep in sync with assets/Containerfile.
const agentHome = "/home/agent"

var agents = map[string]*Agent{
	"pi": {
		Name:        "pi",
		ConfigDir:   agentHome + "/.pi",
		SessionsDir: agentHome + "/.pi/agent/sessions",
	},
	"claude": {
		Name:       "claude",
		ConfigDir:  agentHome + "/.claude",
		ConfigFile: agentHome + "/.claude.json",
	},
}

func lookupAgent(name string) (*Agent, error) {
	agent, ok := agents[name]
	if !ok {
		// Derive the known list from the registry so adding an agent can't
		// leave a stale hint behind.
		names := make([]string, 0, len(agents))
		for n := range agents {
			names = append(names, n)
		}
		sort.Strings(names)
		return nil, fmt.Errorf("unknown agent %q (known: %s)", name, strings.Join(names, ", "))
	}
	return agent, nil
}
