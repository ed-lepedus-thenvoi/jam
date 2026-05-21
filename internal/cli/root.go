package cli

import (
	"io"

	"github.com/spf13/cobra"

	"github.com/thenvoi/jam/internal/config"
)

func newRootCmd(stdin io.Reader, stdout, stderr io.Writer, env Env) *cobra.Command {
	var profileFlag string
	cmd := &cobra.Command{
		Use:           "jam",
		Short:         "Coordinate Claude Code sessions with remote agents on Band",
		SilenceUsage:  true,
		SilenceErrors: false,
	}
	cmd.SetIn(stdin)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
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
	cmd.AddCommand(newSendCmd(stdout, env, getProfile))
	cmd.AddCommand(newReplyCmd(stdout, stderr, env, getProfile))
	cmd.AddCommand(newInboxCmd(stdout, env, getProfile))
	cmd.AddCommand(newAckCmd(stdout, env, getProfile))
	cmd.AddCommand(newChatCmd(stdout, stderr, env, getProfile))
	return cmd
}
