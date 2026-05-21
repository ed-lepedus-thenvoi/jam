package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/ed-lepedus-thenvoi/jam/internal/band"
)

func newWhoamiCmd(stdin io.Reader, stdout, stderr io.Writer, env Env, getProfile func() string) *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Show the user profile for the configured API key",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfigOrHint(env.HomeDir, getProfile())
			if err != nil {
				return err
			}
			c := band.New(cfg.BaseURL, cfg.UserAPIKey)
			profile, err := c.GetProfile()
			if err != nil {
				return fmt.Errorf("fetching profile: %w", err)
			}
			fmt.Fprintf(stdout, "%s %s <%s>\nrole: %s\nprofile: %s\n",
				profile.FirstName, profile.LastName, profile.Email, profile.Role, getProfile())
			return nil
		},
	}
}
