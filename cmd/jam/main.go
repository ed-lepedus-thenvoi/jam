package main

import (
	"os"

	"github.com/thenvoi/jam/internal/cli"
	"github.com/thenvoi/jam/internal/sockpuppet"
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
	}
	os.Exit(cli.Execute(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, env))
}
