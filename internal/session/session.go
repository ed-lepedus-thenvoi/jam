package session

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// State is the persisted record of a running daemon, scoped per-cwd.
type State struct {
	Scope        string    `json:"scope"`
	Cwd          string    `json:"cwd"`
	AgentID      string    `json:"agent_id"`
	AgentName    string    `json:"agent_name"`
	AgentAPIKey  string    `json:"agent_api_key"`
	Handle       string    `json:"handle"`
	PID          int       `json:"pid"`
	LogPath      string    `json:"log_path"`
	TeamName     string    `json:"team_name,omitempty"`
	TeammateName string    `json:"teammate_name,omitempty"`
	StartedAt    time.Time `json:"started_at"`
}

// Scope derives a stable, filesystem-safe identifier for a working directory.
// Format: <basename>-<sha1[:8]>. Basename gives human readability, hash avoids
// collisions between same-named directories in different paths.
func Scope(cwd string) string {
	sum := sha1.Sum([]byte(cwd))
	return filepath.Base(cwd) + "-" + hex.EncodeToString(sum[:])[:8]
}

func Path(homeDir, scope string) string {
	return filepath.Join(homeDir, ".config", "jam", "sessions", scope+".json")
}

var ErrNotFound = errors.New("session state not found")

func Load(homeDir, scope string) (*State, error) {
	data, err := os.ReadFile(Path(homeDir, scope))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func Save(homeDir string, s *State) error {
	dir := filepath.Join(homeDir, ".config", "jam", "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(Path(homeDir, s.Scope), data, 0o600)
}

func Remove(homeDir, scope string) error {
	err := os.Remove(Path(homeDir, scope))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
