package main

import (
	"fmt"
	"os/exec"
	"strings"
)

const (
	hostAlias = "host.container.internal"
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
	// dnsEntries returns the verbatim output of `container system dns list`.
	dnsEntries() (string, error)
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

func (a appleContainer) dnsEntries() (string, error) {
	out, err := exec.Command(a.bin, "system", "dns", "list").Output()
	return string(out), err
}

// containerManager holds the readiness policy: a host's container
// installation is ready to run against iff the required image exists and the
// host alias is DNS-registered so a container can reach the host. It consumes
// its probe through the containerProbe interface and never sees a concrete
// probe type or a binary path: any implementation of the port (appleContainer
// in production, fakeProbe in tests) is drop-in.
type containerManager struct {
	probe     containerProbe
	imageRef  string
	hostAlias string
}

// NewContainerManager builds a manager around probe — any containerProbe,
// e.g. appleContainer{bin: ...} — wiring in the package's canonical imageRef
// and hostAlias to check against.
func NewContainerManager(probe containerProbe) *containerManager {
	return &containerManager{
		probe:     probe,
		imageRef:  imageRef,
		hostAlias: hostAlias,
	}
}

// Validate executes the full readiness check against m.probe.
func (m *containerManager) Validate() error {
	if err := m.validateImage(); err != nil {
		return err
	}
	return m.validateDNS()
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
	list, err := m.probe.dnsEntries()
	if err != nil {
		return fmt.Errorf("container system dns list: %w", err)
	}
	// `container system dns list` has no stable, documented row format worth
	// parsing against, so membership is a substring check; the alias is
	// parameterized so this remains policy over data, not a hardcoded lookup.
	if !strings.Contains(list, m.hostAlias) {
		return fmt.Errorf(
			"DNS domain %q is not registered with 'container'.\n"+
				"This is needed so containers can reach the host. Run once, and again after\n"+
				"any reboot since the packet filter rule doesn't survive a restart:\n\n"+
				"    go tool task system:dns    (from the isolate repo)\n"+
				"    sudo container system dns create %s --localhost 203.0.113.113",
			m.hostAlias, m.hostAlias,
		)
	}
	return nil
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
