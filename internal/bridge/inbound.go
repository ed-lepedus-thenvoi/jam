package bridge

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"text/template"
	"time"

	"github.com/ed-lepedus-thenvoi/jam/internal/band"
	"github.com/ed-lepedus-thenvoi/jam/internal/inbox"
)

// defaultNotifyTemplate is the jam-flavored prompt body Claude Code sees in
// each <teammate-message>. Override via JAM_NOTIFY_TEMPLATE env var.
const defaultNotifyTemplate = `[INCOMING BAND MESSAGE]
Incoming Band message from {{.SenderHandleOrName}} ({{.SenderType}}).

Sender:  {{.SenderHandleOrName}} ({{.SenderType}})
Room:    {{.ChatID}}
Message: {{.MessageID}}
Content: {{.Content}}

Reply via jam (auto-mentions sender, auto-marks inbound processed):
  jam reply {{.MessageID}} "your reply text here"

Or acknowledge without replying:
  jam ack {{.MessageID}}
`

// templateVars is the data passed to the notify template. Field names match
// the Go convention (PascalCase); the Elixir EEx fields used @snake_case.
// SenderHandle is populated when the bridge can resolve the sender's full
// owner/handle via /agent/peers; SenderHandleOrName falls back to SenderName
// so templates always have something readable to print.
type templateVars struct {
	BaseURL            string
	APIKey             string
	ChatID             string
	MessageID          string
	SenderName         string
	SenderID           string
	SenderHandle       string
	SenderHandleOrName string
	SenderType         string
	Content            string
}

type incomingMessage struct {
	ChatID     string
	MessageID  string
	SenderName string
	SenderID   string
	SenderType string
	Content    string
}

// handleIncoming runs for every message_created event received on a chat_room
// channel. It POSTs the processing transition, resolves the sender's full
// handle (and rewrites any `@[[uuid]]` patterns in content text) via the peer
// cache, renders the template, and appends a notification to the teammate
// inbox file (if team integration is configured).
func handleIncoming(cfg Config, tmpl *template.Template, cache *peerCache, client *band.Client, logger *log.Logger, msg incomingMessage) {
	if msg.MessageID == "" {
		return
	}
	logger.Printf("[INCOMING] room=%s msg=%s from=%s (%s): %s",
		msg.ChatID, msg.MessageID, msg.SenderName, msg.SenderType, msg.Content)

	if err := client.MarkProcessing(msg.ChatID, msg.MessageID); err != nil {
		logger.Printf("[Handler] mark_processing failed: %v", err)
		// Continue anyway — the inbox notification is the more important side.
	}

	if cfg.TeamName == "" || cfg.TeammateName == "" {
		return
	}

	// Resolve sender's full owner/handle if available; fall back to bare name.
	senderHandle := ""
	if peer, ok := cache.lookupByID(msg.SenderID); ok {
		senderHandle = peer.Handle
	}
	senderHandleOrName := senderHandle
	if senderHandleOrName == "" {
		senderHandleOrName = msg.SenderName
	}

	// Rewrite any platform-injected `@[[uuid]]` tokens to `@owner/handle` so
	// the displayed text uses a single, copy-paste-friendly form that round-
	// trips back through `jam send` / `jam reply`.
	content := rewriteInboundMentions(msg.Content, cache)

	vars := templateVars{
		BaseURL:            cfg.BaseURL,
		APIKey:             cfg.AgentAPIKey,
		ChatID:             msg.ChatID,
		MessageID:          msg.MessageID,
		SenderName:         msg.SenderName,
		SenderID:           msg.SenderID,
		SenderHandle:       senderHandle,
		SenderHandleOrName: senderHandleOrName,
		SenderType:         msg.SenderType,
		Content:            content,
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, vars); err != nil {
		logger.Printf("[Handler] template render failed: %v", err)
		return
	}

	summarySnippet := content
	if len(summarySnippet) > 50 {
		summarySnippet = summarySnippet[:50]
	}
	notification := inbox.Notification{
		From:      "thenvoi-platform",
		Text:      buf.String(),
		Summary:   fmt.Sprintf("Platform message from %s (%s): %s", senderHandleOrName, msg.SenderType, summarySnippet),
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Read:      false,
		Band: &inbox.BandFields{
			ChatID:       msg.ChatID,
			MessageID:    msg.MessageID,
			SenderID:     msg.SenderID,
			SenderName:   msg.SenderName,
			SenderHandle: senderHandle,
			SenderType:   msg.SenderType,
			Content:      content,
		},
	}

	if err := appendInboxNotification(cfg.HomeDir, cfg.TeamName, cfg.TeammateName, notification); err != nil {
		logger.Printf("[Handler] writing inbox file failed: %v", err)
		return
	}
	logger.Printf("[Handler] Notified teammate %s via inbox", cfg.TeammateName)
}

// appendInboxNotification reads the existing inbox JSON (a list of
// notifications), appends, and writes atomically. Concurrent writes from
// multiple bridge instances in the same team/teammate would race; we tolerate
// that for now since the design is one bridge per (cwd, profile).
func appendInboxNotification(homeDir, teamName, teammateName string, n inbox.Notification) error {
	path := inbox.Path(homeDir, teamName, teammateName)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}

	var existing []inbox.Notification
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &existing)
	} else if !os.IsNotExist(err) {
		return err
	}
	existing = append(existing, n)

	data, err := json.Marshal(existing)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// loadTemplate parses the user-supplied template (or the default if unset).
// Returns an error only if the user-supplied template fails to parse; an
// unset/empty template just returns the default.
func loadTemplate(userTemplate string) (*template.Template, error) {
	body := userTemplate
	if body == "" {
		body = defaultNotifyTemplate
	}
	return template.New("notify").Parse(body)
}

// inboundMentionRe matches platform-resolved mention tokens like
// `@[[ae82ec5b-79ae-465a-8f5a-bd6a883b47dc]]`. The capture accepts any
// non-`]` token rather than enforcing strict UUID syntax — keeps us robust
// against future platform id-format changes and avoids surprising the model
// if a non-standard id ever shows up.
var inboundMentionRe = regexp.MustCompile(`@\[\[([^\]]+)\]\]`)

// rewriteInboundMentions replaces each known `@[[uuid]]` with `@owner/handle`.
// Unknown UUIDs are left intact (visible to the model so they can ask). Self
// resolves through the cache's synthetic self entry, so own-mentions get
// readable handles too.
func rewriteInboundMentions(content string, cache *peerCache) string {
	return inboundMentionRe.ReplaceAllStringFunc(content, func(match string) string {
		m := inboundMentionRe.FindStringSubmatch(match)
		if len(m) < 2 {
			return match
		}
		if peer, ok := cache.lookupByID(m[1]); ok && peer.Handle != "" {
			return "@" + peer.Handle
		}
		return match
	})
}
