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

// jamNotifyTemplate is the EEx template the sockpuppet uses to render
// `text` for inbox notifications when jam is the supervising tool. Jam-flavored
// action guidance replaces the sockpuppet's tool-agnostic default curl recipe.
const jamNotifyTemplate = `[INCOMING BAND MESSAGE]
Incoming Band message from <%= @sender_name %> (<%= @sender_type %>).

Sender:  <%= @sender_name %> (<%= @sender_type %>)
Room:    <%= @chat_id %>
Message: <%= @message_id %>
Content: <%= @content %>

Reply via jam (auto-mentions sender, auto-marks inbound processed):
  jam reply <%= @message_id %> "your reply text here"

Or acknowledge without replying:
  jam ack <%= @message_id %>
`

// DefaultSpawner builds ` + "`mix run --no-halt`" + ` in SockpuppetDir with Band env vars.
func DefaultSpawner(p Params) (*exec.Cmd, error) {
	if p.SockpuppetDir == "" {
		return nil, errors.New("sockpuppet_dir not configured - re-run `jam init --sockpuppet-dir /path/to/agent-sockpuppet`")
	}
	cmd := exec.Command("mix", "run", "--no-halt")
	cmd.Dir = p.SockpuppetDir
	cmd.Env = append(os.Environ(),
		"THENVOI_BASE_URL="+p.BaseURL,
		"THENVOI_AGENT_API_KEY="+p.AgentAPIKey,
		"SOCKPUPPET_NOTIFY_TEMPLATE="+jamNotifyTemplate,
	)
	if p.TeamName != "" {
		cmd.Env = append(cmd.Env, "CLAUDE_TEAM_NAME="+p.TeamName)
	}
	if p.TeammateName != "" {
		cmd.Env = append(cmd.Env, "CLAUDE_TEAMMATE_NAME="+p.TeammateName)
	}
	return cmd, nil
}
