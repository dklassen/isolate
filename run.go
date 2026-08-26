package main

import (
	"errors"
	"flag"
	"fmt"
	iofs "io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// runFlags bundles the `isolate run` CLI. The flag set is the single source
// of truth for which flags splitFlags accepts: registering a flag here
// automatically makes splitFlags recognize --name, -name, and --name=value
// anywhere in argv, and there is no parallel list to keep in sync.
type runFlags struct {
	fs           *flag.FlagSet
	agent        *string
	config       *string
	configFile   *string
	sessions     *string
	secretsFiles *stringSlice
	memory       *string
	help         *bool
	h            *bool
}

func newRunFlags() *runFlags {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)

	var secretsFiles stringSlice
	rf := &runFlags{
		fs:           fs,
		agent:        fs.String("agent", "pi", "Which agent to run inside the container (pi, claude)"),
		config:       fs.String("config", "", "Path to an existing host config directory, live-mounted read-write as the agent's config directory (session history, installed extensions, and other runtime writes persist back to this directory)"),
		configFile:   fs.String("config-file", "", "Path to a single host config file, live-mounted read-write as the agent's top-level config file (only supported by agents that have one, e.g. claude's ~/.claude.json)"),
		sessions:     fs.String("sessions", "", "Path to a host directory to live-mount as the agent's session history directory, persisting sessions across runs (only supported by agents with a separate sessions directory, e.g. pi)"),
		memory:       fs.String("memory", "4g", "Memory limit passed to 'container run --memory' (e.g. 4g); set empty to run without a limit"),
		secretsFiles: &secretsFiles,
		help:         fs.Bool("help", false, "Show help for 'isolate run'"),
		h:            fs.Bool("h", false, "Alias of --help"),
	}
	fs.Var(&secretsFiles, "secrets-file", "Path to a host file of key=value secrets (e.g. GH_TOKEN) passed through to the container via 'container run --env-file'. Deliberately not called --env-file: this is for gitignored credential files like assets/env/secrets.env, not a project's ordinary .env. Repeatable. Never baked into the image or written anywhere by isolate itself.")
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
func lookupFlag(fs *flag.FlagSet, a string) (takesValue bool, known bool) {
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
	unknown := func(name string) error {
		return fmt.Errorf("unknown flag %q (see 'isolate run --help'; use '--' to pass arguments through to the agent)", name)
	}

	for i := 0; i < len(args); i++ {
		a := args[i]

		if a == "--" {
			agentArgs = append(agentArgs, args[i+1:]...)
			break
		}

		if name, val, ok := strings.Cut(a, "="); ok && isFlag(name) {
			takesValue, known := lookupFlag(fs, name)
			if !known {
				return nil, nil, nil, unknown(name)
			}
			if !takesValue {
				return nil, nil, nil, fmt.Errorf("flag %q does not take a value", name)
			}
			flagArgs = append(flagArgs, name, val)
			continue
		}

		if takesValue, known := lookupFlag(fs, a); known {
			if !takesValue {
				flagArgs = append(flagArgs, a)
				continue
			}
			// args[i+1] == "--" is the agent-args separator, not this
			// flag's value: without this check, a missing value would
			// silently absorb "--" and go on to report every following
			// agent arg as an unknown isolate flag instead of a clear
			// "needs a value" error.
			if i+1 >= len(args) || args[i+1] == "--" {
				return nil, nil, nil, fmt.Errorf("flag %q needs a value", a)
			}
			flagArgs = append(flagArgs, a, args[i+1])
			i++
			continue
		}

		if isFlag(a) {
			return nil, nil, nil, unknown(a)
		}

		positional = append(positional, a)
	}

	return flagArgs, positional, agentArgs, nil
}

// isFlag reports whether an argument looks like a flag rather than a path.
// A lone "-" is conventionally a path (stdin), not a flag.
func isFlag(a string) bool { return strings.HasPrefix(a, "-") && a != "-" }

func runCmd(args []string) {
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

	if len(positional) < 1 {
		fatalf("run: missing <folder>")
	}
	if len(positional) > 1 {
		fatalf("run: unexpected argument %q after <folder> (use '--' to pass arguments through to the agent)", positional[1])
	}

	agent, err := lookupAgent(*rf.agent)
	must(err)

	absTarget, err := filepath.Abs(positional[0])
	must(err)
	must(checkMountHostPath(absTarget))
	must(os.MkdirAll(absTarget, 0o750))
	if resolved, err := filepath.EvalSymlinks(absTarget); err == nil {
		absTarget = resolved
		must(checkMountHostPath(absTarget))
	}

	containerBin, err := exec.LookPath("container")
	if err != nil {
		fatalf("cannot find 'container' in PATH")
	}

	manager := NewContainerManager(appleContainer{bin: containerBin})
	must(manager.Validate())

	fmt.Println("--- Container Sandbox Active ---")
	fmt.Printf("Isolating to: %s\n", absTarget)

	// -t demands a terminal attached to stdin; scripts and CI don't have one,
	// so drop the TTY in that case rather than failing the run (issue: -it was
	// previously unconditional and broke non-interactive invocations).
	runArgs := []string{"run", "--rm"}
	if stdinIsTTY() {
		runArgs = append(runArgs, "-it")
	} else {
		runArgs = append(runArgs, "-i")
	}
	runArgs = append(runArgs, "-v", absTarget+":/workspace")
	if *rf.memory != "" {
		runArgs = append(runArgs, "--memory", *rf.memory)
	}
	mounts, err := buildMounts(agent, *rf.config, *rf.configFile, *rf.sessions)
	must(err)
	runArgs = append(runArgs, mounts...)
	for _, sf := range *rf.secretsFiles {
		abs, err := resolveSecretsFile(sf)
		must(err)
		runArgs = append(runArgs, "--env-file", abs)
	}
	runArgs = append(runArgs, imageRef, agent.Name)
	runArgs = append(runArgs, agentArgs...)

	//nolint:gosec // containerBin is resolved via exec.LookPath; running it is this tool's entire purpose
	cmd := exec.Command(containerBin, runArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	runErr := cmd.Run()

	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		os.Exit(exitErr.ExitCode())
	}
	must(runErr)
}

// buildMounts resolves --config, --config-file, and --sessions into
// "-v host:container" mount arguments for the given agent, validating that
// each flag's mount point is one the agent actually supports.
func buildMounts(agent *Agent, config, configFile, sessions string) ([]string, error) {
	var mounts []string
	if config != "" {
		abs, err := filepath.Abs(config)
		if err != nil {
			return nil, err
		}
		if err := checkMountHostPath(abs); err != nil {
			return nil, fmt.Errorf("--config: %v", err)
		}
		// Unlike --sessions, this is not created on demand: a config
		// directory is something the user already populated, so a typo'd
		// path should fail loudly rather than start the agent against an
		// empty config and silently lose its history and extensions.
		info, err := os.Stat(abs)
		if err != nil {
			return nil, fmt.Errorf("--config: %w", err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("--config: %s is not a directory", abs)
		}
		mounts = append(mounts, "-v", abs+":"+agent.ConfigDir)
	}
	if configFile != "" {
		if agent.ConfigFile == "" {
			return nil, fmt.Errorf("--config-file is not supported by agent %q", agent.Name)
		}
		abs, err := filepath.Abs(configFile)
		if err != nil {
			return nil, err
		}
		if err := checkMountHostPath(abs); err != nil {
			return nil, fmt.Errorf("--config-file: %v", err)
		}
		if _, err := os.Stat(abs); err != nil {
			return nil, fmt.Errorf("--config-file: %w", err)
		}
		mounts = append(mounts, "-v", abs+":"+agent.ConfigFile)
	}
	if sessions != "" {
		if agent.SessionsDir == "" {
			return nil, fmt.Errorf("--sessions is not supported by agent %q", agent.Name)
		}
		abs, err := filepath.Abs(sessions)
		if err != nil {
			return nil, err
		}
		if err := checkMountHostPath(abs); err != nil {
			return nil, fmt.Errorf("--sessions: %v", err)
		}
		if err := os.MkdirAll(abs, 0o750); err != nil {
			return nil, err
		}
		mounts = append(mounts, "-v", abs+":"+agent.SessionsDir)
	}
	return mounts, nil
}

// checkMountHostPath rejects host paths that would corrupt a container
// "-v host:container" mount spec: container's -v syntax has no way to
// represent a ":" or a newline inside the host side, so the spec misparses
// (a ":" path reads as a host prefix with the rest as an option).
func checkMountHostPath(path string) error {
	if strings.ContainsAny(path, ":\n") {
		return fmt.Errorf("path %q must not contain ':' or newlines (container's -v mount syntax would misparse it)", path)
	}
	return nil
}

// resolveSecretsFile validates that a --secrets-file host path exists and
// returns its absolute form, ready to pass to `container run --env-file`.
func resolveSecretsFile(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(abs); err != nil {
		return "", fmt.Errorf("--secrets-file: %w", err)
	}
	return abs, nil
}

// stdinIsTTY reports whether isolate's standard input is a terminal. `container
// run -t` refuses to run without one, so runCmd only attaches a TTY when there
// really is one, keeping scripted/CI invocations working.
func stdinIsTTY() bool {
	fi, err := os.Stdin.Stat()
	return err == nil && (fi.Mode()&iofs.ModeCharDevice) != 0
}
