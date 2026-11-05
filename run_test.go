package main

import (
	"errors"
	"io"
	iofs "io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type buildMountsCase struct {
	agent      *Agent
	config     string
	configFile string
	sessions   string
	want       []mount // expected mounts when the mount succeeds
	wantErr    error
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
			want: []mount{
				{dir, pi.ConfigDir},
				{filepath.Join(dir, "sessions"), pi.SessionsDir},
			},
		},
	}, {
		name: "pi rejects config-file",
		build: buildMountsCase{
			agent:      pi,
			configFile: existingFile,
			wantErr:    errUnsupportedFlag,
		},
	}, {
		name: "claude rejects sessions",
		build: buildMountsCase{
			agent:    claude,
			sessions: dir,
			wantErr:  errUnsupportedFlag,
		},
	}, {
		name: "claude config and config-file mount",
		build: buildMountsCase{
			agent:      claude,
			config:     dir,
			configFile: existingFile,
			want: []mount{
				{dir, claude.ConfigDir},
				{existingFile, claude.ConfigFile},
			},
		},
	}, {
		name: "claude config-file must exist on host",
		build: buildMountsCase{
			agent:      claude,
			configFile: filepath.Join(dir, "missing.json"),
			wantErr:    iofs.ErrNotExist,
		},
	}, {
		name: "config directory must exist",
		build: buildMountsCase{
			agent:   pi,
			config:  filepath.Join(dir, "typo"),
			wantErr: iofs.ErrNotExist,
		},
	}, {
		name: "config pointing at a file is rejected",
		build: buildMountsCase{
			agent:   pi,
			config:  notADir,
			wantErr: errNotDir,
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
				agent:   pi,
				config:  colonDir,
				wantErr: errBadMountPath,
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
				agent:    pi,
				sessions: colonSes,
				wantErr:  errBadMountPath,
			},
		})
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mounts, err := buildMounts(tt.build.agent, tt.build.config, tt.build.configFile, tt.build.sessions)
			if !errors.Is(err, tt.build.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.build.wantErr)
			}
			if !slices.Equal(mounts, tt.build.want) {
				t.Fatalf("mounts = %+v, want %+v", mounts, tt.build.want)
			}
		})
	}
}

func TestLookupAgent(t *testing.T) {
	for name := range agents {
		t.Run(name, func(t *testing.T) {
			agent, err := lookupAgent(name)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if agent.Name != name {
				t.Fatalf("agent.Name = %q, want %q", agent.Name, name)
			}
		})
	}
}

func TestLookupAgentUnknown(t *testing.T) {
	_, err := lookupAgent("bogus")
	if !errors.Is(err, errUnknownAgent) {
		t.Fatalf("err = %v, want %v", err, errUnknownAgent)
	}
	// The known list must be derived from the registry, not hardcoded: any
	// agent in agents[] has to appear in the failure message for the hint to
	// stay true.
	for name := range agents {
		if !strings.Contains(err.Error(), name) {
			t.Fatalf("lookupAgent(bogus) error %q does not mention registered agent %q", err.Error(), name)
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

func TestResolveFirewallLog(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "firewall.json")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{{
		name:  "existing directory resolves to itself",
		input: dir,
	}, {
		name:    "missing directory errors",
		input:   filepath.Join(dir, "missing"),
		wantErr: true,
	}, {
		name:    "file errors",
		input:   file,
		wantErr: true,
	}, {
		name:    "path with ':' errors",
		input:   dir + ":x",
		wantErr: true,
	}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			abs, err := resolveFirewallLog(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if abs != tt.input {
				t.Fatalf("resolveFirewallLog(%q) = %q, want %q", tt.input, abs, tt.input)
			}
		})
	}
}

func TestNewRunLogDir(t *testing.T) {
	parent := t.TempDir()
	now := time.Date(2026, 11, 3, 9, 15, 2, 0, time.FixedZone("EST", -5*3600))

	first, err := newRunLogDir(parent, "claude", now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := newRunLogDir(parent, "claude", now)
	if err != nil {
		t.Fatal(err)
	}

	if filepath.Dir(first) != parent {
		t.Fatalf("newRunLogDir = %q, want a child of %q", first, parent)
	}
	if want := now.UTC().Format("20060102T150405Z") + "-claude-"; !strings.HasPrefix(filepath.Base(first), want) {
		t.Fatalf("newRunLogDir = %q, want name starting %q", filepath.Base(first), want)
	}
	if info, err := os.Stat(first); err != nil || !info.IsDir() {
		t.Fatalf("newRunLogDir did not create directory %q: %v", first, err)
	}
	if first == second {
		t.Fatalf("two runs in the same second share %q", first)
	}
}

// testRunFlags builds the run flags on a standalone flag set.
func testRunFlags() *runFlags {
	return newRunFlags(pflag.NewFlagSet("run", pflag.ContinueOnError))
}

// executeRun executes `isolate run args`, stopping where it would start the
// container, and returns the folder args, agent args, and parsed flags.
func executeRun(args []string) (positional, agentArgs []string, fs *pflag.FlagSet, err error) {
	cmd := newRunCmd()
	cmd.SetArgs(args)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.Run = func(cmd *cobra.Command, args []string) {
		positional, agentArgs = splitAgentArgs(cmd, args)
	}
	err = cmd.Execute()
	return positional, agentArgs, cmd.Flags(), err
}

func TestRunCmdParsesArgs(t *testing.T) {
	tests := []struct {
		name           string
		args           []string
		wantPositional []string
		wantAgentArgs  []string
	}{{
		name:           "flags after the folder",
		args:           []string{".", "--agent", "claude", "--memory=2g"},
		wantPositional: []string{"."},
	}, {
		name:           "flags before the folder",
		args:           []string{"--agent", "claude", "--memory", "2g", "."},
		wantPositional: []string{"."},
	}, {
		name:           "args after '--' go to the agent",
		args:           []string{".", "--agent", "claude", "--memory=2g", "--", "--resume", "--dangerously-skip-permissions"},
		wantPositional: []string{"."},
		wantAgentArgs:  []string{"--resume", "--dangerously-skip-permissions"},
	}, {
		name:           "bare '--' with nothing after",
		args:           []string{".", "--agent", "claude", "--memory=2g", "--"},
		wantPositional: []string{"."},
	}, {
		name:           "positional named like a flag",
		args:           []string{flagModel, "--agent", "claude", "--memory=2g"},
		wantPositional: []string{flagModel},
	}, {
		name:           "lone '-' is a positional",
		args:           []string{"-", "--agent", "claude", "--memory=2g"},
		wantPositional: []string{"-"},
	}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			positional, agentArgs, fs, err := executeRun(tt.args)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !slices.Equal(positional, tt.wantPositional) {
				t.Fatalf("positional = %v, want %v", positional, tt.wantPositional)
			}
			if !slices.Equal(agentArgs, tt.wantAgentArgs) {
				t.Fatalf("agentArgs = %v, want %v", agentArgs, tt.wantAgentArgs)
			}
			agent, _ := fs.GetString(flagAgent)
			memory, _ := fs.GetString(flagMemory)
			if agent != "claude" || memory != "2g" {
				t.Fatalf("agent, memory = %q, %q, want claude, 2g", agent, memory)
			}
		})
	}
}

func TestRunCmdRepeatedSecretsFile(t *testing.T) {
	_, _, fs, err := executeRun([]string{".", "--secrets-file", "a.env", "--agent", "claude", "--secrets-file", "b.env"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, _ := fs.GetStringArray(flagSecretsFile)
	if want := []string{"a.env", "b.env"}; !slices.Equal(got, want) {
		t.Fatalf("secretsFiles = %v, want %v", got, want)
	}
}

func TestRunCmdHelpDoesNotRun(t *testing.T) {
	for _, args := range [][]string{{"-h"}, {".", "--help"}} {
		positional, _, _, err := executeRun(args)
		if err != nil {
			t.Fatalf("%v: unexpected error: %v", args, err)
		}
		if positional != nil {
			t.Fatalf("%v: ran with positional %v, want help only", args, positional)
		}
	}
}

// An unrecognized or value-less flag, or a folder count other than one, must
// fail loudly: run anyway, it would mount the wrong folder or none.
func TestRunCmdRejectsInvalidArgs(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"no folder", nil},
		{"only agent args", []string{"--", "--resume"}},
		{"two folders", []string{".", "other"}},
		{"unknown long flag", []string{".", "--pi-config", "assets/pi"}},
		{"unknown long flag (= form)", []string{".", "--pi-config=assets/pi"}},
		{"unknown flag before the folder", []string{"--resume", "."}},
		{"unknown short flag", []string{".", "-x"}},
		{"single-dash long flag", []string{".", "-agent", "claude"}},
		{"value flag with no value", []string{".", "--config"}},
		{"bool flag given a value", []string{".", "--help=maybe"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, _, err := executeRun(tt.args); err == nil {
				t.Fatalf("run %v: expected error, got nil", tt.args)
			}
		})
	}
}
