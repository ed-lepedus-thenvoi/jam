package cli

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ed-lepedus-thenvoi/jam/internal/session"
)

type pruneHarness struct {
	home        string
	srv         *httptest.Server
	deleteCalls atomic.Int32
	deletedIDs  chan string
}

func newPruneHarness(t *testing.T) *pruneHarness {
	t.Helper()
	h := &pruneHarness{
		home:       t.TempDir(),
		deletedIDs: make(chan string, 16),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/me/agents/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "DELETE" {
			w.WriteHeader(405)
			return
		}
		// Extract id from path
		id := strings.TrimPrefix(r.URL.Path, "/api/v1/me/agents/")
		h.deleteCalls.Add(1)
		select {
		case h.deletedIDs <- id:
		default:
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"id":"` + id + `","executions_deleted":0}}`))
	})
	h.srv = httptest.NewServer(mux)
	t.Cleanup(h.srv.Close)
	writeConfig(t, h.home, h.srv.URL, "band_u_test")
	return h
}

func (h *pruneHarness) saveSession(t *testing.T, scope string, pid int, agentID string) {
	t.Helper()
	if err := session.Save(h.home, "", &session.State{
		Scope:       scope,
		Cwd:         "/fake/" + scope,
		AgentID:     agentID,
		AgentName:   "n-" + agentID,
		AgentAPIKey: "band_a_" + agentID,
		Handle:      "alice/" + scope,
		PID:         pid,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPrune_RemovesDeadSessionsAndKeepsAlive(t *testing.T) {
	h := newPruneHarness(t)
	// Dead PID: 0 is never alive (kill(0,0) returns ESRCH).
	h.saveSession(t, "dead-1", 0, "agent-dead-1")
	// Alive PID: this test process itself.
	h.saveSession(t, "alive-1", os.Getpid(), "agent-alive-1")

	var stdout, stderr bytes.Buffer
	code := Execute([]string{"agent", "prune"}, nil, &stdout, &stderr,
		Env{HomeDir: h.home, Getenv: func(string) string { return "" }})
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, stderr.String())
	}
	if got := h.deleteCalls.Load(); got != 1 {
		t.Errorf("expected 1 DELETE, got %d", got)
	}
	// Dead state file should be gone; alive should remain.
	if _, err := session.Load(h.home, "", "dead-1"); err == nil {
		t.Errorf("expected dead-1 state file removed")
	}
	if _, err := session.Load(h.home, "", "alive-1"); err != nil {
		t.Errorf("expected alive-1 state preserved, got %v", err)
	}
	if !strings.Contains(stdout.String(), "agent-dead-1") {
		t.Errorf("expected output to name the pruned agent, got: %s", stdout.String())
	}
}

func TestPrune_DryRunDoesNotMutate(t *testing.T) {
	h := newPruneHarness(t)
	h.saveSession(t, "dead-2", 0, "agent-dead-2")

	var stdout, stderr bytes.Buffer
	code := Execute([]string{"agent", "prune", "--dry-run"}, nil, &stdout, &stderr,
		Env{HomeDir: h.home, Getenv: func(string) string { return "" }})
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, stderr.String())
	}
	if got := h.deleteCalls.Load(); got != 0 {
		t.Errorf("expected 0 DELETE in dry-run, got %d", got)
	}
	if _, err := session.Load(h.home, "", "dead-2"); err != nil {
		t.Errorf("dry-run should preserve state file, got err=%v", err)
	}
	if !strings.Contains(stdout.String(), "would prune") {
		t.Errorf("expected 'would prune' in dry-run output, got: %s", stdout.String())
	}
}

func TestPrune_AcrossProfiles(t *testing.T) {
	h := newPruneHarness(t)
	// Default profile session is dead
	h.saveSession(t, "dead-default", 0, "agent-default")
	// Staging profile config + dead session
	writeProfileConfig(t, h.home, "staging", h.srv.URL, "band_u_test")
	if err := session.Save(h.home, "staging", &session.State{
		Scope: "dead-staging", Profile: "staging", AgentID: "agent-staging",
		AgentName: "x", AgentAPIKey: "band_a_x", Handle: "alice/x", PID: 0,
	}); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := Execute([]string{"agent", "prune"}, nil, &stdout, &stderr,
		Env{HomeDir: h.home, Getenv: func(string) string { return "" }})
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, stderr.String())
	}
	if got := h.deleteCalls.Load(); got != 2 {
		t.Errorf("expected 2 DELETEs (one per profile), got %d", got)
	}
}

