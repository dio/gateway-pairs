package simplegwp_test

import (
	"os"
	"path/filepath"

	"github.com/dio/gateway-pairs/names"
)

// pairNames returns the derived names for a pair, respecting PAIR_PREFIX (default "tr").
// Uses names.For from the root module -- no local duplication.
func pairNames(index int) names.Pair {
	pfx := os.Getenv("PAIR_PREFIX")
	if pfx == "" {
		pfx = "tr"
	}
	return names.For(pfx, index)
}

// repoRootFromGoWork resolves the gateway-pairs repo root using the GOWORK
// environment variable (always set when running inside a Go workspace).
// Falls back to searching upward from the current directory for go.work.
func repoRootFromGoWork() string {
	if gw := os.Getenv("GOWORK"); gw != "" {
		return filepath.Dir(gw)
	}
	// Fallback: walk up from cwd until go.work is found.
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	panic("could not locate repo root (go.work not found)")
}
