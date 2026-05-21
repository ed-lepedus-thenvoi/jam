// Package bridge is jam's in-process replacement for the Elixir
// agent-sockpuppet. It connects to the Band WebSocket as an agent, joins the
// agent_rooms / agent_contacts / chat_room channels, and writes inbox JSON
// notifications when inbound messages arrive — same on-disk schema as the
// sockpuppet so existing consumers (Claude Code teammate-messages,
// jam inbox/reply/ack) work unchanged.
package bridge

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"sync"
	"text/template"
	"time"

	"github.com/nshafer/phx"

	"github.com/thenvoi/jam/internal/band"
)

// Config carries everything the bridge needs to identify itself, talk to Band,
// and optionally deliver Claude Code teammate notifications.
type Config struct {
	BaseURL        string
	AgentAPIKey    string
	TeamName       string // optional; enables inbox notifications when set
	TeammateName   string // optional; required if TeamName is set
	HomeDir        string // base for ~/.claude/teams/<team>/inboxes/<teammate>.json (defaults to $HOME)
	NotifyTemplate string // optional text/template body for the inbox text field
	Output         io.Writer
}

// Run blocks until ctx is cancelled or a fatal startup error occurs. The
// "[Socket] Connected as ..." log line is emitted to Output as soon as the
// WebSocket session is established — daemon.go polls for that marker to know
// the bridge is ready.
func Run(ctx context.Context, cfg Config) error {
	out := cfg.Output
	if out == nil {
		out = os.Stderr
	}
	logger := log.New(out, "", log.LstdFlags|log.Lmicroseconds)

	if cfg.AgentAPIKey == "" {
		return errors.New("agent api key required")
	}
	if cfg.BaseURL == "" {
		return errors.New("base url required")
	}

	client := band.New(cfg.BaseURL, cfg.AgentAPIKey)
	identity, err := client.AgentMe(cfg.AgentAPIKey)
	if err != nil {
		return fmt.Errorf("fetching identity: %w", err)
	}
	logger.Printf("[Socket] Agent identity: %s (%s, %s)", identity.Name, identity.Handle, identity.ID)

	wsURL, err := websocketURL(cfg.BaseURL, cfg.AgentAPIKey)
	if err != nil {
		return fmt.Errorf("building ws url: %w", err)
	}
	logger.Printf("[Socket] Connecting to %s...", redactAPIKey(wsURL))

	socket := phx.NewSocket(wsURL)

	// "Connected as" is the marker daemon.go polls for in the log to know
	// onboarding succeeded. Emit it the moment the WS handshake completes —
	// channel joins follow.
	var openOnce sync.Once
	connected := make(chan struct{})
	socket.OnOpen(func() {
		openOnce.Do(func() {
			logger.Printf("[Socket] Connected as %s (%s)", identity.Name, identity.ID)
			close(connected)
		})
	})
	socket.OnError(func(err error) {
		logger.Printf("[Socket] error: %v", err)
	})
	socket.OnClose(func() {
		logger.Printf("[Socket] connection closed (library will reconnect)")
	})

	if err := socket.Connect(); err != nil {
		return fmt.Errorf("connecting: %w", err)
	}

	// Wait for the first successful handshake before joining channels.
	select {
	case <-connected:
	case <-time.After(30 * time.Second):
		_ = socket.Disconnect()
		return errors.New("timed out waiting for WebSocket handshake")
	case <-ctx.Done():
		_ = socket.Disconnect()
		return ctx.Err()
	}

	tmpl, err := loadTemplate(cfg.NotifyTemplate)
	if err != nil {
		return fmt.Errorf("parsing notify template: %w", err)
	}

	rooms := newRoomTracker(socket, cfg, tmpl, client, logger)

	// Wire agent_rooms first so we don't miss room_added events that fire
	// while we're pre-fetching existing chats.
	roomsCh := socket.Channel("agent_rooms:"+identity.ID, nil)
	roomsCh.On("room_added", func(payload any) {
		id := stringField(payload, "id")
		title := stringField(payload, "title")
		if id == "" {
			return
		}
		logger.Printf("[Socket] Invited to room %s (%s)", id, title)
		rooms.join(id)
	})
	roomsCh.On("room_removed", func(payload any) {
		id := stringField(payload, "id")
		logger.Printf("[Socket] Removed from room %s", id)
		rooms.markRemoved(id)
	})
	if _, err := roomsCh.Join(); err != nil {
		return fmt.Errorf("joining agent_rooms: %w", err)
	}
	logger.Printf("[Socket] Joined agent_rooms channel - listening for room invitations")

	if err := joinTopic(socket, "agent_contacts:"+identity.ID, logger, "listening for contact requests"); err != nil {
		return err
	}

	// Pre-fetch chats the agent is already in so we don't miss messages on
	// existing rooms across daemon restarts.
	if existing, err := client.ListAgentChats(); err != nil {
		logger.Printf("[Socket] warning: pre-fetch chats failed: %v", err)
	} else {
		logger.Printf("[Socket] Found %d existing chat rooms", len(existing))
		for _, c := range existing {
			rooms.join(c.ID)
		}
	}

	// Park until context is done. The phx library handles heartbeats and
	// reconnects in background goroutines.
	<-ctx.Done()
	_ = socket.Disconnect()
	return nil
}

// roomTracker owns the set of joined chat_room channels and prevents
// double-joining when room_added arrives for a room already pre-fetched.
type roomTracker struct {
	mu     sync.Mutex
	joined map[string]bool
	socket *phx.Socket
	cfg    Config
	tmpl   *template.Template
	client *band.Client
	logger *log.Logger
}

func newRoomTracker(socket *phx.Socket, cfg Config, tmpl *template.Template, client *band.Client, logger *log.Logger) *roomTracker {
	return &roomTracker{
		joined: map[string]bool{},
		socket: socket,
		cfg:    cfg,
		tmpl:   tmpl,
		client: client,
		logger: logger,
	}
}

func (r *roomTracker) join(roomID string) {
	r.mu.Lock()
	if r.joined[roomID] {
		r.mu.Unlock()
		return
	}
	r.joined[roomID] = true
	r.mu.Unlock()

	topic := "chat_room:" + roomID
	ch := r.socket.Channel(topic, nil)
	ch.On("message_created", func(payload any) {
		msg := incomingMessage{
			ChatID:     roomID,
			MessageID:  stringField(payload, "id"),
			SenderName: stringField(payload, "sender_name"),
			SenderID:   stringField(payload, "sender_id"),
			SenderType: stringField(payload, "sender_type"),
			Content:    stringField(payload, "content"),
		}
		handleIncoming(r.cfg, r.tmpl, r.client, r.logger, msg)
	})
	join, err := ch.Join()
	if err != nil {
		r.logger.Printf("[Socket] failed to start join for %s: %v", topic, err)
		return
	}
	join.Receive("ok", func(_ any) {
		r.logger.Printf("[Socket] Joined %s - listening for messages", topic)
	})
	join.Receive("error", func(payload any) {
		r.logger.Printf("[Socket] failed to join %s: %v", topic, payload)
	})
}

func (r *roomTracker) markRemoved(roomID string) {
	r.mu.Lock()
	delete(r.joined, roomID)
	r.mu.Unlock()
	// We let the phx library handle channel cleanup via the existing
	// Socket.Channel/Leave APIs only when reconnecting; for now,
	// dropping the joined-set entry is enough to allow rejoin if invited again.
}

// stringField extracts a string from a Phoenix Channels payload (which arrives
// as map[string]any). Returns "" if absent or not a string.
func stringField(payload any, key string) string {
	m, ok := payload.(map[string]any)
	if !ok {
		return ""
	}
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func joinTopic(socket *phx.Socket, topic string, logger *log.Logger, what string) error {
	ch := socket.Channel(topic, nil)
	join, err := ch.Join()
	if err != nil {
		return fmt.Errorf("joining %s: %w", topic, err)
	}
	join.Receive("ok", func(_ any) {
		logger.Printf("[Socket] Joined %s channel - %s", topic, what)
	})
	join.Receive("error", func(payload any) {
		logger.Printf("[Socket] Failed to join %s: %v", topic, payload)
	})
	return nil
}

func websocketURL(baseURL, apiKey string) (*url.URL, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, err
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	default:
		return nil, fmt.Errorf("unsupported scheme %q in base url", u.Scheme)
	}
	// nshafer/phx appends "/websocket" to the path itself, so we point at the
	// base socket path here (NOT /api/v1/socket/websocket).
	u.Path = "/api/v1/socket"
	q := u.Query()
	q.Set("api_key", apiKey)
	u.RawQuery = q.Encode()
	return u, nil
}

func redactAPIKey(u *url.URL) string {
	clone := *u
	q := clone.Query()
	if q.Get("api_key") != "" {
		q.Set("api_key", "<redacted>")
		clone.RawQuery = q.Encode()
	}
	return clone.String()
}
