package preflight_test

import (
	"context"
	"fmt"
	"io"
	"testing"

	"github.com/dio/gateway-pairs/internal/fake"
	"github.com/dio/gateway-pairs/preflight"
)

func newFakeKube(responses map[string]string) *fake.Kubectl {
	return &fake.Kubectl{Responses: responses}
}

func TestRun_allOK_freshCluster(t *testing.T) {
	k := newFakeKube(map[string]string{
		// check 1: context is k3d
		"current-context": "k3d-gw-test",
		// check 2: server reachable
		"version": `{"serverVersion":{"gitVersion":"v1.32.2"}}`,
		// check 3: rbac -- "yes" for can-i queries
		"can-i": "yes",
		// checks 4-5: CRDs not installed (empty = not found)
	})
	r, err := preflight.Run(context.Background(), k, preflight.Options{
		Prefix: "tr",
		Out:    io.Discard,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if r.Failures > 0 {
		for _, c := range r.Checks {
			if c.Status == preflight.StatusFail {
				t.Errorf("unexpected failure: %s -- %s", c.Name, c.Message)
			}
		}
	}
}

func TestRun_serverUnreachable(t *testing.T) {
	k := newFakeKube(map[string]string{
		"current-context": "k3d-gw-test",
		// "version" absent -- server unreachable
	})
	r, err := preflight.Run(context.Background(), k, preflight.Options{
		Prefix: "tr",
		Out:    io.Discard,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if r.Failures == 0 {
		t.Error("expected failure when server unreachable")
	}
	found := false
	for _, c := range r.Checks {
		if c.Name == "server-reachable" && c.Status == preflight.StatusFail {
			found = true
		}
	}
	if !found {
		t.Errorf("expected server-reachable check to fail, got: %+v", r.Checks)
	}
}

func TestRun_rbacForbidden(t *testing.T) {
	k := newFakeKube(map[string]string{
		"current-context": "k3d-gw-test",
		"version":         `{"serverVersion":{"gitVersion":"v1.32.2"}}`,
		// "can-i" absent -- returns "" which is not "yes"
	})
	r, err := preflight.Run(context.Background(), k, preflight.Options{
		Prefix: "tr",
		Out:    io.Discard,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if r.Failures == 0 {
		t.Error("expected failure when RBAC denied")
	}
}

func TestRun_gatewayClassConflict(t *testing.T) {
	k := newFakeKube(map[string]string{
		"current-context": "k3d-gw-test",
		"version":         `{"serverVersion":{"gitVersion":"v1.32.2"}}`,
		"can-i":           "yes",
		// GatewayClass tr-1 exists
		"gatewayclass tr-1": "Helm",
	})
	r, err := preflight.Run(context.Background(), k, preflight.Options{
		Prefix:    "tr",
		PairIndex: 1,
		Out:       io.Discard,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	found := false
	for _, c := range r.Checks {
		if c.Name == "gatewayclass-conflict" && c.Status == preflight.StatusFail {
			found = true
		}
	}
	if !found {
		t.Errorf("expected gatewayclass-conflict failure, checks: %+v", r.Checks)
	}
}

func TestRun_controllerNameConflict(t *testing.T) {
	k := newFakeKube(map[string]string{
		"current-context": "k3d-gw-test",
		"version":         `{"serverVersion":{"gitVersion":"v1.32.2"}}`,
		"can-i":           "yes",
		// controllerName already in use in another ConfigMap
		"envoy-gateway-config": fmt.Sprintf("controllerName: gateway.envoyproxy.io/tr-1"),
	})
	r, err := preflight.Run(context.Background(), k, preflight.Options{
		Prefix:    "tr",
		PairIndex: 1,
		Out:       io.Discard,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	found := false
	for _, c := range r.Checks {
		if c.Name == "controller-name-conflict" && c.Status == preflight.StatusFail {
			found = true
		}
	}
	if !found {
		t.Errorf("expected controller-name-conflict failure, checks: %+v", r.Checks)
	}
}

func TestRun_nonK3dContext_warnsWithUnsafe(t *testing.T) {
	k := newFakeKube(map[string]string{
		"current-context": "my-prod-cluster",
		"version":         `{"serverVersion":{"gitVersion":"v1.32.2"}}`,
		"can-i":           "yes",
	})
	r, err := preflight.Run(context.Background(), k, preflight.Options{
		Prefix:        "tr",
		UnsafeContext: true,
		Out:           io.Discard,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if r.Failures > 0 {
		t.Errorf("expected no failures with --unsafe-context, got: %+v", r.Checks)
	}
	found := false
	for _, c := range r.Checks {
		if c.Name == "context-safety" && c.Status == preflight.StatusWarn {
			found = true
		}
	}
	if !found {
		t.Errorf("expected context-safety warn, got: %+v", r.Checks)
	}
}

func TestRun_nonK3dContext_blocksWithoutUnsafe(t *testing.T) {
	k := newFakeKube(map[string]string{
		"current-context": "my-prod-cluster",
	})
	r, err := preflight.Run(context.Background(), k, preflight.Options{
		Prefix: "tr",
		Out:    io.Discard,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if r.Failures == 0 {
		t.Error("expected failure for non-k3d context without --unsafe-context")
	}
	// Should stop after check 1.
	if len(r.Checks) != 1 {
		t.Errorf("expected 1 check (stopped early), got %d", len(r.Checks))
	}
}
