package cli

import (
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ed-lepedus-thenvoi/jam/internal/band"
	"github.com/ed-lepedus-thenvoi/jam/internal/inbox"
	"github.com/ed-lepedus-thenvoi/jam/internal/session"
)

// handleRegex matches @<owner>/<name> in message text. owner and name are
// each one or more characters from a permissive identifier set. The leading @
// is consumed but not captured.
var handleRegex = regexp.MustCompile(`@([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)`)

func loadSession(env Env, profile string) (*session.State, error) {
	scope := session.Scope(env.Cwd)
	st, err := session.Load(env.HomeDir, profile, scope)
	if err != nil {
		if errors.Is(err, session.ErrNotFound) {
			return nil, errors.New("no running daemon for this cwd - run 'jam onboard' first")
		}
		return nil, err
	}
	return st, nil
}

// extractHandles returns each unique full handle (owner/name) referenced in
// text via @-mention syntax. Order-preserving and deduped.
func extractHandles(text string) []string {
	matches := handleRegex.FindAllStringSubmatch(text, -1)
	seen := map[string]bool{}
	var out []string
	for _, m := range matches {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	return out
}

func shortNameFromHandle(handle string) string {
	if i := strings.Index(handle, "/"); i >= 0 {
		return handle[i+1:]
	}
	return handle
}

func findPeerByHandle(peers []band.Peer, handle string) *band.Peer {
	for i := range peers {
		if peers[i].Handle == handle {
			return &peers[i]
		}
	}
	return nil
}


func newSendCmd(stdout io.Writer, env Env, getProfile func() string) *cobra.Command {
	return &cobra.Command{
		Use:   "send <chat_id> <message>",
		Short: "Send a chat message; @-mentions in the text are resolved automatically",
		Long: "Parses @owner/handle patterns in the message, resolves them to UUIDs via " +
			"/api/v1/agent/peers, and POSTs to /api/v1/agent/chats/<id>/messages. Band requires " +
			"at least one resolved @-mention or it rejects with 422.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			chatID, content := args[0], args[1]
			profile := getProfile()

			st, err := loadSession(env, profile)
			if err != nil {
				return err
			}
			cfg, err := loadConfigOrHint(env.HomeDir, profile)
			if err != nil {
				return err
			}

			handles := extractHandles(content)
			if len(handles) == 0 {
				return errors.New("message must include at least one @owner/handle mention (Band rejects messages with zero resolved mentions)")
			}

			client := band.New(cfg.BaseURL, st.AgentAPIKey)
			peers, err := client.ListPeers()
			if err != nil {
				return fmt.Errorf("listing peers: %w", err)
			}

			var mentions []band.Mention
			for _, h := range handles {
				// The sender's own handle can never be in their peers list — you
				// aren't your own peer. Leave the @text in content for readability,
				// but skip it for the mentions array.
				if h == st.Handle {
					continue
				}
				peer := findPeerByHandle(peers, h)
				if peer == nil {
					return fmt.Errorf("@%s not found in your peer network (have you joined a chat with them, or are they outside the visible peer page?)", h)
				}
				mentions = append(mentions, band.Mention{
					ID:     peer.ID,
					Name:   shortNameFromHandle(h),
					Handle: h,
				})
			}
			if len(mentions) == 0 {
				return fmt.Errorf("message had only your own handle (@%s) mentioned; Band requires at least one resolvable @-mention to someone else", st.Handle)
			}
			// Text is preserved as the user wrote it. Band's resolver matches
			// `@<handle>` (globally unique) before falling back to `@<name>`,
			// so passing both lets full-handle text substitute cleanly without
			// the platform prepending a duplicate pill.

			msgID, err := client.SendChatMessage(chatID, content, mentions)
			if err != nil {
				return fmt.Errorf("sending message: %w", err)
			}
			fmt.Fprintf(stdout, "Sent %s to chat %s (mentioned %d)\n", msgID, chatID, len(mentions))
			return nil
		},
	}
}

func newInboxCmd(stdout io.Writer, env Env, getProfile func() string) *cobra.Command {
	return &cobra.Command{
		Use:   "inbox",
		Short: "List pending inbound messages in this session's team inbox",
		RunE: func(cmd *cobra.Command, args []string) error {
			profile := getProfile()
			st, err := loadSession(env, profile)
			if err != nil {
				return err
			}
			ns, err := inbox.Read(env.HomeDir, st.TeamName, st.TeammateName)
			if err != nil {
				if errors.Is(err, inbox.ErrNoTeamConfigured) {
					return errors.New("no team integration; re-run 'jam onboard --team <name>' to wire up the inbox")
				}
				return fmt.Errorf("reading inbox: %w", err)
			}
			if len(ns) == 0 {
				fmt.Fprintln(stdout, "(inbox empty)")
				return nil
			}
			for _, n := range ns {
				if n.Band == nil {
					continue
				}
				fmt.Fprintf(stdout, "%s  %s (%s)  %s\n", n.Band.MessageID, n.Band.SenderName, n.Band.SenderType, n.Band.Content)
			}
			return nil
		},
	}
}

func newAckCmd(stdout io.Writer, env Env, getProfile func() string) *cobra.Command {
	return &cobra.Command{
		Use:   "ack <message_id>",
		Short: "Mark an inbound message processed without replying",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			msgID := args[0]
			profile := getProfile()
			st, err := loadSession(env, profile)
			if err != nil {
				return err
			}
			cfg, err := loadConfigOrHint(env.HomeDir, profile)
			if err != nil {
				return err
			}
			ns, err := inbox.Read(env.HomeDir, st.TeamName, st.TeammateName)
			if err != nil {
				return fmt.Errorf("reading inbox: %w", err)
			}
			n := inbox.FindByMessageID(ns, msgID)
			if n == nil || n.Band == nil {
				return fmt.Errorf("message %s not found in inbox", msgID)
			}
			client := band.New(cfg.BaseURL, st.AgentAPIKey)
			if err := client.MarkProcessed(n.Band.ChatID, n.Band.MessageID); err != nil {
				return fmt.Errorf("marking processed: %w", err)
			}
			fmt.Fprintf(stdout, "Marked %s processed (chat %s)\n", msgID, n.Band.ChatID)
			return nil
		},
	}
}

func newReplyCmd(stdout, stderr io.Writer, env Env, getProfile func() string) *cobra.Command {
	return &cobra.Command{
		Use:   "reply <message_id> <text>",
		Short: "Reply to an inbound message (auto-mentions sender, auto-marks processed)",
		Long: "Looks up <message_id> in this session's inbox, sends a reply to the same chat " +
			"with the sender auto-mentioned at the start of the text, and marks the inbound " +
			"processed. Works regardless of whether the message has already been processed — " +
			"Band's mark-processed call is idempotent, so it's safe to reply later as long as " +
			"the message ID is still in the inbox file. If you want to send to a different " +
			"chat or mention different agents, use `jam send` instead.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			msgID, replyText := args[0], args[1]
			profile := getProfile()
			st, err := loadSession(env, profile)
			if err != nil {
				return err
			}
			cfg, err := loadConfigOrHint(env.HomeDir, profile)
			if err != nil {
				return err
			}
			ns, err := inbox.Read(env.HomeDir, st.TeamName, st.TeammateName)
			if err != nil {
				return fmt.Errorf("reading inbox: %w", err)
			}
			n := inbox.FindByMessageID(ns, msgID)
			if n == nil || n.Band == nil {
				return fmt.Errorf("message %s not found in inbox", msgID)
			}

			client := band.New(cfg.BaseURL, st.AgentAPIKey)
			content := "@" + n.Band.SenderName + " " + replyText
			mentions := []band.Mention{{ID: n.Band.SenderID, Name: n.Band.SenderName}}
			newID, err := client.SendChatMessage(n.Band.ChatID, content, mentions)
			if err != nil {
				return fmt.Errorf("sending reply: %w", err)
			}
			if err := client.MarkProcessed(n.Band.ChatID, n.Band.MessageID); err != nil {
				fmt.Fprintf(stderr, "warning: reply sent (%s) but mark-processed failed: %v\n", newID, err)
				return nil
			}
			fmt.Fprintf(stdout, "Replied to %s with %s; marked processed\n", msgID, newID)
			return nil
		},
	}
}
