package pair_test

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/dio/gateway-pairs/internal/fake"
	"github.com/dio/gateway-pairs/pair"
)

func TestVerify_healthy(t *testing.T) {
	k := &fake.Kubectl{Responses: map[string]string{
		"availableReplicas": "1/1",
		"gatewayclass":      "Accepted=True ",
	}}
	r, err := pair.Verify(context.Background(), k, 1, pair.VerifyOptions{
		Prefix: "tr",
		Out:    io.Discard,
	})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !r.Healthy {
		t.Errorf("expected healthy, checks: %+v", r.Checks)
	}
}

func TestVerify_controllerDown(t *testing.T) {
	k := &fake.Kubectl{Responses: map[string]string{
		// availableReplicas absent -- controller returns "" (down)
		"gatewayclass": "Accepted=True ",
	}}
	r, err := pair.Verify(context.Background(), k, 1, pair.VerifyOptions{
		Prefix: "tr",
		Out:    io.Discard,
	})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if r.Healthy {
		t.Error("expected unhealthy when controller down")
	}
	found := false
	for _, c := range r.Checks {
		if strings.Contains(c.Name, "controller") && !c.OK {
			found = true
		}
	}
	if !found {
		t.Errorf("expected controller-available check to fail, got: %+v", r.Checks)
	}
}

func TestVerify_gatewayClassPending(t *testing.T) {
	k := &fake.Kubectl{Responses: map[string]string{
		"availableReplicas": "1/1",
		// "gatewayclass" absent -- GatewayClass condition query returns ""
	}}
	r, err := pair.Verify(context.Background(), k, 1, pair.VerifyOptions{
		Prefix: "tr",
		Out:    io.Discard,
	})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if r.Healthy {
		t.Error("expected unhealthy when GatewayClass not accepted")
	}
}
