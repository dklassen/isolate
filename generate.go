package main

//go:generate go run . generate

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"
	"text/template"

	"github.com/spf13/cobra"
)

// ulogdConfPath is relative to the repo root, where go generate and the
// Taskfile run.
const ulogdConfPath = "assets/ulogd.conf"

//go:embed assets/ulogd.conf.tmpl
var ulogdConfTemplate string

// renderUlogdConf fills ulogd.conf.tmpl with the Go-side log paths, so the
// image and isolate agree on where the firewall log lives.
func renderUlogdConf() ([]byte, error) {
	t, err := template.New("ulogd.conf").Option("missingkey=error").Parse(ulogdConfTemplate)
	if err != nil {
		return nil, fmt.Errorf("parse ulogd.conf template: %w", err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, struct{ LogDir string }{firewallLogDir}); err != nil {
		return nil, fmt.Errorf("render ulogd.conf: %w", err)
	}
	return buf.Bytes(), nil
}

func newGenerateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "generate",
		Short: "Write the image-build files derived from Go constants",
		// A build step, not a user command.
		Hidden: true,
		Args:   cobra.NoArgs,
		Run: func(*cobra.Command, []string) {
			conf, err := renderUlogdConf()
			must(err)
			//nolint:gosec // checked-in config the image copies; must stay world-readable
			must(os.WriteFile(ulogdConfPath, conf, 0o644))
		},
	}
}
