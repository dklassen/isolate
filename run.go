package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	iofs "io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// runFlags bundles the `isolate run` CLI. The flag set is the single source
// of truth for which flags splitFlags accepts: registering a flag here
// automatically makes splitFlags recognize --name, -name, and --name=value
// anywhere in argv, and there is no parallel list to keep in sync.
type runFlags struct {
	fs           *flag.FlagSet
	profile      *string
	agent        *string
	model        *string
	network      *string
	config       *string
	configFile   *string
	sessions     *string
	firewallLog  *string
	secretsFiles *stringSlice
	memory       *string
	help         *bool
	h            *bool
}

var (
	errUnknownFlag      = errors.New("unknown flag")
	errFlagTakesNoValue = errors.New("does not take a value")
	errFlagNeedsValue   = errors.New("needs a value")
	errBadMountPath     = errors.New("must not contain ':' or newlines (container's -v mount syntax would misparse it)")
	errNotDir           = errors.New("is not a directory")
	errUnsupportedFlag  = errors.New("is not supported by agent")
	errDirMissing       = errors.New("does not exist; create it first")
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

func newRunFlags() *runFlags {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)

	var secretsFiles stringSlice
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
		secretsFiles: &secretsFiles,
		help:         fs.Bool("help", false, "Show help for 'isolate run'"),
		h:            fs.Bool("h", false, "Alias of --help"),
	}
	fs.Var(&secretsFiles, flagSecretsFile, "Path to a host file of key=value secrets (e.g. GH_TOKEN) passed through to the container via 'container run --env-file'. Deliberately not called --env-file: this is for gitignored credential files like assets/env/secrets.env, not a project's ordinary .env. Repeatable. Never baked into the image or written anywhere by isolate itself.")
	return rf
}

// stringSlice accumulates repeated occurrences of a flag, since Go's flag
// package has no repeatable string flag built in.
type stringSlice []string

func (s *stringSlice) String() string { return strings.Join(*s, ",") }

func (s *stringSlice) Set(v string) error {
	*s = append(*s, v)
	return nil
}

// lookupFlag reports whether a (--name, -name, or --name=value) names a flag
// registered on fs, and whether it takes a value argument. A flag that is not
// registered is not known: its recognition and arity both come from the flag
// set, so there is exactly one place to add a new flag.
func lookupFlag(fs *flag.FlagSet, a string) (takesValue, known bool) {
	f := fs.Lookup(strings.TrimLeft(a, "-"))
	if f == nil {
		return false, false
	}
	// flag.Value carries an optional IsBoolFlag marker distinguishing flags
	// that take no value from the rest.
	if b, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && b.IsBoolFlag() {
		return false, true
	}
	return true, true
}

// splitFlags pulls known "--flag value" / "--flag=value" pairs (known =
// registered on fs) out of args, regardless of position, leaving everything
// else as positional arguments.
// Go's flag package only recognizes flags before the first positional
// argument, which breaks natural invocations like `run . --config path`.
//
// A bare "--" ends isolate's own flags: everything after it is returned as
// agentArgs and forwarded verbatim to the agent inside the container.
// Anything else that looks like a flag but isn't recognized is an error
// rather than a silent demotion to a positional -- a typo or a renamed flag
// would otherwise start the agent with nothing mounted and no diagnostic.
func splitFlags(fs *flag.FlagSet, args []string) (flagArgs, positional, agentArgs []string, err error) {
	for i := 0; i < len(args); i++ {
		if args[i] == "--" {
			agentArgs = append(agentArgs, args[i+1:]...)
			break
		}
		pair, consumed, err := parseFlag(fs, args[i:])
		if err != nil {
			return nil, nil, nil, err
		}
		if consumed == 0 {
			positional = append(positional, args[i])
			continue
		}
		flagArgs = append(flagArgs, pair...)
		i += consumed - 1
	}

	return flagArgs, positional, agentArgs, nil
}

// parseFlag reads the flag at args[0], with its value if it takes one,
// returning it as flag-package args and how many of args it consumed; 0
// means args[0] is a positional argument.
func parseFlag(fs *flag.FlagSet, args []string) (flagArgs []string, consumed int, err error) {
	a := args[0]

	if name, val, ok := strings.Cut(a, "="); ok && isFlag(name) {
		takesValue, known := lookupFlag(fs, name)
		switch {
		case !known:
			return nil, 0, unknownFlag(name)
		case !takesValue:
			return nil, 0, fmt.Errorf("flag %q %w", name, errFlagTakesNoValue)
		}
		return []string{name, val}, 1, nil
	}

	takesValue, known := lookupFlag(fs, a)
	switch {
	case known && !takesValue:
		return []string{a}, 1, nil
	case known:
		// args[1] == "--" is the agent-args separator, not this flag's
		// value: without this check, a missing value would silently absorb
		// "--" and go on to report every following agent arg as an unknown
		// isolate flag instead of a clear "needs a value" error.
		if len(args) < 2 || args[1] == "--" {
			return nil, 0, fmt.Errorf("flag %q %w", a, errFlagNeedsValue)
		}
		return []string{a, args[1]}, 2, nil
	case isFlag(a):
		return nil, 0, unknownFlag(a)
	}
	return nil, 0, nil
}

func unknownFlag(name string) error {
	return fmt.Errorf("%w %q (see 'isolate run --help'; use '--' to pass arguments through to the agent)", errUnknownFlag, name)
}

// isFlag reports whether an argument looks like a flag rather than a path.
// A lone "-" is conventionally a path (stdin), not a flag.
func isFlag(a string) bool { return strings.HasPrefix(a, "-") && a != "-" }

func runCmd(args []string) {
	ctx := context.Background()
	rf := newRunFlags()

	flagArgs, positional, agentArgs, err := splitFlags(rf.fs, args)
	if err != nil {
		fatalf("run: %v", err)
	}
	if err := rf.fs.Parse(flagArgs); err != nil {
		// splitFlags rejects unknown flags and missing values up front, so this
		// only covers residual flag-package quirks; fs has already printed the
		// diagnostic.
		os.Exit(2)
	}
	if *rf.help || *rf.h {
		rf.fs.Usage()
		return
	}
	if *rf.profile != "" {
		p, err := loadProfile(*rf.profile)
		must(err)
		must(p.apply(rf.fs))
	}
	netMode, err := parseNetworkMode(*rf.network)
	must(err)

	if len(positional) < 1 {
		fatalf("run: missing <folder>")
	}
	if len(positional) > 1 {
		fatalf("run: unexpected argument %q after <folder> (use '--' to pass arguments through to the agent)", positional[1])
	}

	agent, err := lookupAgent(*rf.agent)
	must(err)

	absTarget, err := resolveTarget(positional[0])
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
