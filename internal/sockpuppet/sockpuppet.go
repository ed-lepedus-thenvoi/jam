// Package sockpuppet supplies the Spawner that jam's daemon command uses to
// launch the in-process Band bridge as a detached child. Historically this
// spawned the Elixir agent-sockpuppet via `mix run --no-halt`; now it re-execs
// jam itself with the hidden `internal-bridge` subcommand so the bridge is
// part of the same single binary. The package name is kept for minimal churn.
package sockpuppet

import (
	"fmt"
	"os"
	"os/exec"
)

// Params is everything the spawner needs to construct the child process env.
// SockpuppetDir is retained for backward-compat with existing call sites but
// is no longer used — the bridge runs in-process and needs no external dir.
type Params struct {
	SockpuppetDir string // deprecated: ignored, kept for back-compat
	BaseURL       string
	AgentAPIKey   string
	TeamName      string
	TeammateName  string
}

type Spawner func(Params) (*exec.Cmd, error)

// DefaultSpawner re-execs the current jam binary with the `internal-bridge`
// subcommand, passing Band credentials and optional Claude Code team config
// via env vars (same contract the Elixir sockpuppet used).
func DefaultSpawner(p Params) (*exec.Cmd, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("locating jam binary for self-exec: %w", err)
	}
	cmd := exec.Command(self, "internal-bridge")
	cmd.Env = append(os.Environ(),
		"THENVOI_BASE_URL="+p.BaseURL,
		"THENVOI_AGENT_API_KEY="+p.AgentAPIKey,
	)
	if p.TeamName != "" {
		cmd.Env = append(cmd.Env, "CLAUDE_TEAM_NAME="+p.TeamName)
	}
	if p.TeammateName != "" {
		cmd.Env = append(cmd.Env, "CLAUDE_TEAMMATE_NAME="+p.TeammateName)
	}
	return cmd, nil
}
