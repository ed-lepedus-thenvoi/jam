package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type Config struct {
	BaseURL       string `json:"base_url"`
	UserAPIKey    string `json:"user_api_key"`
	SockpuppetDir string `json:"sockpuppet_dir,omitempty"`
}

// Path returns the canonical config file location for the given home dir.
func Path(homeDir string) string {
	return filepath.Join(homeDir, ".config", "jam", "config.json")
}

// Load reads and parses the config file. Returns ErrNotFound if it doesn't exist.
func Load(homeDir string) (*Config, error) {
	data, err := os.ReadFile(Path(homeDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	return &c, nil
}

// Save writes the config atomically with 0600 perms, creating the directory if needed.
func Save(homeDir string, c *Config) error {
	dir := filepath.Join(homeDir, ".config", "jam")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "config.*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, Path(homeDir))
}

var ErrNotFound = errors.New("config not found")
