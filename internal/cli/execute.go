package cli

import (
	"io"

	"github.com/ed-lepedus-thenvoi/jam/internal/sockpuppet"
)

// Env carries process-level inputs tests need to override: home dir, cwd,
// env-var lookup, the sockpuppet spawner factory, and the binary's version
// string (injected at build time via goreleaser ldflags).
type Env struct {
	HomeDir         string
	Cwd             string
	Getenv          func(string) string
	SpawnSockpuppet sockpuppet.Spawner
	Version         string
}

// Execute is the single entrypoint shared by main() and tests.
// Returns the process exit code so callers can os.Exit on it.
func Execute(args []string, stdin io.Reader, stdout, stderr io.Writer, env Env) int {
	root := newRootCmd(stdin, stdout, stderr, env)
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		return 1
	}
	return 0
}
