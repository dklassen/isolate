package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func writeProfile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "profile.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestProfileFillsUnsetFlags(t *testing.T) {
	path := writeProfile(t, `{
		"agent": "claude",
		"model": "ollama/qwen3-coder:30b",
		"network": "ollama",
		"config": "config",
		"secrets-file": ["a.env", "/abs/b.env"]
	}`)
	dir := filepath.Dir(path)

	p, err := loadProfile(path)
	if err != nil {
		t.Fatalf("loadProfile: %v", err)
	}
	rf := testRunFlags()
	if err := rf.fs.Parse(nil); err != nil {
		t.Fatal(err)
	}
	if err := p.apply(rf.fs); err != nil {
		t.Fatalf("apply: %v", err)
	}

	if *rf.agent != "claude" {
		t.Fatalf("agent = %q, want claude", *rf.agent)
	}
	if *rf.model != "ollama/qwen3-coder:30b" {
		t.Fatalf("model = %q, want ollama/qwen3-coder:30b", *rf.model)
	}
	if *rf.network != networkOllama.String() {
		t.Fatalf("network = %q, want %q", *rf.network, networkOllama)
	}
	if want := filepath.Join(dir, "config"); *rf.config != want {
		t.Fatalf("config = %q, want %q (relative to the profile)", *rf.config, want)
	}
	if want := []string{filepath.Join(dir, "a.env"), "/abs/b.env"}; !slices.Equal(*rf.secretsFiles, want) {
		t.Fatalf("secretsFiles = %v, want %v", *rf.secretsFiles, want)
	}
	if *rf.memory != "4g" {
		t.Fatalf("memory = %q, want the flag default 4g when the profile omits it", *rf.memory)
	}
}

func TestCommandLineOverridesProfile(t *testing.T) {
	p, err := loadProfile(writeProfile(t, `{"network": "ollama", "secrets-file": ["profile.env"]}`))
	if err != nil {
		t.Fatalf("loadProfile: %v", err)
	}
	rf := testRunFlags()
	if err := rf.fs.Parse([]string{"--network", networkOpen.String(), "--secrets-file", "cli.env"}); err != nil {
		t.Fatal(err)
	}
	if err := p.apply(rf.fs); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if *rf.network != networkOpen.String() {
		t.Fatalf("network = %q, want %q from the command line", *rf.network, networkOpen)
	}
	if want := []string{"cli.env"}; !slices.Equal(*rf.secretsFiles, want) {
		t.Fatalf("secretsFiles = %v, want %v from the command line only", *rf.secretsFiles, want)
	}
}

func TestLoadProfileRejectsUnknownKeys(t *testing.T) {
	const typo = "netwrok" //nolint:misspell // the misspelled key is the test input
	_, err := loadProfile(writeProfile(t, `{"`+typo+`": "ollama"}`))
	if err == nil || !strings.Contains(err.Error(), typo) {
		t.Fatalf("err = %v, want it to name the unknown key", err)
	}
}
