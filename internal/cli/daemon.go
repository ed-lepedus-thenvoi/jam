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

	"github.com/thenvoi/jam/internal/band"
	"github.com/thenvoi/jam/internal/session"
	"github.com/thenvoi/jam/internal/sockpuppet"
)

// connectedMarker is the substring we wait for in the sockpuppet log to know
// the WebSocket joined and the bridge is ready to receive inbound messages.
const connectedMarker = "[Socket] Connected as"

const connectTimeout = 30 * time.Second

func newDaemonCmd(stdin io.Reader, stdout, stderr io.Writer, env Env, getProfile func() string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Manage the Band sockpuppet daemon for this working directory",
	}
	cmd.AddCommand(newDaemonStartCmd(stdout, stderr, env, getProfile))
	cmd.AddCommand(newDaemonStopCmd(stdout, stderr, env, getProfile))
	cmd.AddCommand(newDaemonStatusCmd(stdout, env, getProfile))
	return cmd
}

func newDaemonStartCmd(stdout, stderr io.Writer, env Env, getProfile func() string) *cobra.Command {
	var name, teamName, teammateName string
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Provision an agent and start the sockpuppet (idempotent per cwd)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDaemonStart(stdout, env, getProfile(), name, teamName, teammateName)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Override the auto-derived agent name")
	cmd.Flags().StringVar(&teamName, "team", "", "Claude Code team name for inbox notifications")
	cmd.Flags().StringVar(&teammateName, "teammate", "", "Claude Code teammate name for inbox notifications")
	return cmd
}

func runDaemonStart(stdout io.Writer, env Env, profile, nameOverride, teamName, teammateName string) error {
	st, already, err := ensureDaemonRunning(env, profile, nameOverride, teamName, teammateName)
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
func ensureDaemonRunning(env Env, profile, nameOverride, teamName, teammateName string) (*session.State, bool, error) {
	cfg, err := loadConfigOrHint(env.HomeDir, profile)
	if err != nil {
		return nil, false, err
	}
	if env.SpawnSockpuppet == nil {
		return nil, false, errors.New("internal: SpawnSockpuppet not configured")
	}

	scope := session.Scope(env.Cwd)

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

	logPath := session.LogPath(env.HomeDir, profile, scope)
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		return rollback(fmt.Errorf("creating sessions dir: %w", err))
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return rollback(fmt.Errorf("opening log: %w", err))
	}

	spawnCmd, err := env.SpawnSockpuppet(sockpuppet.Params{
		SockpuppetDir: cfg.SockpuppetDir,
		BaseURL:       cfg.BaseURL,
		AgentAPIKey:   registered.APIKey,
		TeamName:      teamName,
		TeammateName:  teammateName,
	})
	if err != nil {
		logFile.Close()
		return rollback(fmt.Errorf("preparing sockpuppet: %w", err))
	}
	spawnCmd.Stdout = logFile
	spawnCmd.Stderr = logFile
	if spawnCmd.SysProcAttr == nil {
		spawnCmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	spawnCmd.SysProcAttr.Setsid = true

	if err := spawnCmd.Start(); err != nil {
		logFile.Close()
		return rollback(fmt.Errorf("spawning sockpuppet: %w", err))
	}
	pid := spawnCmd.Process.Pid

	if err := waitForConnected(logPath, pid); err != nil {
		_ = syscall.Kill(pid, syscall.SIGTERM)
		logFile.Close()
		return rollback(err)
	}
	logFile.Close()

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

func newDaemonStopCmd(stdout, stderr io.Writer, env Env, getProfile func() string) *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the sockpuppet and deregister the per-cwd agent",
		RunE: func(cmd *cobra.Command, args []string) error {
			profile := getProfile()
			cfg, err := loadConfigOrHint(env.HomeDir, profile)
			if err != nil {
				return err
			}
			scope := session.Scope(env.Cwd)
			st, err := session.Load(env.HomeDir, profile, scope)
			if err != nil {
				if errors.Is(err, session.ErrNotFound) {
					fmt.Fprintln(stdout, "Not running")
					return nil
				}
				return err
			}
			// Kill the process group so any child mix-spawned beam processes
			// also go down. Setsid at start time made the child a session leader,
			// so its PID == its pgid.
			if processAlive(st.PID) {
				_ = syscall.Kill(-st.PID, syscall.SIGTERM)
				// Brief grace period for clean shutdown.
				deadline := time.Now().Add(3 * time.Second)
				for time.Now().Before(deadline) && processAlive(st.PID) {
					time.Sleep(100 * time.Millisecond)
				}
				if processAlive(st.PID) {
					_ = syscall.Kill(-st.PID, syscall.SIGKILL)
				}
			}
			if err := band.New(cfg.BaseURL, cfg.UserAPIKey).DeleteAgent(st.AgentID, true); err != nil {
				fmt.Fprintf(stderr, "warning: failed to delete agent %s: %v\n", st.AgentID, err)
			}
			if err := session.Remove(env.HomeDir, profile, scope); err != nil {
				return fmt.Errorf("removing session state: %w", err)
			}
			fmt.Fprintf(stdout, "Stopped %s (pid %d)\n", st.Handle, st.PID)
			return nil
		},
	}
}

func newDaemonStatusCmd(stdout io.Writer, env Env, getProfile func() string) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the sockpuppet status for this working directory",
		RunE: func(cmd *cobra.Command, args []string) error {
			profile := getProfile()
			scope := session.Scope(env.Cwd)
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
