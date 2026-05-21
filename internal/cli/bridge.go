package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/thenvoi/jam/internal/bridge"
)

// newInternalBridgeCmd is the in-process replacement for the Elixir sockpuppet
// daemon. It's marked Hidden because `jam daemon start` execs jam with this
// subcommand to spawn the bridge; users should not invoke it directly.
//
// All configuration arrives via env vars to match the contract that
// jam/internal/sockpuppet/DefaultSpawner sets up.
func newInternalBridgeCmd(stdout, stderr io.Writer, env Env) *cobra.Command {
	return &cobra.Command{
		Use:    "internal-bridge",
		Short:  "(internal) Run the Band WebSocket bridge in-process",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			getenv := env.Getenv
			if getenv == nil {
				getenv = os.Getenv
			}
			cfg := bridge.Config{
				BaseURL:        getenv("THENVOI_BASE_URL"),
				AgentAPIKey:    getenv("THENVOI_AGENT_API_KEY"),
				TeamName:       getenv("CLAUDE_TEAM_NAME"),
				TeammateName:   getenv("CLAUDE_TEAMMATE_NAME"),
				HomeDir:        env.HomeDir,
				NotifyTemplate: getenv("JAM_NOTIFY_TEMPLATE"),
				Output:         stderr, // logs go to stderr so stdout stays clean
			}
			if cfg.BaseURL == "" || cfg.AgentAPIKey == "" {
				return errors.New("THENVOI_BASE_URL and THENVOI_AGENT_API_KEY env vars are required")
			}

			ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer cancel()
			return bridge.Run(ctx, cfg)
		},
	}
}
