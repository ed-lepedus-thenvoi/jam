package cli

import (
	"io"

	"github.com/spf13/cobra"

	"github.com/ed-lepedus-thenvoi/jam/internal/config"
)

func newRootCmd(stdin io.Reader, stdout, stderr io.Writer, env Env) *cobra.Command {
	var profileFlag string
	cmd := &cobra.Command{
		Use:           "jam",
		Short:         "Coordinate Claude Code sessions with remote agents on Band",
		Version:       env.Version,
		SilenceUsage:  true,
		SilenceErrors: false,
	}
	cmd.SetIn(stdin)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	// Default cobra prints "jam version X" — match that for the explicit
	// `version` subcommand below too.
	cmd.SetVersionTemplate("jam {{.Version}}\n")
	cmd.PersistentFlags().StringVar(&profileFlag, "profile", "", "Profile name (overrides JAM_PROFILE; defaults to 'default')")

	getProfile := func() string {
		if profileFlag != "" {
			return profileFlag
		}
		if env.Getenv != nil {
			if p := env.Getenv("JAM_PROFILE"); p != "" {
				return p
			}
		}
		return config.DefaultProfile
	}

	cmd.AddCommand(newInitCmd(stdin, stdout, stderr, env, getProfile))
	cmd.AddCommand(newWhoamiCmd(stdin, stdout, stderr, env, getProfile))
	cmd.AddCommand(newAgentCmd(stdin, stdout, stderr, env, getProfile))
	cmd.AddCommand(newDaemonCmd(stdin, stdout, stderr, env, getProfile))
	cmd.AddCommand(newOnboardCmd(stdin, stdout, stderr, env, getProfile))
	cmd.AddCommand(newSendCmd(stdout, stderr, env, getProfile))
	cmd.AddCommand(newReplyCmd(stdout, stderr, env, getProfile))
	cmd.AddCommand(newInboxCmd(stdout, env, getProfile))
	cmd.AddCommand(newAckCmd(stdout, env, getProfile))
	cmd.AddCommand(newChatCmd(stdout, stderr, env, getProfile))
	cmd.AddCommand(newPluginCmd(stdout, stderr))
	cmd.AddCommand(newInternalBridgeCmd(stdout, stderr, env))
	cmd.AddCommand(newVersionCmd(stdout, env))
	return cmd
}

func newVersionCmd(stdout io.Writer, env Env) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the jam version",
		RunE: func(cmd *cobra.Command, args []string) error {
			v := env.Version
			if v == "" {
				v = "dev"
			}
			_, err := cmd.OutOrStdout().Write([]byte("jam " + v + "\n"))
			return err
		},
	}
}
