package main

import (
	"reflect"
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
	rf := testRunFlags()
	if err := rf.fs.Parse(nil); err != nil {
		t.Fatal(err)
	}
	if *rf.network != networkOllama.String() {
		t.Fatalf("default network = %q, want %q", *rf.network, networkOllama)
	}
}

func TestNetworkModeApply(t *testing.T) {
	base := container{hostAlias: hostAlias, hostAliasIP: hostAliasIP}
	egress := "ISOLATE_EGRESS=" + hostAlias + "=" + hostAliasIP + ":" + strconv.Itoa(ollamaPort)

	tests := []struct {
		mode networkMode
		want container
	}{{
		mode: networkOpen,
		want: base,
	}, {
		mode: networkHost,
		want: container{hostAlias: hostAlias, hostAliasIP: hostAliasIP, network: hostOnlyNetwork},
	}, {
		mode: networkOllama,
		want: container{
			hostAlias:   hostAlias,
			hostAliasIP: hostAliasIP,
			network:     hostOnlyNetwork,
			caps:        []string{"NET_ADMIN"},
			env:         []string{egress},
		},
	}}

	for _, tt := range tests {
		t.Run(tt.mode.String(), func(t *testing.T) {
			got := base
			tt.mode.apply(&got)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("apply() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
