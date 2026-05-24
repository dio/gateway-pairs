package names_test

import (
	"testing"

	"github.com/dio/gateway-pairs/names"
)

func TestFor_defaults(t *testing.T) {
	p := names.For("tr", 1)
	if p.SystemNS != "tr-system-1" {
		t.Errorf("SystemNS = %q, want tr-system-1", p.SystemNS)
	}
	if p.DataplaneNS != "tr-dataplane-1" {
		t.Errorf("DataplaneNS = %q, want tr-dataplane-1", p.DataplaneNS)
	}
	if p.GatewayClass != "tr-1" {
		t.Errorf("GatewayClass = %q, want tr-1", p.GatewayClass)
	}
	if p.ControllerName != "gateway.envoyproxy.io/tr-1" {
		t.Errorf("ControllerName = %q, want gateway.envoyproxy.io/tr-1", p.ControllerName)
	}
	if p.ReleaseName != "eg-pair-1" {
		t.Errorf("ReleaseName = %q, want eg-pair-1", p.ReleaseName)
	}
}

func TestFor_emptyPrefix(t *testing.T) {
	p := names.For("", 1)
	if p.SystemNS != "system-1" {
		t.Errorf("SystemNS = %q, want system-1", p.SystemNS)
	}
	if p.DataplaneNS != "dataplane-1" {
		t.Errorf("DataplaneNS = %q, want dataplane-1", p.DataplaneNS)
	}
	if p.GatewayClass != "1" {
		t.Errorf("GatewayClass = %q, want 1", p.GatewayClass)
	}
	if p.ControllerName != "gateway.envoyproxy.io/1" {
		t.Errorf("ControllerName = %q, want gateway.envoyproxy.io/1", p.ControllerName)
	}
}

func TestFor_customPrefix(t *testing.T) {
	p := names.For("myapp", 3)
	if p.SystemNS != "myapp-system-3" {
		t.Errorf("SystemNS = %q, want myapp-system-3", p.SystemNS)
	}
	if p.DataplaneNS != "myapp-dataplane-3" {
		t.Errorf("DataplaneNS = %q, want myapp-dataplane-3", p.DataplaneNS)
	}
	if p.GatewayClass != "myapp-3" {
		t.Errorf("GatewayClass = %q, want myapp-3", p.GatewayClass)
	}
}

func TestWatchNamespaces(t *testing.T) {
	p := names.For("tr", 2)
	ns := p.WatchNamespaces()
	if len(ns) != 2 || ns[0] != "tr-system-2" || ns[1] != "tr-dataplane-2" {
		t.Errorf("WatchNamespaces = %v, want [tr-system-2 tr-dataplane-2]", ns)
	}
}

func TestClusterRoleNames(t *testing.T) {
	p := names.For("tr", 1)
	got := p.ClusterRoleNames()
	want := []string{"eg-pair-1-tokenreviews", "eg-pair-1-gateway-controller"}
	if len(got) != len(want) {
		t.Fatalf("ClusterRoleNames len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ClusterRoleNames[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
