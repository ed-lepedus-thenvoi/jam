package cli

import (
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ed-lepedus-thenvoi/jam/internal/band"
	"github.com/ed-lepedus-thenvoi/jam/internal/inbox"
	"github.com/ed-lepedus-thenvoi/jam/internal/session"
)

// handleRegex matches @<owner>/<name> (agent form) or @<slug> (human form)
// in message text. Each segment must START with an alphanumeric to keep
// common prose patterns from triggering spurious resolution attempts —
// e.g. `@-mention` (leading hyphen) and `@_init` (leading underscore) are
// no longer matched. Inside the segment, hyphen/dot/underscore are fine.
// The /<name> segment is optional so single-segment human handles
// (e.g. `@ed.lepedus`) are captured alongside the longer agent form.
// Greedy matching means full agent handles are preferred over their prefix
// when the slash is present.
var handleRegex = regexp.MustCompile(`@([A-Za-z0-9][A-Za-z0-9_.-]*(?:/[A-Za-z0-9][A-Za-z0-9_.-]*)?)`)

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
// text via @-mention syntax. Order-preserving and deduped. Matches whose `@`
// is preceded by a handle-like char (alphanumeric, `_`, `.`, `-`) are skipped
// as mid-word — `band-peer@jam-marketplace` (Claude Code plugin coordinate
// syntax) and similar package-style strings shouldn't be parsed as mentions.
func extractHandles(text string) []string {
	matches := handleRegex.FindAllStringSubmatchIndex(text, -1)
	seen := map[string]bool{}
	var out []string
	for _, m := range matches {
		atPos := m[0] // index of `@`
		if atPos > 0 && isHandleLikeByte(text[atPos-1]) {
			continue
		}
		captured := text[m[2]:m[3]]
		if !seen[captured] {
			seen[captured] = true
			out = append(out, captured)
		}
	}
	return out
}

// isHandleLikeByte reports whether a byte could appear inside a handle.
// Used to detect when `@` is mid-word (preceded by such a byte), in which
// case it's a package/email-style separator rather than a mention sigil.
func isHandleLikeByte(b byte) bool {
	switch {
	case b >= 'A' && b <= 'Z', b >= 'a' && b <= 'z', b >= '0' && b <= '9':
		return true
	}
	return b == '_' || b == '.' || b == '-'
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

// resolveMentions takes a list of full owner/handle strings and returns the
// corresponding Mention entries (id + name + handle). Unresolvable handles
// are SKIPPED with a stderr warning rather than erroring the whole send —
// prose patterns like the literal phrase `@owner/handle` in a docs example
// would otherwise crash a message that also contained legit mentions. The
// sender's own handle is skipped silently (you can't be in your own peers).
// On any miss, retries once after a brief delay to absorb the platform's
// peer-index propagation lag before declaring missing.
func resolveMentions(client *band.Client, selfHandle string, handles []string, stderr io.Writer) ([]band.Mention, error) {
	resolve := func(peers []band.Peer) (mentions []band.Mention, missing []string) {
		for _, h := range handles {
			if h == selfHandle {
				continue
			}
			peer := findPeerByHandle(peers, h)
			if peer == nil {
				missing = append(missing, h)
				continue
			}
			mentions = append(mentions, band.Mention{
				ID: peer.ID, Name: shortNameFromHandle(h), Handle: h,
			})
		}
		return
	}

	peers, err := client.ListPeers()
	if err != nil {
		return nil, fmt.Errorf("listing peers: %w", err)
	}
	mentions, missing := resolve(peers)
	if len(missing) > 0 {
		// Brief retry against a freshened peer list.
		time.Sleep(500 * time.Millisecond)
		peers, err = client.ListPeers()
		if err != nil {
			return nil, fmt.Errorf("listing peers (retry): %w", err)
		}
		mentions, missing = resolve(peers)
		for _, h := range missing {
			fmt.Fprintf(stderr, "warning: @%s not in your peer network — skipping (will appear as literal text in the message)\n", h)
		}
	}
	// Sort longest-handle-first so the platform's String.replace_all pass
	// substitutes full agent handles before their human-handle prefixes. Mixed
	// human + agent mentions on the same owner (e.g. `@ed.lepedus` plus
	// `@ed.lepedus/claude-foo`) and prefix-sharing agent pairs (`@alice/bob`
	// plus `@alice/bob-junior`) both rely on this ordering or the shorter
	// handle's substitution eats characters from the longer one.
	sort.SliceStable(mentions, func(i, j int) bool {
		return len(mentions[i].Handle) > len(mentions[j].Handle)
	})
	return mentions, nil
}

// waitForPeerVisibility polls /agent/peers until every handle in `want` is
// visible, or the timeout fires. Returns the still-missing handles (empty on
// success). Best-effort — surfaces missing handles via the return rather than
// erroring so the caller can warn or proceed.
func waitForPeerVisibility(client *band.Client, want []string, timeout time.Duration) []string {
	deadline := time.Now().Add(timeout)
	missing := append([]string{}, want...)
	for time.Now().Before(deadline) && len(missing) > 0 {
		peers, err := client.ListPeers()
		if err == nil {
			byHandle := make(map[string]bool, len(peers))
			for _, p := range peers {
				byHandle[p.Handle] = true
			}
			next := missing[:0]
			for _, h := range missing {
				if !byHandle[h] {
					next = append(next, h)
				}
			}
			missing = next
		}
		if len(missing) == 0 {
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	return missing
}


func newSendCmd(stdout, stderr io.Writer, env Env, getProfile func() string) *cobra.Command {
	return &cobra.Command{
		Use:   "send <chat_id> <message>",
		Short: "Send a chat message; @-mentions in the text are resolved automatically",
		Long: "Parses @owner/handle patterns in the message, resolves them to UUIDs via " +
			"/api/v1/agent/peers, and POSTs to /api/v1/agent/chats/<id>/messages. Band requires " +
			"at least one resolved @-mention or it rejects with 422. Unresolvable @-text (typos, " +
			"prose examples) emits a stderr warning and is left as literal text in the message.",
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
			mentions, err := resolveMentions(client, st.Handle, handles, stderr)
			if err != nil {
				return err
			}
			if len(mentions) == 0 {
				return fmt.Errorf("no resolvable @-mention to someone other than yourself; Band requires at least one (typos and prose examples skipped automatically — see stderr warnings)")
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

			// Resolve any @-mentions the user typed in replyText (same as jam send).
			handles := extractHandles(replyText)
			mentions, err := resolveMentions(client, st.Handle, handles, stderr)
			if err != nil {
				return err
			}

			// Make sure the sender ends up in the mentions array. Check by id
			// rather than by handle so a user who typed @owner/short for the
			// sender doesn't get them double-prepended.
			senderInMentions := false
			for _, m := range mentions {
				if m.ID == n.Band.SenderID {
					senderInMentions = true
					break
				}
			}

			senderDisplay := n.Band.SenderHandle
			if senderDisplay == "" {
				senderDisplay = n.Band.SenderName
			}

			var content string
			if senderInMentions {
				content = replyText
			} else {
				content = "@" + senderDisplay + " " + replyText
				mentions = append(mentions, band.Mention{
					ID:     n.Band.SenderID,
					Name:   n.Band.SenderName,
					Handle: n.Band.SenderHandle,
				})
			}

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
