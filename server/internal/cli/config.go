package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// clientConfig is the shape of the client config file written by
// `gdhcp login` and read by every API-client subcommand as the lowest-
// precedence source of --server/--token (root.go's resolveClient documents
// the full precedence order).
type clientConfig struct {
	Server string `yaml:"server"`
	Token  string `yaml:"token"`
}

// defaultClientConfigPath returns "~/.gdhcp.yaml", the default --config
// path (docs/AMENDMENTS.md unit brief: "written on the END USER's machine
// at runtime by 'gdhcp login'"). It falls back to "./.gdhcp.yaml" if the
// user's home directory can't be determined (e.g. no $HOME set).
func defaultClientConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ".gdhcp.yaml"
	}
	return filepath.Join(home, ".gdhcp.yaml")
}

// loadClientConfig reads the client config file at path. A missing file is
// not an error — it just means no persisted server/token yet, which is
// normal before the first `gdhcp login`.
func loadClientConfig(path string) (*clientConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &clientConfig{}, nil
		}
		return nil, fmt.Errorf("cli: read config %s: %w", path, err)
	}
	var cfg clientConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("cli: parse config %s: %w", path, err)
	}
	return &cfg, nil
}

// saveClientConfig writes cfg to path as 0600 YAML (it holds a bearer
// token), creating the parent directory if needed.
func saveClientConfig(path string, cfg *clientConfig) error {
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return fmt.Errorf("cli: create config dir %s: %w", dir, err)
		}
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("cli: encode config: %w", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("cli: write config %s: %w", path, err)
	}
	return nil
}
