package main

import (
	"errors"
	"strings"
	"testing"
)

// fakeProbe is a containerProbe built out of plain data. It lets the manager
// policy run without executing any process, so these tests verify the
// manager's decisions, not a shell's: no PATH, no #!/bin/sh, no printf/echo
// semantics, no exit codes. The only thing in play is containerManager and
// the policy functions it calls.
type fakeProbe struct {
	images string
	dns    string

	imagesErr error
	dnsErr    error
}

func (f *fakeProbe) imageRefs() (string, error)  { return f.images, f.imagesErr }
func (f *fakeProbe) dnsEntries() (string, error) { return f.dns, f.dnsErr }

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

func TestValidateDNSWithProbe(t *testing.T) {
	t.Run("alias present passes", func(t *testing.T) {
		if err := NewContainerManager(&fakeProbe{dns: "host.container.internal localhost\n"}).validateDNS(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("alias absent fails", func(t *testing.T) {
		if err := NewContainerManager(&fakeProbe{dns: "no domains configured\n"}).validateDNS(); err == nil {
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

func TestValidateShortCircuitsOnImageFailure(t *testing.T) {
	// A probe whose DNS call would fail must not even be consulted when the
	// image check fails first: validates Validate's ordering.
	dnsBoom := errors.New("dns boom")
	m := NewContainerManager(&fakeProbe{
		images:    "nothing\n",
		imagesErr: nil,
		dnsErr:    dnsBoom,
	})
	err := m.Validate()
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
