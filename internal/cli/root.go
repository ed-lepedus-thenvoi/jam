package cli

import (
	"io"

	"github.com/spf13/cobra"
)

func newRootCmd(stdin io.Reader, stdout, stderr io.Writer, env Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "jam",
		Short:         "Coordinate Claude Code sessions with remote agents on Band",
		SilenceUsage:  true,
		SilenceErrors: false,
	}
	cmd.SetIn(stdin)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.AddCommand(newInitCmd(stdin, stdout, stderr, env))
	cmd.AddCommand(newWhoamiCmd(stdin, stdout, stderr, env))
	cmd.AddCommand(newAgentCmd(stdin, stdout, stderr, env))
	cmd.AddCommand(newDaemonCmd(stdin, stdout, stderr, env))
	cmd.AddCommand(newOnboardCmd(stdin, stdout, stderr, env))
	return cmd
}
