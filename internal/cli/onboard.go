package cli

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
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

Chats:
  jam chat new --with @h        Create a chat and add a participant in one go
  jam chat list                 List chats you're in (full IDs for copy/paste)
  jam chat add <chat_id> @h     Add a participant to an existing chat

Lifecycle:
  jam daemon status             Show this bridge's status
  jam daemon stop               Tear down (kills the bridge, deregisters the agent)
  jam agent list                List your other Band agents
  jam onboard                   Idempotent: run this any time to re-print this

Multi-session note:
  Every jam command keys state by scope. If you onboarded with --session NAME
  (or JAM_SESSION env), every later jam call from this Claude Code session
  must pass the same --session NAME — including jam reply, jam ack, jam
  inbox, jam send. Without it, jam falls back to a cwd-hash scope and won't
  find your bridge. If you ever hit "no running daemon for scope ...", that's
  the cause; the error will list the available --session names.

Log: %s
`

func newOnboardCmd(stdin io.Reader, stdout, stderr io.Writer, env Env, getProfile, getScope func() string) *cobra.Command {
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
				warnIfTeamNotMember(stderr, env.HomeDir, teamName)
			}
			st, _, err := ensureDaemonRunning(env, getProfile(), getScope(), agentName, teamName, teammateName)
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

// warnIfTeamNotMember surfaces a sharp footgun: jam can create the inbox dir
// and the bridge will write notifications into it, but Claude Code only
// injects <teammate-message> blocks for teams the CC session is actually a
// member of. Membership is established when a CC session runs TeamCreate,
// which writes <team>/config.json. Absence of that file means no CC session
// has joined; notifications will silently go unread.
func warnIfTeamNotMember(stderr io.Writer, homeDir, teamName string) {
	configPath := filepath.Join(homeDir, ".claude", "teams", teamName, "config.json")
	if _, err := os.Stat(configPath); !errors.Is(err, fs.ErrNotExist) {
		return
	}
	fmt.Fprintf(stderr, "warning: team %q has no config.json in ~/.claude/teams/<team>/.\n", teamName)
	fmt.Fprintln(stderr, "  No Claude Code session has joined this team via TeamCreate, so the bridge will")
	fmt.Fprintln(stderr, "  write inbox notifications that won't appear as <teammate-message> blocks in any")
	fmt.Fprintln(stderr, "  CC session. To fix: from a CC session, run TeamCreate(team_name=\""+teamName+"\")")
	fmt.Fprintln(stderr, "  BEFORE jam onboard. Then re-run jam onboard --team "+teamName+".")
}
