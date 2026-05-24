// Package fake provides in-process kubectl/helm fakes for unit testing.
package fake

import (
	"context"
	"io"
	"strings"

	"github.com/dio/gateway-pairs/internal/helm"
)

// Kubectl records calls and returns scripted responses.
// Map key is a substring that must appear in the joined args.
type Kubectl struct {
	Responses map[string]string // substring -> output
	Errors    map[string]error  // substring -> error
	Calls     [][]string        // recorded arg slices
}

func (f *Kubectl) Output(_ context.Context, args ...string) (string, error) {
	f.Calls = append(f.Calls, args)
	key := strings.Join(args, " ")
	for sub, out := range f.Responses {
		if strings.Contains(key, sub) {
			return out, nil
		}
	}
	for sub, err := range f.Errors {
		if strings.Contains(key, sub) {
			return "", err
		}
	}
	return "", nil
}

func (f *Kubectl) Run(_ context.Context, _, _ io.Writer, args ...string) error {
	f.Calls = append(f.Calls, args)
	return nil
}

func (f *Kubectl) RunWithStdin(_ context.Context, _ io.Reader, _, _ io.Writer, args ...string) error {
	f.Calls = append(f.Calls, args)
	return nil
}

// Helm records calls and returns scripted responses.
type Helm struct {
	Releases map[string][]helm.Release // filter regex -> release list
	RunErr   error
	Calls    [][]string
}

func (f *Helm) Run(_ context.Context, _, _ io.Writer, args ...string) error {
	f.Calls = append(f.Calls, args)
	return f.RunErr
}

func (f *Helm) Output(_ context.Context, args ...string) (string, error) {
	f.Calls = append(f.Calls, args)
	return "", nil
}

func (f *Helm) List(_ context.Context, filterRegex string) ([]helm.Release, error) {
	f.Calls = append(f.Calls, []string{"list", filterRegex})
	if f.Releases == nil {
		return nil, nil
	}
	// return the first matching key (substring match for simplicity in tests)
	for key, rels := range f.Releases {
		if strings.Contains(filterRegex, key) || strings.Contains(key, filterRegex) || filterRegex == "" {
			return rels, nil
		}
	}
	return nil, nil
}
