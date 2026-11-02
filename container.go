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

// containerProbe is the boundary around everything these checks need from the
// external container CLI: asking what images exist and what DNS aliases are
// registered. Because the policy in containerManager is written against this
// interface rather than against process exec, production and tests differ
// only in what reports them: appleContainer (Apple's container CLI) shells
// out, fakeProbe returns canned data. Neither side depends on the other
// side's mechanism (binary on PATH, shell, exit codes, output quirks).
type containerProbe interface {
	// imageRefs returns the verbatim lines of `container image list -q`
	// (one ref per line; whether tags are included is the CLI's, not ours).
	imageRefs() (string, error)
	// dnsResolver returns the verbatim resolver file `container system dns
	// create` wrote for domain, or an error wrapping fs.ErrNotExist if the
	// domain isn't registered.
	dnsResolver(domain string) (string, error)
	// networkNames returns the verbatim output of `container network list -q`.
	networkNames() (string, error)
}

// appleContainer is the production containerProbe: named for what it
// represents — Apple's container CLI — and the single place in the program
// that knows how to drive it. Its correctness depends on that external tool
// (argument form, exit status, output shape), so it is an integration
// surface, deliberately untested at the unit level: there is no pure
// behavior in it to assert, and a unit-test fake would only re-test the
// fake. Its real behavior is the real CLI at run time.
type appleContainer struct {
	bin string
}

func (a appleContainer) imageRefs() (string, error) {
	out, err := exec.Command(a.bin, "image", "list", "-q").Output()
	return string(out), err
}

func (a appleContainer) dnsResolver(domain string) (string, error) {
	out, err := os.ReadFile(dnsResolverPrefix + domain)
	return string(out), err
}

func (a appleContainer) networkNames() (string, error) {
	out, err := exec.Command(a.bin, "network", "list", "-q").Output()
	return string(out), err
}

// containerManager holds the readiness policy: a host's container
// installation is ready to run against iff the required image exists and the
// host alias is DNS-registered so a container can reach the host. It consumes
// its probe through the containerProbe interface and never sees a concrete
// probe type or a binary path: any implementation of the port (appleContainer
// in production, fakeProbe in tests) is drop-in.
type containerManager struct {
	probe       containerProbe
	imageRef    string
	hostAlias   string
	hostAliasIP string
}

// NewContainerManager builds a manager around probe — any containerProbe,
// e.g. appleContainer{bin: ...} — wiring in the package's canonical imageRef
// and hostAlias to check against.
func NewContainerManager(probe containerProbe) *containerManager {
	return &containerManager{
		probe:       probe,
		imageRef:    imageRef,
		hostAlias:   hostAlias,
		hostAliasIP: hostAliasIP,
	}
}

// Validate executes the full readiness check against m.probe. network is the
// container network the run attaches to, or "" for the default one.
func (m *containerManager) Validate(network string) error {
	if err := m.validateImage(); err != nil {
		return err
	}
	if err := m.validateDNS(); err != nil {
		return err
	}
	if network == "" {
		return nil
	}
	return m.validateNetwork(network)
}

func (m *containerManager) validateNetwork(network string) error {
	list, err := m.probe.networkNames()
	if err != nil {
		return fmt.Errorf("container network list: %w", err)
	}
	for line := range strings.SplitSeq(list, "\n") {
		if strings.TrimSpace(line) == network {
			return nil
		}
	}
	return fmt.Errorf("container network %q not found; create it once with: go tool task system:network", network)
}

func (m *containerManager) validateImage() error {
	list, err := m.probe.imageRefs()
	if err != nil {
		return fmt.Errorf("container image list: %w", err)
	}
	if imageListContains(list, m.imageRef) {
		return nil
	}
	return fmt.Errorf("image %q not found; build it first with: go tool task images:build", m.imageRef)
}

func (m *containerManager) validateDNS() error {
	resolver, err := m.probe.dnsResolver(m.hostAlias)
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf(
			"DNS domain %q is not registered with 'container'.\n"+
				"This is needed so containers can reach the host. Run once, and again after\n"+
				"any reboot since the packet filter rule doesn't survive a restart:\n\n"+
				"    go tool task system:dns    (from the isolate repo)\n"+
				"    sudo container system dns create %s --localhost %s",
			m.hostAlias, m.hostAlias, m.hostAliasIP,
		)
	}
	if err != nil {
		return fmt.Errorf("reading DNS domain %q: %w", m.hostAlias, err)
	}
	// The firewall allows only hostAliasIP, so an alias registered with any
	// other address would leave the host unreachable.
	if got := resolverLocalhost(resolver); got != m.hostAliasIP {
		return fmt.Errorf(
			"DNS domain %q is registered with --localhost %q, want %q. Re-register it:\n\n"+
				"    sudo container system dns delete %s\n"+
				"    go tool task system:dns    (from the isolate repo)",
			m.hostAlias, got, m.hostAliasIP, m.hostAlias,
		)
	}
	return nil
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

// imageListContains reports whether image-list output (one ref per line)
// includes ref. Pure over its input, so it is testable with no process or
// binary involved.
//
// It matches the full "name:tag" reference exactly, falling back to an exact
// bare-name match only for output that omits tags entirely, where there is
// nothing finer to match on. A looser prefix/name-only comparison would let a
// stale isolate-agents:0.0.1 (or any unrelated isolate-agents-* image)
// satisfy a run that needs a different tag, trading the actionable error in
// validateImage for an opaque `container run` failure.
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
