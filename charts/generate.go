// Package charts embeds Helm chart assets. Run "go generate ./charts/..." to
// regenerate after bumping EG_VERSION in the Makefile.
// Requires: helm CLI in PATH and OCI registry access.
//
//go:generate make -C .. generate-assets

package charts
