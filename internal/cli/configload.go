package cli

import (
	"errors"

	"github.com/thenvoi/jam/internal/config"
)

// loadConfigOrHint returns the parsed config, or a user-facing error pointing
// at `jam init` if the config file is missing.
func loadConfigOrHint(homeDir string) (*config.Config, error) {
	cfg, err := config.Load(homeDir)
	if err == nil {
		return cfg, nil
	}
	if errors.Is(err, config.ErrNotFound) {
		return nil, errors.New("no config found - run 'jam init --user-api-key band_u_...' first")
	}
	return nil, err
}
