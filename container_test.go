package main

import (
	"os"
	"slices"
	"strings"
	"testing"
)

func TestImageListContains(t *testing.T) {
	// ImageRef's name without the tag; checkImage and this test both
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

func TestNetworkListContains(t *testing.T) {
	tests := []struct {
		name     string
		list     string
		contains bool
	}{{
		name:     "listed among others",
		list:     "default\n" + hostOnlyNetwork + "\n",
		contains: true,
	}, {
		name:     "line is trimmed before matching",
		list:     "  " + hostOnlyNetwork + " \n",
		contains: true,
	}, {
		name: "longer name sharing the prefix does not satisfy",
		list: "default\n" + hostOnlyNetwork + "-old\n",
	}, {
		name: "empty list does not satisfy",
	}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := networkListContains(tt.list, hostOnlyNetwork); got != tt.contains {
				t.Fatalf("networkListContains(%q, %q) = %v, want %v", tt.list, hostOnlyNetwork, got, tt.contains)
			}
		})
	}
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

func TestResolverLocalhost(t *testing.T) {
	if got := resolverLocalhost(resolverFile(hostAlias, hostAliasIP)); got != hostAliasIP {
		t.Fatalf("resolverLocalhost() = %q, want %q", got, hostAliasIP)
	}
	if got := resolverLocalhost(resolverFile(hostAlias, "")); got != "" {
		t.Fatalf("resolverLocalhost() without --localhost = %q, want \"\"", got)
	}
}

func TestCheckHostAlias(t *testing.T) {
	// registered writes the resolver file `container system dns create`
	// would for hostAlias into a fresh resolver directory.
	registered := func(t *testing.T, ip string) appleRuntime {
		a := appleRuntime{resolverPrefix: t.TempDir() + "/"}
		if err := os.WriteFile(a.resolverPrefix+hostAlias, []byte(resolverFile(hostAlias, ip)), 0o600); err != nil {
			t.Fatal(err)
		}
		return a
	}

	t.Run("registered with the container's address passes", func(t *testing.T) {
		if err := registered(t, hostAliasIP).checkHostAlias(hostAlias, hostAliasIP); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("not registered fails", func(t *testing.T) {
		a := appleRuntime{resolverPrefix: t.TempDir() + "/"}
		if err := a.checkHostAlias(hostAlias, hostAliasIP); err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("registered with another address fails", func(t *testing.T) {
		if err := registered(t, "198.51.100.1").checkHostAlias(hostAlias, hostAliasIP); err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("registered without --localhost fails", func(t *testing.T) {
		if err := registered(t, "").checkHostAlias(hostAlias, hostAliasIP); err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("unreadable resolver fails", func(t *testing.T) {
		a := appleRuntime{resolverPrefix: t.TempDir() + "/"}
		if err := os.Mkdir(a.resolverPrefix+hostAlias, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := a.checkHostAlias(hostAlias, hostAliasIP); err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}

func TestAppleRuntimeRunArgs(t *testing.T) {
	tests := []struct {
		name string
		c    container
		want []string
	}{{
		name: "minimal container without a terminal",
		c:    container{image: imageRef},
		want: []string{"run", "--rm", "-i", imageRef},
	}, {
		name: "every field",
		c: container{
			image:    imageRef,
			network:  hostOnlyNetwork,
			caps:     []string{"NET_ADMIN"},
			env:      []string{"A=1"},
			envFiles: []string{"/secrets.env"},
			mounts:   []mount{{"/src", "/workspace"}, {"/cfg", "/home/agent/.pi"}},
			memory:   "4g",
			tty:      true,
			command:  []string{"pi", "--model", "m"},
		},
		want: []string{
			"run", "--rm", "-it",
			"-v", "/src:/workspace", "-v", "/cfg:/home/agent/.pi",
			"--network", hostOnlyNetwork,
			"--cap-add", "NET_ADMIN",
			"--env", "A=1",
			"--env-file", "/secrets.env",
			"--memory", "4g",
			imageRef, "pi", "--model", "m",
		},
	}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := (appleRuntime{}).runArgs(tt.c); !slices.Equal(got, tt.want) {
				t.Fatalf("runArgs() = %v, want %v", got, tt.want)
			}
		})
	}
}
