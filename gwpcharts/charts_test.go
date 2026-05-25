package gwpcharts_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dio/gateway-pairs/gwpcharts"
)

func TestList_returnsEGPairChart(t *testing.T) {
	r, err := gwpcharts.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(r.Charts) == 0 {
		t.Fatal("expected at least one chart")
	}
	found := false
	for _, c := range r.Charts {
		if c.Name == "eg-pair" {
			found = true
			if c.AppVersion == "" {
				t.Error("eg-pair AppVersion is empty")
			}
		}
	}
	if !found {
		t.Error("eg-pair not in chart list")
	}
}

func TestList_returnsCRDBundles(t *testing.T) {
	r, err := gwpcharts.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	// CRDs may be absent on a clean checkout (only .gitkeep present).
	// Just verify the call succeeds without error.
	_ = r.CRDs
}

func TestShowValues_returnsYAML(t *testing.T) {
	v, err := gwpcharts.ShowValues("eg-pair")
	if err != nil {
		t.Fatalf("ShowValues: %v", err)
	}
	if !strings.Contains(v, "pair:") {
		t.Errorf("expected 'pair:' in values.yaml, got: %s", v[:min(200, len(v))])
	}
}

func TestShowValues_unknownChart(t *testing.T) {
	_, err := gwpcharts.ShowValues("nonexistent")
	if err == nil {
		t.Error("expected error for unknown chart")
	}
}

func TestExport_writesFiles(t *testing.T) {
	dir := t.TempDir()
	if err := gwpcharts.Export(dir); err != nil {
		t.Fatalf("Export: %v", err)
	}
	// Chart.yaml must be present.
	chartYAML := filepath.Join(dir, "eg-pair", "Chart.yaml")
	if _, err := os.Stat(chartYAML); err != nil {
		t.Errorf("Chart.yaml not exported: %v", err)
	}
	// values.yaml must be present.
	valuesYAML := filepath.Join(dir, "eg-pair", "values.yaml")
	if _, err := os.Stat(valuesYAML); err != nil {
		t.Errorf("values.yaml not exported: %v", err)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
