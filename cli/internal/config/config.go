// Package config stores idpctl's local, per-user state: which control plane
// it talks to, and the session token from the last login. Never a cloud
// credential — idpctl is a client of the control plane API, nothing more.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Config is idpctl's local state.
type Config struct {
	Server string `json:"server"`
	Token  string `json:"token"`
}

// path returns where the config file lives, creating its directory if
// necessary.
func path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("config: finding user config dir: %w", err)
	}
	dir = filepath.Join(dir, "idpctl")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("config: creating config dir: %w", err)
	}
	return filepath.Join(dir, "credentials.json"), nil
}

// Load reads the stored Config. Returns a zero Config, not an error, if
// nothing has been saved yet.
func Load() (Config, error) {
	p, err := path()
	if err != nil {
		return Config{}, err
	}

	b, err := os.ReadFile(p) // #nosec G304 -- path is derived from os.UserConfigDir, not user input
	if os.IsNotExist(err) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("config: reading %s: %w", p, err)
	}

	var cfg Config
	if err := json.Unmarshal(b, &cfg); err != nil {
		return Config{}, fmt.Errorf("config: parsing %s: %w", p, err)
	}
	return cfg, nil
}

// Save persists cfg, replacing whatever was stored before. Written with 0600
// permissions — it holds a live session token.
func Save(cfg Config) error {
	p, err := path()
	if err != nil {
		return err
	}

	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("config: marshalling: %w", err)
	}
	if err := os.WriteFile(p, b, 0o600); err != nil {
		return fmt.Errorf("config: writing %s: %w", p, err)
	}
	return nil
}
