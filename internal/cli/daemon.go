package cli

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/ed-lepedus-thenvoi/jam/internal/band"
	"github.com/ed-lepedus-thenvoi/jam/internal/config"
	"github.com/ed-lepedus-thenvoi/jam/internal/session"
	"github.com/ed-lepedus-thenvoi/jam/internal/sockpuppet"
)

// connectedMarker is the substring we wait for in the sockpuppet log to know
// the WebSocket joined and the bridge is ready to receive inbound messages.
const connectedMarker = "[Socket] Connected as"

const connectTimeout = 30 * time.Second

func newDaemonCmd(stdin io.Reader, stdout, stderr io.Writer, env Env, getProfile, getScope func() string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Manage the Band sockpuppet daemon for this working directory",
	}
	cmd.AddCommand(newDaemonStartCmd(stdout, stderr, env, getProfile, getScope))
	cmd.AddCommand(newDaemonStopCmd(stdout, stderr, env, getProfile, getScope))
	cmd.AddCommand(newDaemonStatusCmd(stdout, env, getProfile, getScope))
	cmd.AddCommand(newDaemonRestartCmd(stdout, stderr, env, getProfile, getScope))
	return cmd
}

func newDaemonStartCmd(stdout, stderr io.Writer, env Env, getProfile, getScope func() string) *cobra.Command {
	var name, teamName, teammateName string
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Provision an agent and start the sockpuppet (idempotent per cwd/session)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDaemonStart(stdout, env, getProfile(), getScope(), name, teamName, teammateName)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Override the auto-derived agent name")
	cmd.Flags().StringVar(&teamName, "team", "", "Claude Code team name for inbox notifications")
	cmd.Flags().StringVar(&teammateName, "teammate", "", "Claude Code teammate name for inbox notifications")
	return cmd
}

func runDaemonStart(stdout io.Writer, env Env, profile, scope, nameOverride, teamName, teammateName string) error {
	st, already, err := ensureDaemonRunning(env, profile, scope, nameOverride, teamName, teammateName)
	if err != nil {
		return err
	}
	if already {
		fmt.Fprintf(stdout, "Already running as %s (pid %d, log %s)\n", st.Handle, st.PID, st.LogPath)
		return nil
	}
	fmt.Fprintf(stdout, "Started %s (pid %d)\nLog: %s\n", st.Handle, st.PID, st.LogPath)
	return nil
}

// ensureDaemonRunning is the idempotent core shared by `daemon start` and
// `onboard`. Returns the live session state and a bool indicating whether the
// daemon was already running (true) or was just started (false).
func ensureDaemonRunning(env Env, profile, scope, nameOverride, teamName, teammateName string) (*session.State, bool, error) {
	cfg, err := loadConfigOrHint(env.HomeDir, profile)
	if err != nil {
		return nil, false, err
	}
	if env.SpawnSockpuppet == nil {
		return nil, false, errors.New("internal: SpawnSockpuppet not configured")
	}

	if st, err := session.Load(env.HomeDir, profile, scope); err == nil {
		if processAlive(st.PID) {
			return st, true, nil
		}
		_ = session.Remove(env.HomeDir, profile, scope)
	}

	agentName := nameOverride
	if agentName == "" {
		agentName = defaultAgentName(env.Cwd)
	}
	description := defaultDescription(env.Cwd)

	bandClient := band.New(cfg.BaseURL, cfg.UserAPIKey)
	registered, err := bandClient.RegisterAgent(agentName, description)
	if err != nil {
		return nil, false, fmt.Errorf("registering agent: %w", err)
	}

	// From here on, failures must roll back the agent registration so we don't
	// orphan it on the platform.
	rollback := func(reason error) (*session.State, bool, error) {
		if delErr := bandClient.DeleteAgent(registered.Agent.ID, true); delErr != nil {
			return nil, false, fmt.Errorf("%w (additionally, rollback delete failed: %v)", reason, delErr)
		}
		return nil, false, reason
	}

	pid, logPath, err := spawnBridge(env, profile, scope, cfg, registered.APIKey, teamName, teammateName)
	if err != nil {
		return rollback(err)
	}

	identity, err := bandClient.AgentMe(registered.APIKey)
	if err != nil {
		_ = syscall.Kill(pid, syscall.SIGTERM)
		return rollback(fmt.Errorf("resolving handle: %w", err))
	}

	st := &session.State{
		Scope:        scope,
		Profile:      profile,
		Cwd:          env.Cwd,
		AgentID:      registered.Agent.ID,
		AgentName:    registered.Agent.Name,
		AgentAPIKey:  registered.APIKey,
		Handle:       identity.Handle,
		PID:          pid,
		LogPath:      logPath,
		TeamName:     teamName,
		TeammateName: teammateName,
		StartedAt:    time.Now().UTC(),
	}
	if err := session.Save(env.HomeDir, profile, st); err != nil {
		_ = syscall.Kill(pid, syscall.SIGTERM)
		return rollback(fmt.Errorf("saving session: %w", err))
	}

	return st, false, nil
}

func newDaemonStopCmd(stdout, stderr io.Writer, env Env, getProfile, getScope func() string) *cobra.Command {
	var keep bool
	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop the bridge (defaults to also force-deleting the per-cwd agent)",
		Long: "Tears down the bridge process and removes local session state. By default also " +
			"force-deletes the Band agent from the platform, since per-cwd agents are intended " +
			"to be ephemeral. Pass --keep to preserve the agent (useful when you're stopping " +
			"only to pick up a new binary, or to resume later — pair with `jam daemon restart` " +
			"or a fresh `jam onboard` from the same cwd to come back online with the same handle).",
		RunE: func(cmd *cobra.Command, args []string) error {
			profile := getProfile()
			cfg, err := loadConfigOrHint(env.HomeDir, profile)
			if err != nil {
				return err
			}
			scope := getScope()
			st, err := session.Load(env.HomeDir, profile, scope)
			if err != nil {
				if errors.Is(err, session.ErrNotFound) {
					fmt.Fprintln(stdout, "Not running")
					return nil
				}
				return err
			}
			stopBridgeProcess(st.PID)
			if keep {
				fmt.Fprintf(stdout, "Stopped %s (pid %d); agent %s preserved on the platform.\n",
					st.Handle, st.PID, st.AgentID)
			} else {
				if err := band.New(cfg.BaseURL, cfg.UserAPIKey).DeleteAgent(st.AgentID, true); err != nil {
					fmt.Fprintf(stderr, "warning: failed to delete agent %s: %v\n", st.AgentID, err)
				}
				fmt.Fprintf(stdout, "Stopped %s (pid %d)\n", st.Handle, st.PID)
			}
			if err := session.Remove(env.HomeDir, profile, scope); err != nil {
				return fmt.Errorf("removing session state: %w", err)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&keep, "keep", false,
		"Preserve the agent on the platform (skip force-delete). Pair with `jam daemon restart` or a future `jam onboard` to come back with the same handle.")
	return cmd
}

func newDaemonRestartCmd(stdout, stderr io.Writer, env Env, getProfile, getScope func() string) *cobra.Command {
	return &cobra.Command{
		Use:   "restart",
		Short: "Restart the bridge, preserving the agent identity (no register/delete)",
		Long: "Bounces the bridge process without touching the platform agent. Useful for " +
			"picking up a new jam binary after `brew upgrade`, or recovering from a crashed " +
			"bridge without losing your handle. Requires existing session state for this cwd; " +
			"run `jam onboard` first if you don't have a bridge yet.",
		RunE: func(cmd *cobra.Command, args []string) error {
			profile := getProfile()
			scope := getScope()
			st, err := session.Load(env.HomeDir, profile, scope)
			if err != nil {
				if errors.Is(err, session.ErrNotFound) {
					return missingSessionError(env.HomeDir, profile, scope,
						fmt.Sprintf("no session state for scope %q; can't restart what isn't there", scope))
				}
				return err
			}
			cfg, err := loadConfigOrHint(env.HomeDir, profile)
			if err != nil {
				return err
			}
			oldPID := st.PID
			stopBridgeProcess(oldPID)

			pid, logPath, err := spawnBridge(env, profile, scope, cfg, st.AgentAPIKey, st.TeamName, st.TeammateName)
			if err != nil {
				return fmt.Errorf("restarting bridge: %w", err)
			}
			st.PID = pid
			st.LogPath = logPath
			st.StartedAt = time.Now().UTC()
			if err := session.Save(env.HomeDir, profile, st); err != nil {
				_ = syscall.Kill(pid, syscall.SIGTERM)
				return fmt.Errorf("saving session: %w", err)
			}
			fmt.Fprintf(stdout, "Restarted %s (pid %d → %d)\nLog: %s\n", st.Handle, oldPID, pid, logPath)
			return nil
		},
	}
}

// stopBridgeProcess SIGTERMs the bridge's process group with a 3s grace
// period, then SIGKILLs. Setsid at spawn made each bridge a session leader,
// so its PID is also its pgid.
func stopBridgeProcess(pid int) {
	if !processAlive(pid) {
		return
	}
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && processAlive(pid) {
		time.Sleep(100 * time.Millisecond)
	}
	if processAlive(pid) {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	}
}

// spawnBridge opens (or truncates) the session log, prepares an exec.Cmd via
// env.SpawnSockpuppet, starts it detached, and polls the log until the
// "Connected as" marker appears. Returns the new pid and the log path. The
// agent registration / identity lookup / state save are the caller's job.
func spawnBridge(env Env, profile, scope string, cfg *config.Config, agentAPIKey, teamName, teammateName string) (int, string, error) {
	logPath := session.LogPath(env.HomeDir, profile, scope)
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		return 0, "", fmt.Errorf("creating sessions dir: %w", err)
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, "", fmt.Errorf("opening log: %w", err)
	}

	spawnCmd, err := env.SpawnSockpuppet(sockpuppet.Params{
		SockpuppetDir: cfg.SockpuppetDir,
		BaseURL:       cfg.BaseURL,
		AgentAPIKey:   agentAPIKey,
		TeamName:      teamName,
		TeammateName:  teammateName,
	})
	if err != nil {
		logFile.Close()
		return 0, "", fmt.Errorf("preparing sockpuppet: %w", err)
	}
	spawnCmd.Stdout = logFile
	spawnCmd.Stderr = logFile
	if spawnCmd.SysProcAttr == nil {
		spawnCmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	spawnCmd.SysProcAttr.Setsid = true

	if err := spawnCmd.Start(); err != nil {
		logFile.Close()
		return 0, "", fmt.Errorf("spawning sockpuppet: %w", err)
	}
	pid := spawnCmd.Process.Pid

	if err := waitForConnected(logPath, pid); err != nil {
		_ = syscall.Kill(pid, syscall.SIGTERM)
		logFile.Close()
		return 0, "", err
	}
	logFile.Close()

	return pid, logPath, nil
}

func newDaemonStatusCmd(stdout io.Writer, env Env, getProfile, getScope func() string) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the sockpuppet status for this working directory",
		RunE: func(cmd *cobra.Command, args []string) error {
			profile := getProfile()
			scope := getScope()
			st, err := session.Load(env.HomeDir, profile, scope)
			if err != nil {
				if errors.Is(err, session.ErrNotFound) {
					fmt.Fprintln(stdout, "Not running")
					return nil
				}
				return err
			}
			if !processAlive(st.PID) {
				fmt.Fprintf(stdout, "Stale state (pid %d not alive); run 'jam daemon stop' to clean up\n", st.PID)
				return nil
			}
			uptime := time.Since(st.StartedAt).Round(time.Second)
			fmt.Fprintf(stdout, "Running %s (pid %d, uptime %s)\nLog: %s\n", st.Handle, st.PID, uptime, st.LogPath)
			return nil
		},
	}
}

func waitForConnected(logPath string, pid int) error {
	deadline := time.Now().Add(connectTimeout)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			data, _ := os.ReadFile(logPath)
			return fmt.Errorf("sockpuppet exited before connecting:\n%s", data)
		}
		data, _ := os.ReadFile(logPath)
		if strings.Contains(string(data), connectedMarker) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	data, _ := os.ReadFile(logPath)
	return fmt.Errorf("timed out after %s waiting for sockpuppet to connect:\n%s", connectTimeout, data)
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, syscall.Signal(0)) == nil
}

func defaultAgentName(cwd string) string {
	base := filepath.Base(cwd)
	if base == "" || base == "." || base == "/" {
		base = "claude"
	}
	// Constrain to Band's 3-100 chars and avoid weird characters.
	base = sanitizeAgentNamePart(base)
	suffix := randHex(3) // 6 hex chars
	return "claude-" + base + "-" + suffix
}

func defaultDescription(cwd string) string {
	return "Claude Code session managed by jam in " + cwd
}

func sanitizeAgentNamePart(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
		case r == '_' || r == ' ':
			b.WriteRune('-')
		}
	}
	out := b.String()
	if out == "" {
		out = "session"
	}
	return out
}

func randHex(n int) string {
	buf := make([]byte, n)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}
