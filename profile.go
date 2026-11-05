package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/pflag"
)

// profile is a saved `isolate run` setup, loaded with --profile. Each field
// fills in the flag of the same name; flags given on the command line win.
// Relative paths are relative to the profile file, so a checked-in profile
// works from any directory.
type profile struct {
	Agent        string   `json:"agent"`
	Model        string   `json:"model"`
	Network      string   `json:"network"`
	Config       string   `json:"config"`
	ConfigFile   string   `json:"config-file"`
	Sessions     string   `json:"sessions"`
	FirewallLog  string   `json:"firewall-log"`
	SecretsFiles []string `json:"secrets-file"`
	Memory       string   `json:"memory"`
}

func loadProfile(path string) (*profile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("--profile: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	// A misspelled key would otherwise silently drop a setting, such as the
	// network lockdown.
	dec.DisallowUnknownFields()
	var p profile
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("--profile %s: %w", path, err)
	}

	dir := filepath.Dir(path)
	rel := func(s string) string {
		if s == "" || filepath.IsAbs(s) {
			return s
		}
		return filepath.Join(dir, s)
	}
	p.Config = rel(p.Config)
	p.ConfigFile = rel(p.ConfigFile)
	p.Sessions = rel(p.Sessions)
	p.FirewallLog = rel(p.FirewallLog)
	for i, sf := range p.SecretsFiles {
		p.SecretsFiles[i] = rel(sf)
	}
	return &p, nil
}

// apply sets each of p's non-empty fields on fs, skipping flags already set
// on the command line.
func (p *profile) apply(fs *pflag.FlagSet) error {
	set := map[string]bool{}
	fs.Visit(func(f *pflag.Flag) { set[f.Name] = true })

	values := []struct {
		flag   string
		values []string
	}{
		{flagAgent, []string{p.Agent}},
		{flagModel, []string{p.Model}},
		{flagNetwork, []string{p.Network}},
		{flagConfig, []string{p.Config}},
		{flagConfigFile, []string{p.ConfigFile}},
		{flagSessions, []string{p.Sessions}},
		{flagFirewallLog, []string{p.FirewallLog}},
		{flagSecretsFile, p.SecretsFiles},
		{flagMemory, []string{p.Memory}},
	}
	for _, v := range values {
		if set[v.flag] {
			continue
		}
		for _, val := range v.values {
			if val == "" {
				continue
			}
			if err := fs.Set(v.flag, val); err != nil {
				return fmt.Errorf("--profile: %s: %w", v.flag, err)
			}
		}
	}
	return nil
}
