package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ed-lepedus-thenvoi/jam/internal/band"
	"github.com/ed-lepedus-thenvoi/jam/internal/config"
)

const defaultBaseURL = "https://app.band.ai"

func newInitCmd(stdin io.Reader, stdout, stderr io.Writer, env Env, getProfile func() string) *cobra.Command {
	var baseURL, userKey string
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Verify your Band user API key and save it to a profile config",
		Long: "Verifies the provided user API key against /api/v1/me/profile and, on success, " +
			"writes ~/.config/jam/profiles/<profile>.json with mode 0600. The profile defaults to " +
			"'default' and can be selected with --profile or JAM_PROFILE. Re-running overwrites.",
		RunE: func(cmd *cobra.Command, args []string) error {
			baseURL = strings.TrimRight(baseURL, "/")
			c := band.New(baseURL, userKey)
			profile, err := c.GetProfile()
			if err != nil {
				return fmt.Errorf("verifying API key: %w", err)
			}
			profileName := getProfile()
			if err := config.Save(env.HomeDir, profileName, &config.Config{
				BaseURL:    baseURL,
				UserAPIKey: userKey,
			}); err != nil {
				return fmt.Errorf("saving config: %w", err)
			}
			fmt.Fprintf(stdout, "Authenticated as %s %s <%s>\nProfile '%s' saved to %s\n",
				profile.FirstName, profile.LastName, profile.Email,
				profileName, config.Path(env.HomeDir, profileName))
			return nil
		},
	}
	cmd.Flags().StringVar(&baseURL, "base-url", defaultBaseURL, "Band API base URL")
	cmd.Flags().StringVar(&userKey, "user-api-key", "", "Band user API key (band_u_...)")
	_ = cmd.MarkFlagRequired("user-api-key")
	return cmd
}
