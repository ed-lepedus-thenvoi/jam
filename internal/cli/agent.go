package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/thenvoi/jam/internal/band"
)

func newAgentCmd(stdin io.Reader, stdout, stderr io.Writer, env Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Manage Band agents owned by you",
	}
	cmd.AddCommand(newAgentListCmd(stdout, env))
	return cmd
}

func newAgentListCmd(stdout io.Writer, env Env) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List Band agents you own",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfigOrHint(env.HomeDir)
			if err != nil {
				return err
			}
			agents, err := band.New(cfg.BaseURL, cfg.UserAPIKey).ListAgents()
			if err != nil {
				return fmt.Errorf("listing agents: %w", err)
			}
			if len(agents) == 0 {
				fmt.Fprintln(stdout, "(no agents)")
				return nil
			}
			for _, a := range agents {
				short := a.ID
				if len(short) >= 8 {
					short = short[:8]
				}
				fmt.Fprintf(stdout, "%s  %s  %s\n", short, a.Name, a.Description)
			}
			return nil
		},
	}
}
