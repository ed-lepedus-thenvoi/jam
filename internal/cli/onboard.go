package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

const orientationTemplate = `You're online as %s (pid %d).

Inbound: messages directed at @%s arrive automatically as <teammate-message>
blocks in your next turn — the sockpuppet writes them to your team inbox, so
no polling is required. Each notification's text tells you the exact jam
command to reply or acknowledge.

Messaging:
  jam reply <msg_id> "text"     Reply to an inbound (auto-mentions sender, auto-acks)
  jam ack <msg_id>              Mark an inbound processed without replying
  jam inbox                     List pending inbound
  jam send <chat_id> "@h text"  Send a fresh message (@-mentions resolved automatically)

Lifecycle:
  jam daemon status             Show this bridge's status
  jam daemon stop               Tear down (kills the bridge, deregisters the agent)
  jam agent list                List your other Band agents
  jam onboard                   Idempotent: run this any time to re-print this

Log: %s
`

func newOnboardCmd(stdin io.Reader, stdout, stderr io.Writer, env Env, getProfile func() string) *cobra.Command {
	var teamName, teammateName, agentName string
	cmd := &cobra.Command{
		Use:   "onboard",
		Short: "One-shot bootstrap: provisions an agent, starts the bridge, prints orientation",
		Long: "Idempotent. Run in a fresh Claude Code session to wire it up as a Band peer. " +
			"If --team is set (with --teammate, default `team-lead`), the team inbox directory " +
			"under ~/.claude/teams/<team>/inboxes/ is created so the sockpuppet can deliver " +
			"<teammate-message> notifications.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if teamName != "" {
				if teammateName == "" {
					teammateName = "team-lead"
				}
				if err := ensureTeamInbox(env.HomeDir, teamName); err != nil {
					return fmt.Errorf("preparing team inbox dir: %w", err)
				}
			}
			st, _, err := ensureDaemonRunning(env, getProfile(), agentName, teamName, teammateName)
			if err != nil {
				return err
			}
			fmt.Fprintf(stdout, orientationTemplate, st.Handle, st.PID, st.Handle, st.LogPath)
			return nil
		},
	}
	cmd.Flags().StringVar(&teamName, "team", "", "Claude Code team name (enables <teammate-message> delivery)")
	cmd.Flags().StringVar(&teammateName, "teammate", "", "Claude Code teammate name within the team (default `team-lead` when --team is set)")
	cmd.Flags().StringVar(&agentName, "name", "", "Override the auto-derived agent name")
	return cmd
}

func ensureTeamInbox(homeDir, teamName string) error {
	dir := filepath.Join(homeDir, ".claude", "teams", teamName, "inboxes")
	return os.MkdirAll(dir, 0o700)
}
