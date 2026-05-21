package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// DefaultProfile is the profile name used when none is supplied.
const DefaultProfile = "default"

type Config struct {
	BaseURL       string `json:"base_url"`
	UserAPIKey    string `json:"user_api_key"`
	SockpuppetDir string `json:"sockpuppet_dir,omitempty"`
}

// resolveProfile maps "" to DefaultProfile so callers can pass empty-string
// meaning "no preference."
func resolveProfile(profile string) string {
	if profile == "" {
		return DefaultProfile
	}
	return profile
}

// Path returns the config file location for a given home dir + profile.
func Path(homeDir, profile string) string {
	return filepath.Join(homeDir, ".config", "jam", "profiles", resolveProfile(profile)+".json")
}

func Load(homeDir, profile string) (*Config, error) {
	data, err := os.ReadFile(Path(homeDir, profile))
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

func Save(homeDir, profile string, c *Config) error {
	dir := filepath.Join(homeDir, ".config", "jam", "profiles")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".config.*.tmp")
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
	return os.Rename(tmpName, Path(homeDir, profile))
}

var ErrNotFound = errors.New("config not found")
