package cli

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ed-lepedus-thenvoi/jam/internal/band"
)

func newChatCmd(stdout, stderr io.Writer, env Env, getProfile func() string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "chat",
		Short: "Create, list, and add participants to Band chats",
	}
	cmd.AddCommand(newChatNewCmd(stdout, stderr, env, getProfile))
	cmd.AddCommand(newChatListCmd(stdout, env, getProfile))
	cmd.AddCommand(newChatAddCmd(stdout, env, getProfile))
	cmd.AddCommand(newChatShowCmd(stdout, env, getProfile))
	return cmd
}

func newChatShowCmd(stdout io.Writer, env Env, getProfile func() string) *cobra.Command {
	return &cobra.Command{
		Use:   "show <chat_id>",
		Short: "List participants in a chat (with full handles)",
		Long: "Useful when you've joined a chat (or someone messaged you) and you want to " +
			"discover full owner/handle for follow-up `jam send` calls. The inbox JSON's " +
			"sender_handle covers the case of who messaged you; this covers everyone else.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			chatID := args[0]
			profile := getProfile()
			st, err := loadSession(env, profile)
			if err != nil {
				return err
			}
			cfg, err := loadConfigOrHint(env.HomeDir, profile)
			if err != nil {
				return err
			}
			parts, err := band.New(cfg.BaseURL, st.AgentAPIKey).ListParticipants(chatID)
			if err != nil {
				return fmt.Errorf("listing participants: %w", err)
			}
			if len(parts) == 0 {
				fmt.Fprintln(stdout, "(no participants)")
				return nil
			}
			for _, p := range parts {
				handle := p.Handle
				if handle == "" {
					handle = "(no handle)"
				}
				fmt.Fprintf(stdout, "%s  %s  %s  %s  %s\n", p.ID, handle, p.Type, p.Role, p.Status)
			}
			return nil
		},
	}
}

func newChatNewCmd(stdout, stderr io.Writer, env Env, getProfile func() string) *cobra.Command {
	var withHandles []string
	cmd := &cobra.Command{
		Use:   "new",
		Short: "Create a new chat room (optionally with initial participants)",
		Long: "Creates a Band chat owned by this session's agent. Each --with handle is " +
			"resolved via /agent/peers and added as a participant. If any handle cannot be " +
			"resolved, the chat is still created but a warning is printed for that handle.",
		RunE: func(cmd *cobra.Command, args []string) error {
			profile := getProfile()
			st, err := loadSession(env, profile)
			if err != nil {
				return err
			}
			cfg, err := loadConfigOrHint(env.HomeDir, profile)
			if err != nil {
				return err
			}
			client := band.New(cfg.BaseURL, st.AgentAPIKey)

			chat, err := client.CreateChat()
			if err != nil {
				return fmt.Errorf("creating chat: %w", err)
			}
			fmt.Fprintf(stdout, "Created chat %s\n", chat.ID)

			if len(withHandles) == 0 {
				return nil
			}
			peers, err := client.ListPeers()
			if err != nil {
				return fmt.Errorf("listing peers to resolve --with: %w", err)
			}
			var added []string
			for _, h := range withHandles {
				h = strings.TrimPrefix(h, "@")
				peer := findPeerByHandle(peers, h)
				if peer == nil {
					fmt.Fprintf(stderr, "warning: @%s not in peer network, not added\n", h)
					continue
				}
				if err := client.AddParticipant(chat.ID, peer.ID, "member"); err != nil {
					fmt.Fprintf(stderr, "warning: failed to add @%s: %v\n", h, err)
					continue
				}
				fmt.Fprintf(stdout, "Added @%s\n", h)
				added = append(added, h)
			}
			// Block until each added handle is visible via /agent/peers
			// before returning — peer-index propagation lag would otherwise
			// make the next `jam send` flap. Best-effort with a short timeout.
			if len(added) > 0 {
				if missing := waitForPeerVisibility(client, added, 3*time.Second); len(missing) > 0 {
					for _, h := range missing {
						fmt.Fprintf(stderr, "warning: @%s added but not yet visible in peers after 3s; `jam send` will retry\n", h)
					}
				}
			}
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&withHandles, "with", nil, "Add this handle (owner/short) as a participant (repeatable)")
	return cmd
}

func newChatListCmd(stdout io.Writer, env Env, getProfile func() string) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List Band chats this session's agent is in",
		RunE: func(cmd *cobra.Command, args []string) error {
			profile := getProfile()
			st, err := loadSession(env, profile)
			if err != nil {
				return err
			}
			cfg, err := loadConfigOrHint(env.HomeDir, profile)
			if err != nil {
				return err
			}
			chats, err := band.New(cfg.BaseURL, st.AgentAPIKey).ListAgentChats()
			if err != nil {
				return fmt.Errorf("listing chats: %w", err)
			}
			if len(chats) == 0 {
				fmt.Fprintln(stdout, "(no chats)")
				return nil
			}
			for _, c := range chats {
				title := c.Title
				if title == "" {
					title = "(untitled)"
				}
				// Full ID — copy-paste target for `jam send`, `jam chat add`.
				fmt.Fprintf(stdout, "%s  %s  updated=%s\n", c.ID, title, c.UpdatedAt)
			}
			return nil
		},
	}
}

func newChatAddCmd(stdout io.Writer, env Env, getProfile func() string) *cobra.Command {
	return &cobra.Command{
		Use:   "add <chat_id> <handle...>",
		Short: "Add participants to a chat by handle",
		Long: "Resolves each owner/short handle via /agent/peers and adds them as members " +
			"of the chat. Errors on the first unknown handle (so a typo doesn't silently " +
			"drop people from the room).",
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			chatID := args[0]
			rawHandles := args[1:]
			profile := getProfile()
			st, err := loadSession(env, profile)
			if err != nil {
				return err
			}
			cfg, err := loadConfigOrHint(env.HomeDir, profile)
			if err != nil {
				return err
			}
			client := band.New(cfg.BaseURL, st.AgentAPIKey)
			peers, err := client.ListPeers()
			if err != nil {
				return fmt.Errorf("listing peers: %w", err)
			}
			for _, h := range rawHandles {
				h = strings.TrimPrefix(h, "@")
				peer := findPeerByHandle(peers, h)
				if peer == nil {
					return fmt.Errorf("@%s not found in your peer network", h)
				}
				if err := client.AddParticipant(chatID, peer.ID, "member"); err != nil {
					return fmt.Errorf("adding @%s: %w", h, err)
				}
				fmt.Fprintf(stdout, "Added @%s to %s\n", h, chatID)
			}
			return nil
		},
	}
}
