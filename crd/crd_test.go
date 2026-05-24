package crd_test

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/dio/gateway-pairs/crd"
	"github.com/dio/gateway-pairs/internal/fake"
)

func TestDetect_notInstalled(t *testing.T) {
	k := &fake.Kubectl{} // empty = all outputs ""
	r, err := crd.Detect(context.Background(), k)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if r.GatewayAPI.State != crd.NotInstalled {
		t.Errorf("GatewayAPI.State = %v, want NotInstalled", r.GatewayAPI.State)
	}
	if r.EG.State != crd.NotInstalled {
		t.Errorf("EG.State = %v, want NotInstalled", r.EG.State)
	}
}

func TestDetect_selfManaged(t *testing.T) {
	k := &fake.Kubectl{
		Responses: map[string]string{
			"bundle-version": "v1.5.1",
			"channel":        "standard",
			"managedFields":  "helm",
			"envoyproxies":   "v1.8.0",
		},
	}
	r, err := crd.Detect(context.Background(), k)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if r.GatewayAPI.State != crd.SelfManaged {
		t.Errorf("GatewayAPI.State = %v, want SelfManaged", r.GatewayAPI.State)
	}
	if r.GatewayAPI.BundleVersion != "v1.5.1" {
		t.Errorf("BundleVersion = %q, want v1.5.1", r.GatewayAPI.BundleVersion)
	}
	if r.EG.State != crd.SelfManaged {
		t.Errorf("EG.State = %v, want SelfManaged", r.EG.State)
	}
	if r.EG.Version != "v1.8.0" {
		t.Errorf("EG.Version = %q, want v1.8.0", r.EG.Version)
	}
}

func TestDetect_providerManaged(t *testing.T) {
	k := &fake.Kubectl{
		Responses: map[string]string{
			"bundle-version": "v1.2.0",
			"channel":        "standard",
			"managedFields":  "gke-networking-controller",
		},
	}
	r, err := crd.Detect(context.Background(), k)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if r.GatewayAPI.State != crd.ProviderManaged {
		t.Errorf("GatewayAPI.State = %v, want ProviderManaged", r.GatewayAPI.State)
	}
	if r.GatewayAPI.ProviderManager != "gke-networking-controller" {
		t.Errorf("ProviderManager = %q, want gke-networking-controller", r.GatewayAPI.ProviderManager)
	}
}

func TestDetect_aksProviderManaged(t *testing.T) {
	k := &fake.Kubectl{
		Responses: map[string]string{
			"bundle-version": "v1.3.0",
			"channel":        "standard",
			"managedFields":  "aks-gateway-api-controller",
		},
	}
	r, err := crd.Detect(context.Background(), k)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if r.GatewayAPI.State != crd.ProviderManaged {
		t.Errorf("GatewayAPI.State = %v, want ProviderManaged", r.GatewayAPI.State)
	}
}

func TestInstall_skipGatewayAPI(t *testing.T) {
	k := &fake.Kubectl{}
	detected := crd.DetectResult{
		GatewayAPI: crd.GatewayAPIInfo{State: crd.NotInstalled},
		EG:         crd.EGInfo{State: crd.NotInstalled},
	}
	var out strings.Builder
	_ = crd.Install(context.Background(), k, detected, crd.InstallOptions{
		SkipGatewayAPI: true,
		Out:            &out,
	})
	// EG install will fail because embedded CRDs aren't present in test env,
	// so we only check that gateway-api skip was printed.
	output := out.String()
	if !strings.Contains(output, "skipped") {
		t.Errorf("expected 'skipped' in output, got: %s", output)
	}
}

func TestInstall_alreadyInstalled_noForce(t *testing.T) {
	k := &fake.Kubectl{}
	detected := crd.DetectResult{
		GatewayAPI: crd.GatewayAPIInfo{State: crd.SelfManaged, BundleVersion: "v1.5.1", Channel: "standard"},
		EG:         crd.EGInfo{State: crd.SelfManaged, Version: "v1.8.0"},
	}
	var out strings.Builder
	err := crd.Install(context.Background(), k, detected, crd.InstallOptions{Out: &out})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	output := out.String()
	if !strings.Contains(output, "already installed") {
		t.Errorf("expected 'already installed' in output, got: %s", output)
	}
	// kubectl should NOT have been called (no RunWithStdin)
	if len(k.Calls) != 0 {
		t.Errorf("expected no kubectl calls, got: %v", k.Calls)
	}
}

func TestInstall_providerManaged_skipped(t *testing.T) {
	k := &fake.Kubectl{}
	detected := crd.DetectResult{
		GatewayAPI: crd.GatewayAPIInfo{
			State:           crd.ProviderManaged,
			BundleVersion:   "v1.2.0",
			Channel:         "standard",
			ProviderManager: "gke-networking-controller",
		},
		EG: crd.EGInfo{State: crd.SelfManaged, Version: "v1.8.0"},
	}
	var out strings.Builder
	err := crd.Install(context.Background(), k, detected, crd.InstallOptions{Out: &out})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	output := out.String()
	if !strings.Contains(output, "provider-managed") {
		t.Errorf("expected 'provider-managed' in output, got: %s", output)
	}
}

// stubWriter discards writes but records them were attempted.
type stubWriter struct{ n int }

func (s *stubWriter) Write(p []byte) (int, error) { s.n += len(p); return len(p), nil }
var _ io.Writer = (*stubWriter)(nil)
