package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"strings"
)

const (
	hostAlias = "host.container.internal"
	// hostAliasIP is the --localhost address hostAlias is registered with.
	// Host-only networks have no working DNS, so the firewall can't resolve
	// hostAlias and is given this address instead.
	hostAliasIP = "203.0.113.113"
	// `container system dns create` writes each domain, with its --localhost
	// address, to this file; `dns list` prints only the domain.
	dnsResolverPrefix = "/etc/resolver/containerization."
	// Derived from version (main.go) so bumping the release means editing one
	// place; the Taskfile's IMAGE_REF still needs to be kept in sync (its
	// comment tracks this).
	imageRef = "isolate-agents:" + version
)

// container describes one isolate run: what a containerRuntime checks, then
// runs.
type container struct {
	image string
	// network is "" for the runtime's default network.
	network string
	// hostAlias is the name the container reaches the host by, registered to
	// resolve to hostAliasIP.
	hostAlias   string
	hostAliasIP string
	caps        []string
	env         []string // KEY=VALUE
	envFiles    []string
	mounts      []mount
	memory      string // "" for no limit
	tty         bool
	command     []string // arguments to the image's entrypoint
}

// mount binds the host path host into the container at target.
type mount struct {
	host, target string
}

// containerRuntime runs containers on some container system.
type containerRuntime interface {
	// Check returns an actionable error if c can't run here.
	Check(c container) error
	// Run runs c on isolate's stdio until it exits.
	Run(c container) error
}

// appleRuntime is the containerRuntime for Apple's container CLI.
type appleRuntime struct {
	bin            string
	resolverPrefix string
}

func newAppleRuntime(bin string) appleRuntime {
	return appleRuntime{bin: bin, resolverPrefix: dnsResolverPrefix}
}

func (r appleRuntime) Check(c container) error {
	if err := r.checkImage(c.image); err != nil {
		return err
	}
	if c.hostAlias != "" {
		if err := r.checkHostAlias(c.hostAlias, c.hostAliasIP); err != nil {
			return err
		}
	}
	if c.network != "" {
		return r.checkNetwork(c.network)
	}
	return nil
}

func (r appleRuntime) Run(c container) error {
	//nolint:gosec // bin is resolved via exec.LookPath; running it is this tool's entire purpose
	cmd := exec.Command(r.bin, r.runArgs(c)...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// runArgs returns the `container run` arguments for c.
func (r appleRuntime) runArgs(c container) []string {
	args := []string{"run", "--rm"}
	// -t demands a terminal attached to stdin; scripts and CI don't have one.
	if c.tty {
		args = append(args, "-it")
	} else {
		args = append(args, "-i")
	}
	for _, m := range c.mounts {
		args = append(args, "-v", m.host+":"+m.target)
	}
	if c.network != "" {
		args = append(args, "--network", c.network)
	}
	for _, cp := range c.caps {
		args = append(args, "--cap-add", cp)
	}
	for _, e := range c.env {
		args = append(args, "--env", e)
	}
	for _, f := range c.envFiles {
		args = append(args, "--env-file", f)
	}
	if c.memory != "" {
		args = append(args, "--memory", c.memory)
	}
	args = append(args, c.image)
	return append(args, c.command...)
}

func (r appleRuntime) checkImage(ref string) error {
	out, err := exec.Command(r.bin, "image", "list", "-q").Output()
	if err != nil {
		return fmt.Errorf("container image list: %w", err)
	}
	if imageListContains(string(out), ref) {
		return nil
	}
	return fmt.Errorf("image %q not found; build it first with: go tool task images:build", ref)
}

func (r appleRuntime) checkHostAlias(alias, ip string) error {
	resolver, err := os.ReadFile(r.resolverPrefix + alias)
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf(
			"DNS domain %q is not registered with 'container'.\n"+
				"This is needed so containers can reach the host. Run once, and again after\n"+
				"any reboot since the packet filter rule doesn't survive a restart:\n\n"+
				"    go tool task system:dns    (from the isolate repo)\n"+
				"    sudo container system dns create %s --localhost %s",
			alias, alias, ip,
		)
	}
	if err != nil {
		return fmt.Errorf("reading DNS domain %q: %w", alias, err)
	}
	// The firewall allows only ip, so an alias registered with any other
	// address would leave the host unreachable.
	if got := resolverLocalhost(string(resolver)); got != ip {
		return fmt.Errorf(
			"DNS domain %q is registered with --localhost %q, want %q. Re-register it:\n\n"+
				"    sudo container system dns delete %s\n"+
				"    go tool task system:dns    (from the isolate repo)",
			alias, got, ip, alias,
		)
	}
	return nil
}

func (r appleRuntime) checkNetwork(network string) error {
	out, err := exec.Command(r.bin, "network", "list", "-q").Output()
	if err != nil {
		return fmt.Errorf("container network list: %w", err)
	}
	if networkListContains(string(out), network) {
		return nil
	}
	return fmt.Errorf("container network %q not found; create it once with: go tool task system:network", network)
}

// resolverLocalhost returns the address from the "options localhost:<ip>"
// line `container system dns create --localhost` writes, or "" if absent.
func resolverLocalhost(resolver string) string {
	for line := range strings.SplitSeq(resolver, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != "options" {
			continue
		}
		for _, opt := range fields[1:] {
			if ip, ok := strings.CutPrefix(opt, "localhost:"); ok {
				return ip
			}
		}
	}
	return ""
}

// networkListContains reports whether `container network list -q` output
// (one name per line) includes network.
func networkListContains(list, network string) bool {
	for line := range strings.SplitSeq(list, "\n") {
		if strings.TrimSpace(line) == network {
			return true
		}
	}
	return false
}

// imageListContains reports whether image-list output (one ref per line)
// includes ref.
//
// It matches the full "name:tag" reference exactly, falling back to an exact
// bare-name match only for output that omits tags entirely, where there is
// nothing finer to match on. A looser prefix/name-only comparison would let a
// stale isolate-agents:0.0.1 (or any unrelated isolate-agents-* image)
// satisfy a run that needs a different tag, trading the actionable error in
// checkImage for an opaque `container run` failure.
func imageListContains(list, ref string) bool {
	name, _, _ := strings.Cut(ref, ":")
	for line := range strings.SplitSeq(list, "\n") {
		line = strings.TrimSpace(line)
		if line == ref || line == name {
			return true
		}
	}
	return false
}
