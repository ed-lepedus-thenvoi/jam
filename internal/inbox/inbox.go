package inbox

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// Notification mirrors the JSON the sockpuppet writes into the Claude Code
// teammate inbox. `text` is the prompt body Claude sees; `Band` carries the
// structured fields jam acts on (chat_id, message_id, sender_id, etc.).
type Notification struct {
	From      string      `json:"from"`
	Text      string      `json:"text"`
	Summary   string      `json:"summary"`
	Timestamp string      `json:"timestamp"`
	Read      bool        `json:"read"`
	Band      *BandFields `json:"band,omitempty"`
}

type BandFields struct {
	ChatID       string `json:"chat_id"`
	MessageID    string `json:"message_id"`
	SenderID     string `json:"sender_id"`
	SenderName   string `json:"sender_name"`
	SenderHandle string `json:"sender_handle,omitempty"`
	SenderType   string `json:"sender_type"`
	Content      string `json:"content"`
}

func Path(homeDir, teamName, teammateName string) string {
	return filepath.Join(homeDir, ".claude", "teams", teamName, "inboxes", teammateName+".json")
}

var ErrNoTeamConfigured = errors.New("session has no team/teammate set - re-run onboard with --team")

// Read returns the notifications currently in the inbox. Missing file = empty
// list (the sockpuppet writes lazily on first inbound). Empty team or teammate
// returns ErrNoTeamConfigured so callers can surface a useful hint.
func Read(homeDir, teamName, teammateName string) ([]Notification, error) {
	if teamName == "" || teammateName == "" {
		return nil, ErrNoTeamConfigured
	}
	data, err := os.ReadFile(Path(homeDir, teamName, teammateName))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var ns []Notification
	if err := json.Unmarshal(data, &ns); err != nil {
		return nil, err
	}
	return ns, nil
}

// FindByMessageID returns the first notification whose Band.MessageID matches.
// Returns nil if not found.
func FindByMessageID(ns []Notification, msgID string) *Notification {
	for i := range ns {
		if ns[i].Band != nil && ns[i].Band.MessageID == msgID {
			return &ns[i]
		}
	}
	return nil
}
