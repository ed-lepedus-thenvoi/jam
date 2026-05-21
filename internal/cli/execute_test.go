package cli

import (
	"bytes"
	"strings"
	"testing"
)

// Smallest sanity check that the harness wires up correctly.
// More substantive acceptance tests live in slice-specific files.
func TestExecute_HelpExitsZero(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Execute([]string{"--help"}, nil, &stdout, &stderr, Env{})
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\nstderr: %s", code, stderr.String())
	}
	if strings.TrimSpace(stdout.String()) == "" {
		t.Fatalf("expected non-empty help output, got: %q", stdout.String())
	}
}
