package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/thenvoi/jam/internal/band"
	"github.com/thenvoi/jam/internal/config"
)

func newInitCmd(stdin io.Reader, stdout, stderr io.Writer, env Env) *cobra.Command {
	var baseURL, userKey, sockpuppetDir string
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Verify your Band user API key and save it to config",
		Long: "Verifies the provided user API key against /api/v1/me/profile and, on success, " +
			"writes ~/.config/jam/config.json with mode 0600. Re-running overwrites the existing config.",
		RunE: func(cmd *cobra.Command, args []string) error {
			baseURL = strings.TrimRight(baseURL, "/")
			c := band.New(baseURL, userKey)
			profile, err := c.GetProfile()
			if err != nil {
				return fmt.Errorf("verifying API key: %w", err)
			}
			if err := config.Save(env.HomeDir, &config.Config{
				BaseURL:       baseURL,
				UserAPIKey:    userKey,
				SockpuppetDir: sockpuppetDir,
			}); err != nil {
				return fmt.Errorf("saving config: %w", err)
			}
			fmt.Fprintf(stdout, "Authenticated as %s %s <%s>\nConfig saved to %s\n",
				profile.FirstName, profile.LastName, profile.Email, config.Path(env.HomeDir))
			return nil
		},
	}
	cmd.Flags().StringVar(&baseURL, "base-url", "https://platform.band.ai", "Band API base URL")
	cmd.Flags().StringVar(&userKey, "user-api-key", "", "Band user API key (band_u_...)")
	cmd.Flags().StringVar(&sockpuppetDir, "sockpuppet-dir", "", "Path to the agent-sockpuppet Elixir project (required for `jam daemon`)")
	_ = cmd.MarkFlagRequired("user-api-key")
	return cmd
}
