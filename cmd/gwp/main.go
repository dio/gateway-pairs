package main

import (
	"fmt"
	"os"

	"github.com/dio/gateway-pairs/internal/cli"
)

// Baked in by goreleaser ldflags.
var (
	version   = "dev"
	egVersion = "v1.8.0"
	commit    = "none"
	date      = "unknown"
)

func main() {
	if err := cli.Execute(cli.BuildInfo{
		Version:   version,
		EGVersion: egVersion,
		Commit:    commit,
		Date:      date,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
