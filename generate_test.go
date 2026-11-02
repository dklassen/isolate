package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestUlogdConfIsGenerated(t *testing.T) {
	want, err := renderUlogdConf()
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(ulogdConfPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s is stale; regenerate it with: go generate .", ulogdConfPath)
	}
}

func TestContainerfileCreatesFirewallLogDir(t *testing.T) {
	containerfile, err := os.ReadFile("assets/Containerfile")
	if err != nil {
		t.Fatal(err)
	}
	if want := "install -d -m 700 " + firewallLogDir + "\n"; !strings.Contains(string(containerfile), want) {
		t.Fatalf("assets/Containerfile must create firewallLogDir: missing %q", want)
	}
}
