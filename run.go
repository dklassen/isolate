package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	iofs "io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// runFlags bundles the `isolate run` CLI.
type runFlags struct {
	fs           *pflag.FlagSet
	profile      *string
	agent        *string
	model        *string
	network      *string
	config       *string
	configFile   *string
	sessions     *string
	firewallLog  *string
	secretsFiles *[]string
	memory       *string
}

var (
	errBadMountPath    = errors.New("must not contain ':' or newlines (container's -v mount syntax would misparse it)")
	errNotDir          = errors.New("is not a directory")
	errUnsupportedFlag = errors.New("is not supported by agent")
	errDirMissing      = errors.New("does not exist; create it first")
	errMissingFolder   = errors.New("missing <folder>")
	errExtraArg        = errors.New("unexpected argument after <folder>")
)

// Flag names shared by newRunFlags and profile.apply.
const (
	flagAgent       = "agent"
	flagModel       = "model"
	flagNetwork     = "network"
	flagConfig      = "config"
	flagConfigFile  = "config-file"
	flagSessions    = "sessions"
	flagFirewallLog = "firewall-log"
	flagSecretsFile = "secrets-file"
	flagMemory      = "memory"
)

func newRunFlags(fs *pflag.FlagSet) *runFlags {
	rf := &runFlags{
		fs:           fs,
		profile:      fs.String("profile", "", "Path to a JSON profile that fills in the other flags (agent, model, network, config, ...); flags given on the command line override it"),
		agent:        fs.String(flagAgent, "pi", "Which agent to run inside the container (pi, claude)"),
		model:        fs.String(flagModel, "", "Model passed to the agent as --model (e.g. ollama/qwen3-coder:30b for pi); empty uses the agent's configured default"),
		network:      fs.String(flagNetwork, networkOllama.String(), "Network access: ollama (only the host's Ollama port), host (host only, no internet), or open (internet and host)"),
		config:       fs.String(flagConfig, "", "Path to an existing host config directory, live-mounted read-write as the agent's config directory (session history, installed extensions, and other runtime writes persist back to this directory)"),
		configFile:   fs.String(flagConfigFile, "", "Path to a single host config file, live-mounted read-write as the agent's top-level config file (only supported by agents that have one, e.g. claude's ~/.claude.json)"),
		sessions:     fs.String(flagSessions, "", "Path to a host directory to live-mount as the agent's session history directory, persisting sessions across runs (only supported by agents with a separate sessions directory, e.g. pi)"),
		firewallLog:  fs.String(flagFirewallLog, "", "Path to an existing host directory; each run creates its own <time>-<agent>-<suffix> subdirectory there, mounted at "+firewallLogDir+", where packets dropped by the firewall are logged as JSON lines (firewall.json); only --network ollama has a firewall, so in other modes it stays empty"),
		memory:       fs.String(flagMemory, "4g", "Memory limit passed to 'container run --memory' (e.g. 4g); set empty to run without a limit"),
		secretsFiles: fs.StringArray(flagSecretsFile, nil, "Path to a host file of key=value secrets (e.g. GH_TOKEN) passed through to the container via 'container run --env-file'. Deliberately not called --env-file: this is for gitignored credential files like assets/env/secrets.env, not a project's ordinary .env. Repeatable. Never baked into the image or written anywhere by isolate itself."),
	}
	return rf
}

func newRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run <folder> [flags] [-- <agent args>...]",
		Short: "Run an agent in a container whose only writable host path is <folder>",
		Args: func(cmd *cobra.Command, args []string) error {
			folder, _ := splitAgentArgs(cmd, args)
			switch {
			case len(folder) < 1:
				return errMissingFolder
			case len(folder) > 1:
				return fmt.Errorf("%w: %q (use '--' to pass arguments through to the agent)", errExtraArg, folder[1])
			}
			return nil
		},
	}
	rf := newRunFlags(cmd.Flags())
	cmd.Run = func(cmd *cobra.Command, args []string) {
		folder, agentArgs := splitAgentArgs(cmd, args)
		runCmd(rf, folder[0], agentArgs)
	}
	return cmd
}

// splitAgentArgs splits a command's positional args at a bare "--"; what
// follows it is forwarded verbatim to the agent.
func splitAgentArgs(cmd *cobra.Command, args []string) (positional, agentArgs []string) {
	if dash := cmd.ArgsLenAtDash(); dash >= 0 {
		return args[:dash], args[dash:]
	}
	return args, nil
}

func runCmd(rf *runFlags, folder string, agentArgs []string) {
	ctx := context.Background()

	if *rf.profile != "" {
		p, err := loadProfile(*rf.profile)
		must(err)
		must(p.apply(rf.fs))
	}
	netMode, err := parseNetworkMode(*rf.network)
	must(err)

	agent, err := lookupAgent(*rf.agent)
	must(err)

	absTarget, err := resolveTarget(folder)
	must(err)

	containerBin, err := exec.LookPath("container")
	if err != nil {
		fatalf("cannot find 'container' in PATH")
	}

	c := container{
		image:       imageRef,
		hostAlias:   hostAlias,
		hostAliasIP: hostAliasIP,
		mounts:      []mount{{absTarget, "/workspace"}},
		memory:      *rf.memory,
		tty:         stdinIsTTY(),
		command:     []string{agent.Name},
	}
	netMode.apply(&c)
	runLogDir, err := addRunMounts(&c, rf, agent)
	must(err)
	if *rf.model != "" {
		c.command = append(c.command, "--model", *rf.model)
	}
	c.command = append(c.command, agentArgs...)

	var runtime containerRuntime = newAppleRuntime(containerBin)
	must(runtime.Check(ctx, c))

	fmt.Println("--- Container Sandbox Active ---")
	fmt.Printf("Isolating to: %s\n", absTarget)
	fmt.Printf("Network: %s\n", netMode)
	if runLogDir != "" {
		fmt.Printf("Firewall log: %s\n", runLogDir)
	}

	runErr := runtime.Run(ctx, c)

	if exitErr, ok := errors.AsType[*exec.ExitError](runErr); ok {
		os.Exit(exitErr.ExitCode())
	}
	must(runErr)
}

// resolveTarget returns the absolute, symlink-resolved form of the <folder>
// to isolate, creating it if needed.
func resolveTarget(path string) (string, error) {
	abs, err := resolveMountPath("<folder>", path)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(abs, 0o750); err != nil {
		return "", fmt.Errorf("<folder>: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolveMountPath("<folder>", resolved)
	}
	return abs, nil
}

// addRunMounts adds the config, sessions, firewall log, and secrets files rf
// asks for to c, returning this run's firewall log directory, if any.
func addRunMounts(c *container, rf *runFlags, agent *Agent) (runLogDir string, err error) {
	mounts, err := buildMounts(agent, *rf.config, *rf.configFile, *rf.sessions)
	if err != nil {
		return "", err
	}
	c.mounts = append(c.mounts, mounts...)
	if *rf.firewallLog != "" {
		abs, err := resolveFirewallLog(*rf.firewallLog)
		if err != nil {
			return "", err
		}
		if runLogDir, err = newRunLogDir(abs, agent.Name, time.Now()); err != nil {
			return "", err
		}
		c.mounts = append(c.mounts, mount{runLogDir, firewallLogDir})
	}
	for _, sf := range *rf.secretsFiles {
		abs, err := resolveSecretsFile(sf)
		if err != nil {
			return "", err
		}
		c.envFiles = append(c.envFiles, abs)
	}
	return runLogDir, nil
}

// buildMounts resolves --config, --config-file, and --sessions into mounts
// for the given agent, validating that each flag's mount point is one the
// agent actually supports.
func buildMounts(agent *Agent, config, configFile, sessions string) ([]mount, error) {
	var mounts []mount
	for _, m := range []struct {
		path  string
		mount func(*Agent, string) (mount, error)
	}{
		{config, configMount},
		{configFile, configFileMount},
		{sessions, sessionsMount},
	} {
		if m.path == "" {
			continue
		}
		mnt, err := m.mount(agent, m.path)
		if err != nil {
			return nil, err
		}
		mounts = append(mounts, mnt)
	}
	return mounts, nil
}

func configMount(agent *Agent, config string) (mount, error) {
	abs, err := resolveMountPath("--"+flagConfig, config)
	if err != nil {
		return mount{}, err
	}
	// Unlike --sessions, this is not created on demand: a config
	// directory is something the user already populated, so a typo'd
	// path should fail loudly rather than start the agent against an
	// empty config and silently lose its history and extensions.
	info, err := os.Stat(abs)
	if err != nil {
		return mount{}, fmt.Errorf("--config: %w", err)
	}
	if !info.IsDir() {
		return mount{}, fmt.Errorf("--config: %s %w", abs, errNotDir)
	}
	return mount{abs, agent.ConfigDir}, nil
}

func configFileMount(agent *Agent, configFile string) (mount, error) {
	if agent.ConfigFile == "" {
		return mount{}, fmt.Errorf("--config-file %w %q", errUnsupportedFlag, agent.Name)
	}
	abs, err := resolveMountPath("--"+flagConfigFile, configFile)
	if err != nil {
		return mount{}, err
	}
	if _, err := os.Stat(abs); err != nil {
		return mount{}, fmt.Errorf("--config-file: %w", err)
	}
	return mount{abs, agent.ConfigFile}, nil
}

func sessionsMount(agent *Agent, sessions string) (mount, error) {
	if agent.SessionsDir == "" {
		return mount{}, fmt.Errorf("--sessions %w %q", errUnsupportedFlag, agent.Name)
	}
	abs, err := resolveMountPath("--"+flagSessions, sessions)
	if err != nil {
		return mount{}, err
	}
	if err := os.MkdirAll(abs, 0o750); err != nil {
		return mount{}, fmt.Errorf("--sessions: %w", err)
	}
	return mount{abs, agent.SessionsDir}, nil
}

// resolveMountPath returns the absolute form of a host path to mount,
// prefixing errors with what the path was given as.
func resolveMountPath(what, path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("%s: %w", what, err)
	}
	if err := checkMountHostPath(abs); err != nil {
		return "", fmt.Errorf("%s: %w", what, err)
	}
	return abs, nil
}

// checkMountHostPath rejects host paths that would corrupt a container
// "-v host:container" mount spec: container's -v syntax has no way to
// represent a ":" or a newline inside the host side, so the spec misparses
// (a ":" path reads as a host prefix with the rest as an option).
func checkMountHostPath(path string) error {
	if strings.ContainsAny(path, ":\n") {
		return fmt.Errorf("path %q %w", path, errBadMountPath)
	}
	return nil
}

// resolveSecretsFile validates that a --secrets-file host path exists and
// returns its absolute form, ready to pass to `container run --env-file`.
func resolveSecretsFile(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("--secrets-file: %w", err)
	}
	if _, err := os.Stat(abs); err != nil {
		return "", fmt.Errorf("--secrets-file: %w", err)
	}
	return abs, nil
}

// resolveFirewallLog validates that a --firewall-log host path is an existing
// directory that can be mounted, and returns its absolute form.
func resolveFirewallLog(path string) (string, error) {
	abs, err := resolveMountPath("--"+flagFirewallLog, path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if errors.Is(err, iofs.ErrNotExist) {
		return "", fmt.Errorf("--firewall-log: directory %s %w", abs, errDirMissing)
	}
	if err != nil {
		return "", fmt.Errorf("--firewall-log: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("--firewall-log: %s %w", abs, errNotDir)
	}
	return abs, nil
}

// newRunLogDir creates this run's own subdirectory of the --firewall-log
// directory, so concurrent runs never share a file and each container sees
// only its own log. Names sort by start time in UTC, like ulogd's
// timestamps; the suffix separates runs started in the same second.
func newRunLogDir(parent, agent string, now time.Time) (string, error) {
	suffix := make([]byte, 2)
	if _, err := rand.Read(suffix); err != nil {
		return "", fmt.Errorf("--firewall-log: %w", err)
	}
	name := now.UTC().Format("20060102T150405Z") + "-" + agent + "-" + hex.EncodeToString(suffix)
	dir := filepath.Join(parent, name)
	if err := os.Mkdir(dir, 0o750); err != nil {
		return "", fmt.Errorf("--firewall-log: %w", err)
	}
	return dir, nil
}

// stdinIsTTY reports whether isolate's standard input is a terminal. `container
// run -t` refuses to run without one, so runCmd only attaches a TTY when there
// really is one, keeping scripted/CI invocations working.
func stdinIsTTY() bool {
	fi, err := os.Stdin.Stat()
	return err == nil && (fi.Mode()&iofs.ModeCharDevice) != 0
}
