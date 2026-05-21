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
no polling is required.

Lifecycle:
  jam daemon status      Show this bridge's status
  jam daemon stop        Tear down (kills the bridge, deregisters the agent)
  jam agent list         List your other Band agents
  jam onboard            Idempotent: run this again any time to re-print this

Log: %s

Messaging commands (jam send / reply / inbox / ack) ship in the next slice.
Until then, outbound is curl against /api/v1/agent/chats/<chat_id>/messages
with your agent API key. Remember to @-mention the recipient using their full
handle (ed.lepedus/...) and mark every inbound processed or Band stalls the
queue.
`

func newOnboardCmd(stdin io.Reader, stdout, stderr io.Writer, env Env) *cobra.Command {
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
			st, _, err := ensureDaemonRunning(env, agentName, teamName, teammateName)
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
