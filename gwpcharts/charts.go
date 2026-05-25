// Package gwpcharts provides introspection and export of the embedded Helm charts.
// It reads directly from the charts.FS() embedded filesystem -- no Helm exec needed.
package gwpcharts

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/dio/gateway-pairs/charts"
	"gopkg.in/yaml.v3"
)

// ChartInfo describes one embedded Helm chart.
type ChartInfo struct {
	Name       string `json:"name"`
	Version    string `json:"version"`
	AppVersion string `json:"appVersion"`
}

// CRDBundleInfo describes one embedded pre-rendered CRD bundle.
type CRDBundleInfo struct {
	Name    string `json:"name"`
	Channel string `json:"channel,omitempty"`
}

// ListResult is the output of List.
type ListResult struct {
	Charts []ChartInfo    `json:"charts"`
	CRDs   []CRDBundleInfo `json:"crds"`
}

// List returns metadata about all embedded charts and CRD bundles.
func List() (*ListResult, error) {
	res := &ListResult{}

	// Read chart metadata from Chart.yaml.
	for _, name := range []string{"eg-pair"} {
		info, err := readChartInfo(name)
		if err != nil {
			return nil, fmt.Errorf("reading Chart.yaml for %s: %w", name, err)
		}
		res.Charts = append(res.Charts, *info)
	}

	// List embedded CRD files.
	crdFS := charts.CRDs()
	entries, err := fs.ReadDir(crdFS, ".")
	if err != nil {
		return nil, fmt.Errorf("reading embedded CRDs: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") || e.Name() == ".gitkeep" {
			continue
		}
		bundle := CRDBundleInfo{Name: strings.TrimSuffix(e.Name(), ".yaml")}
		if strings.Contains(e.Name(), "standard") {
			bundle.Channel = "standard"
		} else if strings.Contains(e.Name(), "experimental") {
			bundle.Channel = "experimental"
		}
		res.CRDs = append(res.CRDs, bundle)
	}
	return res, nil
}

// Export extracts the eg-pair chart tree to dir. Creates dir if it does not exist.
// This is the same extraction used by pair.Install but writes to a user-specified
// directory rather than a temp dir.
func Export(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating output dir %s: %w", dir, err)
	}
	return fs.WalkDir(charts.FS(), "eg-pair", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(dir, path)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := fs.ReadFile(charts.FS(), path)
		if err != nil {
			return fmt.Errorf("reading %s: %w", path, err)
		}
		return os.WriteFile(target, data, 0o644)
	})
}

// ShowValues returns the default values.yaml content for chart name.
// Currently only "eg-pair" is supported.
func ShowValues(name string) (string, error) {
	path := name + "/values.yaml"
	data, err := fs.ReadFile(charts.FS(), path)
	if err != nil {
		return "", fmt.Errorf("chart %q not found in embedded assets", name)
	}
	return string(data), nil
}

// Print writes a human-readable chart listing to w.
func Print(w io.Writer, r *ListResult) {
	fmt.Fprintf(w, "%-20s %-12s %s\n", "CHART", "VERSION", "APP-VERSION")
	for _, c := range r.Charts {
		fmt.Fprintf(w, "%-20s %-12s %s\n", c.Name, c.Version, c.AppVersion)
	}
	if len(r.CRDs) > 0 {
		fmt.Fprintln(w, "\nEmbedded CRD bundles:")
		for _, b := range r.CRDs {
			if b.Channel != "" {
				fmt.Fprintf(w, "  %s (%s)\n", b.Name, b.Channel)
			} else {
				fmt.Fprintf(w, "  %s\n", b.Name)
			}
		}
	}
}

// chartYAML is the minimal subset of Chart.yaml we need.
type chartYAML struct {
	Name       string `yaml:"name"`
	Version    string `yaml:"version"`
	AppVersion string `yaml:"appVersion"`
}

func readChartInfo(name string) (*ChartInfo, error) {
	data, err := fs.ReadFile(charts.FS(), name+"/Chart.yaml")
	if err != nil {
		return nil, err
	}
	var cy chartYAML
	if err := yaml.Unmarshal(data, &cy); err != nil {
		return nil, fmt.Errorf("parsing Chart.yaml: %w", err)
	}
	return &ChartInfo{
		Name:       cy.Name,
		Version:    cy.Version,
		AppVersion: strings.Trim(cy.AppVersion, `"`),
	}, nil
}
