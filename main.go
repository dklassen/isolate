// Command isolate runs an AI coding agent in a container whose only writable
// host path is the one folder it is given.
package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// version is the single source of the tool's version; `isolate version`
// prints it and imageRef (container.go) derives its tag from it.
const version = "0.1.0"

func main() {
	root := &cobra.Command{
		Use:           "isolate",
		Short:         "Run an AI coding agent in a container whose only writable host path is the one folder it is given",
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return fmt.Errorf("%w (see '%s --help'; use '--' to pass arguments through to the agent)", err, cmd.CommandPath())
	})
	root.AddCommand(newRunCmd(), newGenerateCmd(), &cobra.Command{
		Use:   "version",
		Short: "Print the isolate version",
		Args:  cobra.NoArgs,
		Run:   func(*cobra.Command, []string) { fmt.Println("isolate " + version) },
	})
	if cmd, err := root.ExecuteC(); err != nil {
		if cmd != root {
			err = fmt.Errorf("%s: %w", cmd.Name(), err)
		}
		fatalf("%v", err)
	}
}
