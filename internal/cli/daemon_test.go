package cli

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ed-lepedus-thenvoi/jam/internal/session"
	"github.com/ed-lepedus-thenvoi/jam/internal/sockpuppet"
)

// daemonHarness wires up a mock Band server, a stub sockpuppet script, and
// returns the Env to feed to Execute(). Subprocesses spawned by the test are
// killed in t.Cleanup so they don't outlive the test.
type daemonHarness struct {
	home          string
	cwd           string
	srv           *httptest.Server
	registerCalls atomic.Int32
	deleteCalls   atomic.Int32
	spawned       []*exec.Cmd
	connectLine   string
}

func newDaemonHarness(t *testing.T) *daemonHarness {
	t.Helper()
	h := &daemonHarness{
		home:        t.TempDir(),
		cwd:         t.TempDir(),
		connectLine: "[Socket] Connected as test-stub",
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/me/agents/register", func(w http.ResponseWriter, r *http.Request) {
		h.registerCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{"data":{"agent":{"id":"new-agent-id","name":"test-stub","description":"stub","inserted_at":"2026-05-21T00:00:00Z","owner_id":"owner-1"},"credentials":{"api_key":"band_a_NEW"}}}`))
	})
	mux.HandleFunc("/api/v1/agent/me", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "band_a_NEW" {
			w.WriteHeader(403)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"id":"new-agent-id","name":"test-stub","handle":"ed.lepedus/test-stub"}}`))
	})
	mux.HandleFunc("/api/v1/me/agents/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" {
			h.deleteCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"id":"new-agent-id","executions_deleted":0}}`))
			return
		}
		w.WriteHeader(404)
	})
	h.srv = httptest.NewServer(mux)
	t.Cleanup(h.srv.Close)
	writeConfig(t, h.home, h.srv.URL, "band_u_test")

	t.Cleanup(func() {
		for _, c := range h.spawned {
			if c.Process != nil {
				_ = c.Process.Kill()
			}
		}
	})
	return h
}

func (h *daemonHarness) env(t *testing.T) Env {
	t.Helper()
	scriptDir := t.TempDir()
	script := filepath.Join(scriptDir, "stub.sh")
	body := "#!/bin/sh\necho '" + h.connectLine + "'\nexec sleep 60\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return Env{
		HomeDir: h.home,
		Cwd:     h.cwd,
		Getenv:  os.Getenv,
		SpawnSockpuppet: func(p sockpuppet.Params) (*exec.Cmd, error) {
			cmd := exec.Command("sh", script)
			h.spawned = append(h.spawned, cmd)
			// In tests, this process is the spawned child's parent. Production
			// jam exits after start so init reaps; here we must Wait to avoid
			// leaving zombies, which would make kill-0 falsely report alive.
			go func() {
				for cmd.Process == nil {
					time.Sleep(10 * time.Millisecond)
				}
				_, _ = cmd.Process.Wait()
			}()
			return cmd, nil
		},
	}
}

func TestDaemonStart_ColdStartProvisionsAndSpawns(t *testing.T) {
	h := newDaemonHarness(t)

	var stdout, stderr bytes.Buffer
	code := Execute([]string{"daemon", "start", "--name", "test-stub"}, nil, &stdout, &stderr, h.env(t))
	if code != 0 {
		t.Fatalf("exit %d\nstderr: %s", code, stderr.String())
	}
	if got := h.registerCalls.Load(); got != 1 {
		t.Errorf("expected 1 register call, got %d", got)
	}
	if !strings.Contains(stdout.String(), "ed.lepedus/test-stub") {
		t.Errorf("expected stdout to mention handle, got: %s", stdout.String())
	}

	scope := session.Scope(h.cwd)
	st, err := session.Load(h.home, "", scope)
	if err != nil {
		t.Fatalf("session state not saved: %v", err)
	}
	if st.AgentID != "new-agent-id" {
		t.Errorf("AgentID = %s", st.AgentID)
	}
	if st.AgentAPIKey != "band_a_NEW" {
		t.Errorf("AgentAPIKey = %s", st.AgentAPIKey)
	}
	if st.Handle != "ed.lepedus/test-stub" {
		t.Errorf("Handle = %s", st.Handle)
	}
	if st.PID == 0 {
		t.Errorf("PID not recorded")
	}
	if !processAlive(st.PID) {
		t.Errorf("expected PID %d to be alive after start", st.PID)
	}

	// Log file should contain the connected line we polled for.
	data, err := os.ReadFile(st.LogPath)
	if err != nil {
		t.Fatalf("log file not readable: %v", err)
	}
	if !strings.Contains(string(data), h.connectLine) {
		t.Errorf("log missing connected marker, got:\n%s", data)
	}
}

func TestDaemonStart_SecondStartIsIdempotent(t *testing.T) {
	h := newDaemonHarness(t)
	env := h.env(t)

	var s1out, s1err bytes.Buffer
	if code := Execute([]string{"daemon", "start", "--name", "test-stub"}, nil, &s1out, &s1err, env); code != 0 {
		t.Fatalf("first start failed: exit %d\n%s", code, s1err.String())
	}
	var s2out, s2err bytes.Buffer
	code := Execute([]string{"daemon", "start", "--name", "test-stub"}, nil, &s2out, &s2err, env)
	if code != 0 {
		t.Fatalf("second start should be a no-op success, got exit %d\n%s", code, s2err.String())
	}
	if got := h.registerCalls.Load(); got != 1 {
		t.Errorf("expected exactly 1 register call across two starts, got %d", got)
	}
	if !strings.Contains(s2out.String(), "lready running") {
		t.Errorf("expected 'already running' on second start, got: %s", s2out.String())
	}
}

func TestDaemonStop_KillsDeletesAndCleans(t *testing.T) {
	h := newDaemonHarness(t)
	env := h.env(t)

	var s1out, s1err bytes.Buffer
	if code := Execute([]string{"daemon", "start"}, nil, &s1out, &s1err, env); code != 0 {
		t.Fatalf("start failed: %s", s1err.String())
	}
	scope := session.Scope(h.cwd)
	st, _ := session.Load(h.home, "", scope)
	pidBefore := st.PID

	var stdout, stderr bytes.Buffer
	if code := Execute([]string{"daemon", "stop"}, nil, &stdout, &stderr, env); code != 0 {
		t.Fatalf("stop failed: exit %d\n%s", code, stderr.String())
	}
	if h.deleteCalls.Load() != 1 {
		t.Errorf("expected 1 DELETE call, got %d", h.deleteCalls.Load())
	}
	if _, err := session.Load(h.home, "", scope); err == nil {
		t.Errorf("expected session state removed after stop")
	}
	// Give the OS a moment to reap.
	for i := 0; i < 20 && processAlive(pidBefore); i++ {
		time.Sleep(50 * time.Millisecond)
	}
	if processAlive(pidBefore) {
		t.Errorf("expected PID %d to be dead after stop", pidBefore)
	}
}

func TestDaemonStop_KeepSkipsAgentDelete(t *testing.T) {
	h := newDaemonHarness(t)
	env := h.env(t)
	if code := Execute([]string{"daemon", "start"}, nil, &bytes.Buffer{}, &bytes.Buffer{}, env); code != 0 {
		t.Fatal("start failed")
	}
	scope := session.Scope(h.cwd)
	st, _ := session.Load(h.home, "", scope)
	pidBefore := st.PID

	var stdout, stderr bytes.Buffer
	if code := Execute([]string{"daemon", "stop", "--keep"}, nil, &stdout, &stderr, env); code != 0 {
		t.Fatalf("stop --keep failed: exit %d\n%s", code, stderr.String())
	}
	if got := h.deleteCalls.Load(); got != 0 {
		t.Errorf("expected NO DELETE calls with --keep, got %d", got)
	}
	if _, err := session.Load(h.home, "", scope); err == nil {
		t.Errorf("expected local session state removed even with --keep")
	}
	for i := 0; i < 20 && processAlive(pidBefore); i++ {
		time.Sleep(50 * time.Millisecond)
	}
	if processAlive(pidBefore) {
		t.Errorf("expected PID %d to be dead after stop --keep", pidBefore)
	}
}

func TestDaemonRestart_BouncesBridgeKeepsAgent(t *testing.T) {
	h := newDaemonHarness(t)
	env := h.env(t)
	if code := Execute([]string{"daemon", "start"}, nil, &bytes.Buffer{}, &bytes.Buffer{}, env); code != 0 {
		t.Fatal("start failed")
	}
	scope := session.Scope(h.cwd)
	st1, _ := session.Load(h.home, "", scope)
	oldPID := st1.PID
	agentID := st1.AgentID

	var stdout, stderr bytes.Buffer
	if code := Execute([]string{"daemon", "restart"}, nil, &stdout, &stderr, env); code != 0 {
		t.Fatalf("restart failed: exit %d\n%s", code, stderr.String())
	}
	if got := h.registerCalls.Load(); got != 1 {
		t.Errorf("expected register count to remain 1 across restart, got %d", got)
	}
	if got := h.deleteCalls.Load(); got != 0 {
		t.Errorf("expected NO DELETE during restart, got %d", got)
	}
	st2, _ := session.Load(h.home, "", scope)
	if st2.AgentID != agentID {
		t.Errorf("AgentID should be preserved across restart: was %s, now %s", agentID, st2.AgentID)
	}
	if st2.PID == oldPID {
		t.Errorf("PID should have changed after restart (still %d)", oldPID)
	}
	for i := 0; i < 20 && processAlive(oldPID); i++ {
		time.Sleep(50 * time.Millisecond)
	}
	if processAlive(oldPID) {
		t.Errorf("expected old PID %d to be dead after restart", oldPID)
	}
	if !processAlive(st2.PID) {
		t.Errorf("expected new PID %d to be alive after restart", st2.PID)
	}
}

func TestDaemonRestart_ErrorsIfNoState(t *testing.T) {
	h := newDaemonHarness(t)
	env := h.env(t)
	var stdout, stderr bytes.Buffer
	code := Execute([]string{"daemon", "restart"}, nil, &stdout, &stderr, env)
	if code == 0 {
		t.Fatalf("expected nonzero exit when no session state")
	}
	if !strings.Contains(stderr.String(), "onboard") {
		t.Errorf("expected hint about 'onboard', got: %s", stderr.String())
	}
}

func TestSession_FlagSelectsExplicitScope(t *testing.T) {
	h := newDaemonHarness(t)
	env := h.env(t)
	if code := Execute([]string{"--session", "alice", "daemon", "start"}, nil, &bytes.Buffer{}, &bytes.Buffer{}, env); code != 0 {
		t.Fatal("start with --session failed")
	}
	if _, err := session.Load(h.home, "", "alice"); err != nil {
		t.Errorf("expected session file at scope=alice, got %v", err)
	}
	if _, err := session.Load(h.home, "", session.Scope(h.cwd)); err == nil {
		t.Errorf("expected NO session file at cwd-derived scope when --session is set")
	}
}

func TestSession_TwoSessionsCoexist(t *testing.T) {
	h := newDaemonHarness(t)
	env := h.env(t)
	if code := Execute([]string{"--session", "alice", "daemon", "start"}, nil, &bytes.Buffer{}, &bytes.Buffer{}, env); code != 0 {
		t.Fatal("alice start failed")
	}
	if code := Execute([]string{"--session", "bob", "daemon", "start"}, nil, &bytes.Buffer{}, &bytes.Buffer{}, env); code != 0 {
		t.Fatal("bob start failed")
	}
	if got := h.registerCalls.Load(); got != 2 {
		t.Errorf("expected 2 register calls (one per session), got %d", got)
	}
	stA, _ := session.Load(h.home, "", "alice")
	stB, _ := session.Load(h.home, "", "bob")
	if stA == nil || stB == nil {
		t.Fatalf("both session files must exist (alice=%v bob=%v)", stA, stB)
	}
	if stA.PID == stB.PID {
		t.Errorf("alice and bob should have distinct PIDs")
	}
	if !processAlive(stA.PID) || !processAlive(stB.PID) {
		t.Errorf("both bridges must be alive (alice=%d bob=%d)", stA.PID, stB.PID)
	}
}

func TestSession_EnvVarFallback(t *testing.T) {
	h := newDaemonHarness(t)
	env := h.env(t)
	env.Getenv = func(k string) string {
		if k == "JAM_SESSION" {
			return "from-env"
		}
		return ""
	}
	if code := Execute([]string{"daemon", "start"}, nil, &bytes.Buffer{}, &bytes.Buffer{}, env); code != 0 {
		t.Fatal("start with JAM_SESSION env failed")
	}
	if _, err := session.Load(h.home, "", "from-env"); err != nil {
		t.Errorf("expected session file at scope=from-env, got %v", err)
	}
}

func TestDaemonStatus_ShowsRunningAndNotRunning(t *testing.T) {
	h := newDaemonHarness(t)
	env := h.env(t)

	var notRunOut, notRunErr bytes.Buffer
	code := Execute([]string{"daemon", "status"}, nil, &notRunOut, &notRunErr, env)
	if code != 0 {
		t.Fatalf("status (not running) should exit 0, got %d\n%s", code, notRunErr.String())
	}
	if !strings.Contains(notRunOut.String(), "ot running") {
		t.Errorf("expected 'not running', got: %s", notRunOut.String())
	}

	var s1out, s1err bytes.Buffer
	if code := Execute([]string{"daemon", "start"}, nil, &s1out, &s1err, env); code != 0 {
		t.Fatalf("start failed: %s", s1err.String())
	}
	var runOut, runErr bytes.Buffer
	if code := Execute([]string{"daemon", "status"}, nil, &runOut, &runErr, env); code != 0 {
		t.Fatalf("status (running) exit %d: %s", code, runErr.String())
	}
	if !strings.Contains(runOut.String(), "ed.lepedus/test-stub") {
		t.Errorf("expected status to mention handle, got: %s", runOut.String())
	}
}

