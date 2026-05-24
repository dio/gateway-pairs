package fake_test

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/dio/gateway-pairs/internal/fake"
	"github.com/dio/gateway-pairs/internal/helm"
)

func TestKubectl_Output_scripted(t *testing.T) {
	k := &fake.Kubectl{
		Responses: map[string]string{
			"availableReplicas": "1",
		},
	}
	out, err := k.Output(context.Background(), "get", "deployment", "-o", "jsonpath={.status.availableReplicas}")
	if err != nil {
		t.Fatalf("Output: %v", err)
	}
	if out != "1" {
		t.Errorf("Output = %q, want 1", out)
	}
}

func TestKubectl_Output_noMatch(t *testing.T) {
	k := &fake.Kubectl{}
	out, err := k.Output(context.Background(), "get", "foo")
	if err != nil {
		t.Fatalf("Output: %v", err)
	}
	if out != "" {
		t.Errorf("Output = %q, want empty", out)
	}
}

func TestKubectl_Run_recordsCall(t *testing.T) {
	k := &fake.Kubectl{}
	err := k.Run(context.Background(), io.Discard, io.Discard, "apply", "-f", "-")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(k.Calls) != 1 {
		t.Fatalf("Calls len = %d, want 1", len(k.Calls))
	}
	if !strings.Contains(strings.Join(k.Calls[0], " "), "apply") {
		t.Errorf("expected 'apply' in call, got %v", k.Calls[0])
	}
}

func TestHelm_List_empty(t *testing.T) {
	h := &fake.Helm{}
	rels, err := h.List(context.Background(), "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("List len = %d, want 0", len(rels))
	}
}

func TestHelm_List_scripted(t *testing.T) {
	h := &fake.Helm{
		Releases: map[string][]helm.Release{
			"eg-pair": {
				{Name: "eg-pair-1", Namespace: "tr-system-1", Status: "deployed"},
				{Name: "eg-pair-2", Namespace: "tr-system-2", Status: "deployed"},
			},
		},
	}
	rels, err := h.List(context.Background(), "eg-pair")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rels) != 2 {
		t.Fatalf("List len = %d, want 2", len(rels))
	}
	if rels[0].Name != "eg-pair-1" {
		t.Errorf("rels[0].Name = %q, want eg-pair-1", rels[0].Name)
	}
}

func TestHelm_Run_recordsError(t *testing.T) {
	h := &fake.Helm{RunErr: io.ErrUnexpectedEOF}
	err := h.Run(context.Background(), io.Discard, io.Discard, "upgrade", "--install")
	if err != io.ErrUnexpectedEOF {
		t.Errorf("Run err = %v, want ErrUnexpectedEOF", err)
	}
}
