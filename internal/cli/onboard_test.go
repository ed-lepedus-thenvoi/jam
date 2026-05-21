package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOnboard_StartsDaemonAndPrintsOrientation(t *testing.T) {
	h := newDaemonHarness(t)
	env := h.env(t)

	var stdout, stderr bytes.Buffer
	code := Execute([]string{"onboard"}, nil, &stdout, &stderr, env)
	if code != 0 {
		t.Fatalf("exit %d\nstderr: %s", code, stderr.String())
	}
	if got := h.registerCalls.Load(); got != 1 {
		t.Errorf("expected 1 register call, got %d", got)
	}

	out := stdout.String()
	wants := []string{
		"ed.lepedus/test-stub",  // your handle
		"teammate-message",       // explains how inbound arrives
		"jam daemon stop",        // command crib
	}
	for _, want := range wants {
		if !strings.Contains(out, want) {
			t.Errorf("orientation missing %q\ngot:\n%s", want, out)
		}
	}
}

func TestOnboard_IsIdempotentWhenAlreadyRunning(t *testing.T) {
	h := newDaemonHarness(t)
	env := h.env(t)

	var pre1, pre2 bytes.Buffer
	if code := Execute([]string{"daemon", "start"}, nil, &pre1, &pre2, env); code != 0 {
		t.Fatalf("pre-start failed: %s", pre2.String())
	}

	var stdout, stderr bytes.Buffer
	if code := Execute([]string{"onboard"}, nil, &stdout, &stderr, env); code != 0 {
		t.Fatalf("onboard exit %d\nstderr: %s", code, stderr.String())
	}
	if got := h.registerCalls.Load(); got != 1 {
		t.Errorf("expected no re-registration, got %d total register calls", got)
	}
	if !strings.Contains(stdout.String(), "ed.lepedus/test-stub") {
		t.Errorf("orientation should still print when already running; got: %s", stdout.String())
	}
}

func TestOnboard_WarnsWhenTeamConfigMissing(t *testing.T) {
	h := newDaemonHarness(t)
	env := h.env(t)
	// Pre-condition: no ~/.claude/teams/<team>/config.json exists (the team
	// was never TeamCreate'd from a CC session). Onboard should still succeed
	// but emit a clear warning that inbound delivery is broken.
	var stdout, stderr bytes.Buffer
	code := Execute([]string{"onboard", "--team", "lonely-team"}, nil, &stdout, &stderr, env)
	if code != 0 {
		t.Fatalf("exit %d\nstderr: %s", code, stderr.String())
	}
	for _, want := range []string{"lonely-team", "TeamCreate", "<teammate-message>"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("expected stderr warning to mention %q\ngot:\n%s", want, stderr.String())
		}
	}
}

func TestOnboard_NoWarningWhenTeamConfigExists(t *testing.T) {
	h := newDaemonHarness(t)
	env := h.env(t)
	// Pre-create the team config.json — simulates the case where a CC session
	// has already run TeamCreate for this team.
	teamDir := filepath.Join(h.home, ".claude", "teams", "wired-team")
	if err := os.MkdirAll(teamDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "config.json"), []byte(`{"team_name":"wired-team"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := Execute([]string{"onboard", "--team", "wired-team"}, nil, &stdout, &stderr, env)
	if code != 0 {
		t.Fatalf("exit %d\nstderr: %s", code, stderr.String())
	}
	if strings.Contains(stderr.String(), "TeamCreate") {
		t.Errorf("expected NO warning when config exists; got: %s", stderr.String())
	}
}

func TestOnboard_NoConfigErrors(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := Execute([]string{"onboard"}, nil, &stdout, &stderr, Env{HomeDir: home, Cwd: cwd})
	if code == 0 {
		t.Fatalf("expected nonzero exit without config")
	}
	if !strings.Contains(stderr.String(), "jam init") {
		t.Errorf("expected stderr to hint at jam init, got: %s", stderr.String())
	}
}
