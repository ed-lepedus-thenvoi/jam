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

// DefaultProfile mirrors config.DefaultProfile so this package can stay
// dependency-free of config.
const DefaultProfile = "default"

// State is the persisted record of a running daemon, scoped per-cwd-per-profile.
type State struct {
	Scope        string    `json:"scope"`
	Profile      string    `json:"profile"`
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

func Scope(cwd string) string {
	sum := sha1.Sum([]byte(cwd))
	return filepath.Base(cwd) + "-" + hex.EncodeToString(sum[:])[:8]
}

func resolveProfile(profile string) string {
	if profile == "" {
		return DefaultProfile
	}
	return profile
}

func Dir(homeDir, profile string) string {
	return filepath.Join(homeDir, ".config", "jam", "sessions", resolveProfile(profile))
}

func Path(homeDir, profile, scope string) string {
	return filepath.Join(Dir(homeDir, profile), scope+".json")
}

func LogPath(homeDir, profile, scope string) string {
	return filepath.Join(Dir(homeDir, profile), scope+".log")
}

var ErrNotFound = errors.New("session state not found")

func Load(homeDir, profile, scope string) (*State, error) {
	data, err := os.ReadFile(Path(homeDir, profile, scope))
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

func Save(homeDir, profile string, s *State) error {
	dir := Dir(homeDir, profile)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(Path(homeDir, profile, s.Scope), data, 0o600)
}

func Remove(homeDir, profile, scope string) error {
	err := os.Remove(Path(homeDir, profile, scope))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
