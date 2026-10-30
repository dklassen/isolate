package main

import (
	"slices"
	"strconv"
	"testing"
)

func TestParseNetworkModeRoundTrips(t *testing.T) {
	for m := range networkModeNames {
		mode := networkMode(m)
		got, err := parseNetworkMode(mode.String())
		if err != nil {
			t.Fatalf("parseNetworkMode(%q): %v", mode, err)
		}
		if got != mode {
			t.Fatalf("parseNetworkMode(%q) = %v, want %v", mode, got, mode)
		}
	}
	if _, err := parseNetworkMode("none"); err == nil {
		t.Fatal("parseNetworkMode(none): expected error, got nil")
	}
}

func TestNetworkDefaultsToOllama(t *testing.T) {
	rf := newRunFlags()
	if err := rf.fs.Parse(nil); err != nil {
		t.Fatal(err)
	}
	if *rf.network != networkOllama.String() {
		t.Fatalf("default network = %q, want %q", *rf.network, networkOllama)
	}
}

func TestNetworkModeRunArgs(t *testing.T) {
	egress := "ISOLATE_EGRESS=" + hostAlias + ":" + strconv.Itoa(ollamaPort)

	tests := []struct {
		mode networkMode
		want []string
	}{{
		mode: networkOpen,
	}, {
		mode: networkHost,
		want: []string{"--network", hostOnlyNetwork},
	}, {
		mode: networkOllama,
		want: []string{"--network", hostOnlyNetwork, "--cap-add", "NET_ADMIN", "--env", egress},
	}}

	for _, tt := range tests {
		t.Run(tt.mode.String(), func(t *testing.T) {
			if got := tt.mode.runArgs(); !slices.Equal(got, tt.want) {
				t.Fatalf("runArgs() = %v, want %v", got, tt.want)
			}
		})
	}
}
