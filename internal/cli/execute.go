package cli

import (
	"io"

	"github.com/thenvoi/jam/internal/sockpuppet"
)

// Env carries process-level inputs tests need to override: home dir, cwd,
// env-var lookup, and the sockpuppet spawner factory.
type Env struct {
	HomeDir         string
	Cwd             string
	Getenv          func(string) string
	SpawnSockpuppet sockpuppet.Spawner
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
