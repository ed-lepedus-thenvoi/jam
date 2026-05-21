package cli

import (
	"errors"
	"fmt"

	"github.com/thenvoi/jam/internal/config"
)

// loadConfigOrHint returns the parsed config for the given profile, or a
// user-facing error pointing at `jam init` if the profile is missing.
func loadConfigOrHint(homeDir, profile string) (*config.Config, error) {
	cfg, err := config.Load(homeDir, profile)
	if err == nil {
		return cfg, nil
	}
	if errors.Is(err, config.ErrNotFound) {
		hint := "no config found - run 'jam init --user-api-key band_u_...' first"
		if profile != "" && profile != config.DefaultProfile {
			hint = fmt.Sprintf("no config for profile '%s' - run 'jam init --profile %s --user-api-key band_u_...' first", profile, profile)
		}
		return nil, errors.New(hint)
	}
	return nil, err
}
