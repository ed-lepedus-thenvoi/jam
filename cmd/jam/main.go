package main

import (
	"fmt"
	"os"

	"github.com/ed-lepedus-thenvoi/jam/internal/cli"
	"github.com/ed-lepedus-thenvoi/jam/internal/sockpuppet"
)

// Set at link time by goreleaser via -ldflags "-X main.version=... -X main.commit=... -X main.date=...".
// Defaults are for `go build` / `go install` without those flags.
var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

func main() {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = ""
	}
	env := cli.Env{
		HomeDir:         os.Getenv("HOME"),
		Cwd:             cwd,
		Getenv:          os.Getenv,
		SpawnSockpuppet: sockpuppet.DefaultSpawner,
		Version:         fmt.Sprintf("%s (commit %s, built %s)", version, commit, date),
	}
	os.Exit(cli.Execute(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, env))
}
