package main

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type buildMountsCase struct {
	agent      *Agent
	config     string
	configFile string
	sessions   string
	want       []string // expected "-v..." args when the mount succeeds
	wantErrSub string   // expected error substring when the mount must fail
}

func TestBuildMounts(t *testing.T) {
	pi := agents["pi"]
	claude := agents["claude"]

	dir := t.TempDir()
	existingFile := filepath.Join(dir, "claude.json")
	if err := os.WriteFile(existingFile, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	notADir := filepath.Join(dir, "notadir")
	if err := os.WriteFile(notADir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name  string
		build buildMountsCase
	}{{
		name: "pi config and sessions mount",
		build: buildMountsCase{
			agent:    pi,
			config:   dir,
			sessions: filepath.Join(dir, "sessions"),
			want: []string{
				"-v", dir + ":/root/.pi",
				"-v", filepath.Join(dir, "sessions") + ":/root/.pi/agent/sessions",
			},
		},
	}, {
		name: "pi rejects config-file",
		build: buildMountsCase{
			agent:      pi,
			configFile: existingFile,
			wantErrSub: "not supported",
		},
	}, {
		name: "claude rejects sessions",
		build: buildMountsCase{
			agent:      claude,
			sessions:   dir,
			wantErrSub: "not supported",
		},
	}, {
		name: "claude config and config-file mount",
		build: buildMountsCase{
			agent:      claude,
			config:     dir,
			configFile: existingFile,
			want: []string{
				"-v", dir + ":/root/.claude",
				"-v", existingFile + ":/root/.claude.json",
			},
		},
	}, {
		name: "claude config-file must exist on host",
		build: buildMountsCase{
			agent:      claude,
			configFile: filepath.Join(dir, "missing.json"),
			wantErrSub: "no such file",
		},
	}, {
		name: "config directory must exist",
		build: buildMountsCase{
			agent:      pi,
			config:     filepath.Join(dir, "typo"),
			wantErrSub: "no such file",
		},
	}, {
		name: "config pointing at a file is rejected",
		build: buildMountsCase{
			agent:      pi,
			config:     notADir,
			wantErrSub: "not a directory",
		},
	}}

	// A ':' in a host path would be parsed by container's -v host:guest
	// syntax as host prefix + option, silently mounting the wrong thing. Some
	// filesystems (e.g. APFS) disallow ':' in names, so the rows only exist
	// where the fixture can be created.
	if colonDir := filepath.Join(dir, "weird:name"); os.MkdirAll(colonDir, 0o750) == nil {
		tests = append(tests, struct {
			name  string
			build buildMountsCase
		}{
			name: "colon in config path is rejected",
			build: buildMountsCase{
				agent:      pi,
				config:     colonDir,
				wantErrSub: "must not contain",
			},
		})
	}
	if colonSes := filepath.Join(dir, "weird:sessions"); os.MkdirAll(colonSes, 0o750) == nil {
		tests = append(tests, struct {
			name  string
			build buildMountsCase
		}{
			name: "colon in sessions path is rejected",
			build: buildMountsCase{
				agent:      pi,
				sessions:   colonSes,
				wantErrSub: "must not contain",
			},
		})
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mounts, err := buildMounts(tt.build.agent, tt.build.config, tt.build.configFile, tt.build.sessions)
			if tt.build.wantErrSub != "" {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if !strings.Contains(err.Error(), tt.build.wantErrSub) {
					t.Fatalf("err = %v, want containing %q", err, tt.build.wantErrSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(mounts) != len(tt.build.want) {
				t.Fatalf("mounts = %v, want %v", mounts, tt.build.want)
			}
			for i := range tt.build.want {
				if mounts[i] != tt.build.want[i] {
					t.Fatalf("mounts[%d] = %q, want %q", i, mounts[i], tt.build.want[i])
				}
			}
		})
	}
}

func TestLookupAgent(t *testing.T) {
	tests := []struct {
		agent   string
		known   bool
		wantSub string // error substring (implies !known)
	}{{
		agent: "pi",
		known: true,
	}, {
		agent: "claude",
		known: true,
	}, {
		agent:   "bogus",
		known:   false,
		wantSub: "unknown agent",
	}}

	for _, tt := range tests {
		t.Run(tt.agent, func(t *testing.T) {
			agent, err := lookupAgent(tt.agent)
			if !tt.known {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if !strings.Contains(err.Error(), tt.wantSub) {
					t.Fatalf("err = %v, want containing %q", err, tt.wantSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if agent.Name != tt.agent {
				t.Fatalf("agent.Name = %q, want %q", agent.Name, tt.agent)
			}
		})
	}

	// The known list must be derived from the registry, not hardcoded: any
	// agent in agents[] has to appear in the failure message for the hint to
	// stay true.
	if _, err := lookupAgent("bogus"); err == nil {
		t.Fatal("lookupAgent(bogus): expected error, got nil")
	} else {
		for name := range agents {
			if !strings.Contains(err.Error(), name) {
				t.Fatalf("lookupAgent(bogus) error %q does not mention registered agent %q", err.Error(), name)
			}
		}
	}
}

func TestResolveSecretsFile(t *testing.T) {
	dir := t.TempDir()
	secretsFile := filepath.Join(dir, "secrets.env")
	if err := os.WriteFile(secretsFile, []byte("GH_TOKEN=ghp_test\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		input   string
		chdir   bool // chdir into the fixture dir first (relative-input case)
		wantErr bool
		want    string
	}{{
		name:  "existing absolute path resolves to itself",
		input: secretsFile,
		want:  secretsFile,
	}, {
		// Relative input is what exercises filepath.Abs; an absolute input
		// leaves that code path untested.
		name:  "relative path resolves against cwd",
		input: "secrets.env",
		chdir: true,
		want:  filepath.Join(dir, "secrets.env"),
	}, {
		name:    "missing file errors",
		input:   filepath.Join(dir, "missing.env"),
		wantErr: true,
	}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.chdir {
				t.Chdir(dir)
			}
			abs, err := resolveSecretsFile(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if abs != tt.want {
				t.Fatalf("resolveSecretsFile(%q) = %q, want %q", tt.input, abs, tt.want)
			}
		})
	}
}

// testFlagSet builds the production run flag set for tests that only need
// flag recognition (splitFlags derives it from the registered set).
func testFlagSet() *flag.FlagSet { return newRunFlags().fs }

func TestSplitFlagsRepeatedSecretsFile(t *testing.T) {
	rf := newRunFlags()
	flagArgs, positional, agentArgs, err := splitFlags(rf.fs, []string{
		".", "--secrets-file", "a.env", "--agent", "claude", "--secrets-file", "b.env",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantPositional := []string{"."}
	if len(positional) != len(wantPositional) || positional[0] != wantPositional[0] {
		t.Fatalf("positional = %v, want %v", positional, wantPositional)
	}
	if len(agentArgs) != 0 {
		t.Fatalf("agentArgs = %v, want empty", agentArgs)
	}

	if err := rf.fs.Parse(flagArgs); err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}

	if *rf.agent != "claude" {
		t.Fatalf("agent = %q, want claude", *rf.agent)
	}
	want := []string{"a.env", "b.env"}
	if len(*rf.secretsFiles) != len(want) || (*rf.secretsFiles)[0] != want[0] || (*rf.secretsFiles)[1] != want[1] {
		t.Fatalf("secretsFiles = %v, want %v", *rf.secretsFiles, want)
	}
}

// All of these are "must fail loudly, never silently demote" regressions:
// an unrecognized or value-less flag used to land in positional and either
// launch the agent with nothing mounted (--pi-config was renamed to --config)
// or get MkdirAll'd into a literal ./--config directory that was then mounted
// as /workspace. The "--" separator case: "--" is an agent-args boundary,
// not a value --config may swallow.
func TestSplitFlagsRejectsInvalidInput(t *testing.T) {
	fs := testFlagSet()

	tests := []struct {
		name    string
		args    []string
		wantSub string // error substring (presence implies an error is expected)
	}{{
		name:    "unknown long flag",
		args:    []string{".", "--pi-config", "assets/pi"},
		wantSub: `"--pi-config"`,
	}, {
		name:    "unknown long flag (= form)",
		args:    []string{".", "--pi-config=assets/pi"},
		wantSub: `"--pi-config"`,
	}, {
		name:    "unknown flag before the folder",
		args:    []string{"--resume", "."},
		wantSub: `"--resume"`,
	}, {
		name:    "unknown short flag",
		args:    []string{".", "-x"},
		wantSub: `"-x"`,
	}, {
		name:    "value flag at end, no value",
		args:    []string{"run", "--config"},
		wantSub: `"--config" needs a value`,
	}, {
		name:    "value flag after the folder, no value",
		args:    []string{".", "--config"},
		wantSub: `"--config" needs a value`,
	}, {
		name:    "value flag followed by the '--' separator",
		args:    []string{".", "--config", "--", "--resume"},
		wantSub: `"--config" needs a value`,
	}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, _, err := splitFlags(fs, tt.args)
			if err == nil {
				t.Fatalf("splitFlags(%v): expected error, got nil", tt.args)
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Fatalf("err = %v, want containing %q", err, tt.wantSub)
			}
		})
	}
}

func TestSplitFlagsForwardsAgentArgs(t *testing.T) {
	flagArgs, positional, agentArgs, err := splitFlags(testFlagSet(), []string{
		".", "--agent", "claude", "--", "--resume", "--dangerously-skip-permissions",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(positional) != 1 || positional[0] != "." {
		t.Fatalf("positional = %v, want [.]", positional)
	}
	wantFlags := []string{"--agent", "claude"}
	if len(flagArgs) != len(wantFlags) || flagArgs[0] != wantFlags[0] || flagArgs[1] != wantFlags[1] {
		t.Fatalf("flagArgs = %v, want %v", flagArgs, wantFlags)
	}
	wantAgent := []string{"--resume", "--dangerously-skip-permissions"}
	if len(agentArgs) != len(wantAgent) {
		t.Fatalf("agentArgs = %v, want %v", agentArgs, wantAgent)
	}
	for i := range wantAgent {
		if agentArgs[i] != wantAgent[i] {
			t.Fatalf("agentArgs[%d] = %q, want %q", i, agentArgs[i], wantAgent[i])
		}
	}
}

func TestSplitFlagsBareDoubleDashWithNothingAfter(t *testing.T) {
	_, positional, agentArgs, err := splitFlags(testFlagSet(), []string{".", "--"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(positional) != 1 || positional[0] != "." {
		t.Fatalf("positional = %v, want [.]", positional)
	}
	if len(agentArgs) != 0 {
		t.Fatalf("agentArgs = %v, want empty", agentArgs)
	}
}

// Guards the single-source-of-truth design: a flag only needs registering on
// the flag set to be accepted by splitFlags (no parallel knownFlags list).
func TestSplitFlagsRecognizesRegisteredFlags(t *testing.T) {
	fs := testFlagSet()

	t.Run("memory registered with a value (--name=value form)", func(t *testing.T) {
		flagArgs, positional, _, err := splitFlags(fs, []string{".", "--memory=2g"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(positional) != 1 || positional[0] != "." {
			t.Fatalf("positional = %v, want [.]", positional)
		}
		if len(flagArgs) != 2 || flagArgs[0] != "--memory" || flagArgs[1] != "2g" {
			t.Fatalf("flagArgs = %v, want [--memory 2g]", flagArgs)
		}
		rf := newRunFlags()
		if err := rf.fs.Parse([]string{"--memory=2g"}); err != nil {
			t.Fatalf("unexpected parse error: %v", err)
		}
		if *rf.memory != "2g" {
			t.Fatalf("memory = %q, want 2g", *rf.memory)
		}
	})

	t.Run("memory registered with a separate value", func(t *testing.T) {
		if _, _, _, err := splitFlags(fs, []string{".", "--memory", "8g"}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("unregistered flag stays an error even after adding a registered one", func(t *testing.T) {
		if _, _, _, err := splitFlags(fs, []string{".", "--memory", "2g", "--pi-config", "assets/pi"}); err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}
