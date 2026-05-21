package sockpuppet

import (
	"errors"
	"os"
	"os/exec"
)

// Params is everything the spawner needs to build a sockpuppet command.
type Params struct {
	SockpuppetDir string
	BaseURL       string
	AgentAPIKey   string
	TeamName      string
	TeammateName  string
}

// Spawner returns a prepared *exec.Cmd. The caller wires log redirection and
// process-group handling before calling Start. Returning *exec.Cmd (not running
// it) keeps the daemon code in control of file descriptors and lifetime.
type Spawner func(Params) (*exec.Cmd, error)

// DefaultSpawner builds `mix run --no-halt` in SockpuppetDir with Band env vars.
func DefaultSpawner(p Params) (*exec.Cmd, error) {
	if p.SockpuppetDir == "" {
		return nil, errors.New("sockpuppet_dir not configured - re-run `jam init --sockpuppet-dir /path/to/agent-sockpuppet`")
	}
	cmd := exec.Command("mix", "run", "--no-halt")
	cmd.Dir = p.SockpuppetDir
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
