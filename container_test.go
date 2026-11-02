package main

import (
	"errors"
	"io/fs"
	"strings"
	"testing"
)

// fakeProbe is a containerProbe built out of plain data. It lets the manager
// policy run without executing any process, so these tests verify the
// manager's decisions, not a shell's: no PATH, no #!/bin/sh, no printf/echo
// semantics, no exit codes. The only thing in play is containerManager and
// the policy functions it calls.
type fakeProbe struct {
	images    string
	resolvers map[string]string // registered domain -> resolver file
	networks  string

	imagesErr   error
	dnsErr      error
	networksErr error
}

func (f *fakeProbe) imageRefs() (string, error) { return f.images, f.imagesErr }

func (f *fakeProbe) dnsResolver(domain string) (string, error) {
	if f.dnsErr != nil {
		return "", f.dnsErr
	}
	r, ok := f.resolvers[domain]
	if !ok {
		return "", fs.ErrNotExist
	}
	return r, nil
}

func (f *fakeProbe) networkNames() (string, error) { return f.networks, f.networksErr }

func TestImageListContains(t *testing.T) {
	// ImageRef's name without the tag; the manager and this test both
	// parameterize against the same constant, so pin the tag logic here
	// against a fixed reference.
	name, _, _ := strings.Cut(imageRef, ":")

	tests := []struct {
		name     string
		list     string
		contains bool
	}{{
		name:     "exact name:tag among other images",
		list:     "foo:latest\n" + imageRef + "\nnode:20\n",
		contains: true,
	}, {
		name:     "bare name line when the list omits tags",
		list:     name + "\n",
		contains: true,
	}, {
		name:     "line is trimmed before matching",
		list:     "   " + imageRef + "  \n",
		contains: true,
	}, {
		name: "stale tag does not satisfy",
		list: name + ":0.0.1\n",
	}, {
		name: "unrelated image with the same tag does not satisfy",
		list: "other-agents:0.1.0\n",
	}, {
		name: "longer repo sharing the name does not satisfy",
		list: name + "-extra\n",
	}, {
		name: "empty list does not satisfy",
		list: "",
	}, {
		name: "whitespace-only list does not satisfy",
		list: "\n\n",
	}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := imageListContains(tt.list, imageRef)
			if got != tt.contains {
				t.Fatalf("imageListContains(%q, %q) = %v, want %v", tt.list, imageRef, got, tt.contains)
			}
		})
	}
}

func TestValidateImageWithProbe(t *testing.T) {
	name, _, _ := strings.Cut(imageRef, ":")

	t.Run("image reported present passes", func(t *testing.T) {
		if err := NewContainerManager(&fakeProbe{images: name + ":0.0.1\n" + imageRef + "\n"}).validateImage(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("only a stale tag reported fails", func(t *testing.T) {
		if err := NewContainerManager(&fakeProbe{images: name + ":0.0.1\n"}).validateImage(); err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("probe failure propagates, unwrapped by policy", func(t *testing.T) {
		boom := errors.New("probe boom")
		err := NewContainerManager(&fakeProbe{imagesErr: boom}).validateImage()
		if !errors.Is(err, boom) {
			t.Fatalf("err = %v, want it to wrap %v", err, boom)
		}
	})
}

// resolverFile is what `container system dns create domain --localhost ip`
// writes, with ip "" for a domain created without --localhost.
func resolverFile(domain, ip string) string {
	options := ""
	if ip != "" {
		options = "options localhost:" + ip
	}
	return "domain " + domain + "\nsearch " + domain + "\nnameserver 127.0.0.1\nport 1053\n" + options
}

func TestValidateDNSWithProbe(t *testing.T) {
	t.Run("alias registered with hostAliasIP passes", func(t *testing.T) {
		p := &fakeProbe{resolvers: map[string]string{hostAlias: resolverFile(hostAlias, hostAliasIP)}}
		if err := NewContainerManager(p).validateDNS(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("alias absent fails", func(t *testing.T) {
		p := &fakeProbe{resolvers: map[string]string{"test": resolverFile("test", "")}}
		if err := NewContainerManager(p).validateDNS(); err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("alias registered with another address fails", func(t *testing.T) {
		p := &fakeProbe{resolvers: map[string]string{hostAlias: resolverFile(hostAlias, "198.51.100.1")}}
		if err := NewContainerManager(p).validateDNS(); err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("alias registered without --localhost fails", func(t *testing.T) {
		p := &fakeProbe{resolvers: map[string]string{hostAlias: resolverFile(hostAlias, "")}}
		if err := NewContainerManager(p).validateDNS(); err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("probe failure propagates, unwrapped by policy", func(t *testing.T) {
		boom := errors.New("probe boom")
		err := NewContainerManager(&fakeProbe{dnsErr: boom}).validateDNS()
		if !errors.Is(err, boom) {
			t.Fatalf("err = %v, want it to wrap %v", err, boom)
		}
	})
}

func TestValidateNetwork(t *testing.T) {
	ready := fakeProbe{
		images:    imageRef + "\n",
		resolvers: map[string]string{hostAlias: resolverFile(hostAlias, hostAliasIP)},
	}

	t.Run("default network needs no lookup", func(t *testing.T) {
		p := ready
		p.networksErr = errors.New("must not be called")
		if err := NewContainerManager(&p).Validate(""); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("network present passes", func(t *testing.T) {
		p := ready
		p.networks = "default\n" + hostOnlyNetwork + "\n"
		if err := NewContainerManager(&p).Validate(hostOnlyNetwork); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("network absent fails", func(t *testing.T) {
		p := ready
		p.networks = "default\n" + hostOnlyNetwork + "-old\n"
		if err := NewContainerManager(&p).Validate(hostOnlyNetwork); err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}

func TestValidateShortCircuitsOnImageFailure(t *testing.T) {
	// A probe whose DNS call would fail must not even be consulted when the
	// image check fails first: validates Validate's ordering.
	dnsBoom := errors.New("dns boom")
	m := NewContainerManager(&fakeProbe{
		images:    "nothing\n",
		imagesErr: nil,
		dnsErr:    dnsBoom,
	})
	err := m.Validate("")
	if errors.Is(err, dnsBoom) {
		t.Fatalf("err = %v, validateImage's failure should short-circuit before validateDNS runs", err)
	}
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// Note: appleContainer, the only exec-based piece, is deliberately absent from
// these tests. Its contract is "ask the real CLI"; unit-testing it against a
// fake would only re-test the fake. It is covered by the real CLI at run
// time (and, if desired later, by an explicitly-labeled integration test).
