// Package cli wires the gwp cobra command tree.
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	// Import charts to ensure embedded assets are linked into the binary.
	_ "github.com/dio/gateway-pairs/charts"

	"github.com/dio/gateway-pairs/crd"
	"github.com/dio/gateway-pairs/gwpapi"
	"github.com/dio/gateway-pairs/internal/kube"
	"github.com/dio/gateway-pairs/names"
	"github.com/dio/gateway-pairs/pair"
)

// BuildInfo carries version metadata baked in at link time.
type BuildInfo struct {
	Version   string
	EGVersion string
	Commit    string
	Date      string
}

// global flags
var (
	globalContext    string
	globalKubeconfig string
	globalPrefix     string
	globalSuffix     string
	globalOutput     string
)

// Execute builds and runs the root command.
func Execute(info BuildInfo) error {
	root := &cobra.Command{
		Use:           "gwp",
		Short:         "Manage Envoy Gateway controller+dataplane pairs",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.PersistentFlags().StringVar(&globalContext, "context", "",
		"kubeconfig context (default: current-context)")
	root.PersistentFlags().StringVar(&globalKubeconfig, "kubeconfig", "",
		"path to kubeconfig file (default: ~/.kube/config)")
	root.PersistentFlags().StringVar(&globalPrefix, "prefix", "tr",
		`name prefix for all derived resource names (e.g. "tr" → tr-system-1, tr-1)`)
	root.PersistentFlags().StringVar(&globalSuffix, "suffix", "",
		`string suffix override (e.g. "prod" → tr-system-prod, GatewayClass tr-prod). `+
			`When set, replaces the numeric index in all names; use --suffix instead of an index.`)
	root.PersistentFlags().StringVarP(&globalOutput, "output", "o", "text",
		"output format: text or json")

	root.AddCommand(
		newVersionCmd(info),
		newCRDsCmd(),
		newPairCmd(info),
	)

	return root.Execute()
}

func apiClient() *gwpapi.Client {
	return gwpapi.New(gwpapi.Options{
		KubeContext: globalContext,
		Kubeconfig:  globalKubeconfig,
		Prefix:      globalPrefix,
		Suffix:      globalSuffix,
	})
}

// emit writes v as JSON to w if --output=json, otherwise calls textFn.
// textFn receives w so it can write directly.
func emit(w io.Writer, v any, textFn func(io.Writer)) error {
	if globalOutput == "json" {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	}
	textFn(w)
	return nil
}

// ── version ───────────────────────────────────────────────────────────────────

func newVersionCmd(info BuildInfo) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print gwp version and bundled component versions",
		RunE: func(cmd *cobra.Command, _ []string) error {
			type versionOutput struct {
				Version   string `json:"version"`
				EGVersion string `json:"egVersion"`
				Commit    string `json:"commit"`
				Date      string `json:"date"`
			}
			v := versionOutput{info.Version, info.EGVersion, info.Commit, info.Date}
			return emit(cmd.OutOrStdout(), v, func(w io.Writer) {
				fmt.Fprintf(w, "gwp %s\n", v.Version)
				fmt.Fprintf(w, "  eg-version: %s\n", v.EGVersion)
				fmt.Fprintf(w, "  commit:     %s\n", v.Commit)
				fmt.Fprintf(w, "  built:      %s\n", v.Date)
			})
		},
	}
}

// ── crds ──────────────────────────────────────────────────────────────────────

func newCRDsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "crds",
		Short: "Manage Gateway API and Envoy Gateway CRDs",
	}
	cmd.AddCommand(newCRDsDetectCmd(), newCRDsInstallCmd())
	return cmd
}

func newCRDsDetectCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "detect",
		Short: "Show what CRDs are installed and who manages them",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := context.Background()
			k := &kube.Client{Context: globalContext, Kubeconfig: globalKubeconfig}
			result, err := crd.Detect(ctx, k)
			if err != nil {
				return err
			}
			return emit(cmd.OutOrStdout(), result, func(w io.Writer) {
				printCRDDetect(w, result)
			})
		},
	}
}

func newCRDsInstallCmd() *cobra.Command {
	var skipGatewayAPI bool
	var forceGatewayAPI bool
	var channel string

	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install Gateway API and Envoy Gateway CRDs",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := context.Background()
			c := apiClient()
			fmt.Fprintln(cmd.OutOrStdout(), "Detecting existing CRDs...")
			return c.CRDInstall(ctx, gwpapi.CRDInstallOptions{
				SkipGatewayAPI:  skipGatewayAPI,
				ForceGatewayAPI: forceGatewayAPI,
				Channel:         channel,
				Out:             cmd.OutOrStdout(),
			})
		},
	}

	cmd.Flags().BoolVar(&skipGatewayAPI, "skip-gateway-api-crds", false,
		"skip Gateway API CRDs (use for provider-managed clusters)")
	cmd.Flags().BoolVar(&forceGatewayAPI, "force-gateway-api-crds", false,
		"install Gateway API CRDs even when already present")
	cmd.Flags().StringVar(&channel, "channel", "standard",
		"Gateway API channel: standard or experimental")
	return cmd
}

// ── pair ──────────────────────────────────────────────────────────────────────

// pairNames returns the derived names for a pair, respecting --suffix.
// When --suffix is set, the numeric index is used only for the Helm release
// ordering; all resource names derive from the suffix string instead.
func pairNames(index int) names.Pair {
	if globalSuffix != "" {
		return names.ForSuffix(globalPrefix, globalSuffix)
	}
	return names.For(globalPrefix, index)
}

func newPairCmd(info BuildInfo) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pair",
		Short: "Manage eg-pair Helm releases",
	}
	cmd.AddCommand(
		newPairInstallCmd(),
		newPairDeleteCmd(),
		newPairStatusCmd(info),
		newPairListCmd(),
		newPairInfoCmd(),
	)
	return cmd
}

func newPairInstallCmd() *cobra.Command {
	var extraSet []string
	var helmTimeout time.Duration
	var waitTimeout time.Duration

	cmd := &cobra.Command{
		Use:   "install <index>",
		Short: "Install or upgrade a pair",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			index, err := parseIndex(args[0])
			if err != nil {
				return err
			}
			ctx := context.Background()
			c := apiClient()
			return c.PairInstall(ctx, index, gwpapi.PairInstallOptions{
				ExtraSet:    extraSet,
				HelmTimeout: helmTimeout,
				WaitTimeout: waitTimeout,
				Out:         cmd.OutOrStdout(),
			})
		},
	}

	cmd.Flags().StringArrayVar(&extraSet, "set", nil,
		"additional --set flags passed to helm (repeatable)")
	cmd.Flags().DurationVar(&helmTimeout, "helm-timeout", 5*time.Minute,
		"timeout for helm upgrade --install")
	cmd.Flags().DurationVar(&waitTimeout, "wait-timeout", 3*time.Minute,
		"timeout for post-install readiness polling")
	return cmd
}

func newPairDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <index>",
		Short: "Uninstall a pair",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			index, err := parseIndex(args[0])
			if err != nil {
				return err
			}
			ctx := context.Background()
			c := apiClient()
			return c.PairDelete(ctx, index, cmd.OutOrStdout())
		},
	}
}

func newPairStatusCmd(info BuildInfo) *cobra.Command {
	return &cobra.Command{
		Use:   "status [index]",
		Short: "Show health of one pair or all pairs",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			c := apiClient()
			out := cmd.OutOrStdout()

			if len(args) == 1 {
				index, err := parseIndex(args[0])
				if err != nil {
					return err
				}
				s, err := c.PairGet(ctx, index)
				if err != nil {
					return err
				}
				s.BundledEGVer = info.EGVersion
				if s.InstalledEGVer != "" && s.InstalledEGVer != info.EGVersion {
					s.VersionDrift = true
				}
				return emit(out, s, func(w io.Writer) { printPairStatus(w, s) })
			}

			statuses, err := c.PairList(ctx)
			if err != nil {
				return err
			}
			if len(statuses) == 0 {
				if globalOutput == "json" {
					fmt.Fprintln(out, "[]")
					return nil
				}
				fmt.Fprintln(out, "No pairs installed.")
				return nil
			}
			return emit(out, statuses, func(w io.Writer) {
				fmt.Fprintf(w, "%-6s %-20s %-22s %-12s %-10s %-10s %s\n",
					"PAIR", "SYSTEM-NS", "DATAPLANE-NS", "GW-CLASS", "CONTROLLER", "GW-CLASS", "L3-GATEWAYS")
				for _, s := range statuses {
					printPairRow(w, s)
				}
			})
		},
	}
}

func newPairListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all installed pairs",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := context.Background()
			c := apiClient()
			statuses, err := c.PairList(ctx)
			if err != nil {
				return err
			}
			if len(statuses) == 0 {
				if globalOutput == "json" {
					fmt.Fprintln(cmd.OutOrStdout(), "[]")
					return nil
				}
				fmt.Fprintln(cmd.OutOrStdout(), "No pairs installed.")
				return nil
			}
			return emit(cmd.OutOrStdout(), statuses, func(w io.Writer) {
				fmt.Fprintf(w, "%-6s %-20s %-12s %-10s\n", "PAIR", "SYSTEM-NS", "GW-CLASS", "STATUS")
				for _, s := range statuses {
					fmt.Fprintf(w, "%-6d %-20s %-12s %-10s\n",
						s.Index, s.Names.SystemNS, s.Names.GatewayClass, s.HelmStatus)
				}
			})
		},
	}
}

func newPairInfoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "info <index>",
		Short: "Print coupling fields for writing Layer 3 manifests",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			index, err := parseIndex(args[0])
			if err != nil {
				return err
			}
			n := pair.Info(globalPrefix, globalSuffix, index)
			return emit(cmd.OutOrStdout(), n, func(w io.Writer) {
				fmt.Fprintf(w, "Pair %d:\n", index)
				fmt.Fprintf(w, "  gatewayClassName:    %s\n", n.GatewayClass)
				fmt.Fprintf(w, "  dataplaneNamespace:  %s\n", n.DataplaneNS)
				fmt.Fprintf(w, "  allowedRoutes label: tr/gateway-routes=true\n\n")
				fmt.Fprintf(w, "Use in your Gateway manifests:\n\n")
				fmt.Fprintf(w, "  spec:\n")
				fmt.Fprintf(w, "    gatewayClassName: %s\n", n.GatewayClass)
				fmt.Fprintf(w, "    infrastructure:\n")
				fmt.Fprintf(w, "      parametersRef:\n")
				fmt.Fprintf(w, "        group: gateway.envoyproxy.io\n")
				fmt.Fprintf(w, "        kind: EnvoyProxy\n")
				fmt.Fprintf(w, "        name: <your-tier-name>  # must exist in %s\n\n", n.DataplaneNS)
				fmt.Fprintf(w, "  listeners:\n")
				fmt.Fprintf(w, "  - allowedRoutes:\n")
				fmt.Fprintf(w, "      namespaces:\n")
				fmt.Fprintf(w, "        from: Selector\n")
				fmt.Fprintf(w, "        selector:\n")
				fmt.Fprintf(w, "          matchLabels:\n")
				fmt.Fprintf(w, "            tr/gateway-routes: \"true\"\n")
			})
		},
	}
}

// ── text renderers ────────────────────────────────────────────────────────────

func printCRDDetect(w io.Writer, result crd.DetectResult) {
	gapi := result.GatewayAPI
	fmt.Fprintf(w, "Gateway API CRDs:\n")
	switch gapi.State {
	case crd.NotInstalled:
		fmt.Fprintf(w, "  state: not installed\n")
	case crd.ProviderManaged:
		fmt.Fprintf(w, "  state:   provider-managed (%s)\n", gapi.ProviderManager)
		fmt.Fprintf(w, "  version: %s (%s)\n", gapi.BundleVersion, gapi.Channel)
		fmt.Fprintf(w, "  note:    gwp will not overwrite -- use --skip-gateway-api-crds\n")
	case crd.SelfManaged:
		fmt.Fprintf(w, "  state:   installed\n")
		fmt.Fprintf(w, "  version: %s (%s)\n", gapi.BundleVersion, gapi.Channel)
	}
	eg := result.EG
	fmt.Fprintf(w, "\nEnvoy Gateway CRDs:\n")
	switch eg.State {
	case crd.NotInstalled:
		fmt.Fprintf(w, "  state: not installed\n")
	default:
		fmt.Fprintf(w, "  state:   installed\n")
		fmt.Fprintf(w, "  version: %s\n", eg.Version)
	}
}

func printPairStatus(w io.Writer, s *pair.Status) {
	fmt.Fprintf(w, "Pair %d (%s):\n", s.Index, s.Names.GatewayClass)
	fmt.Fprintf(w, "  System namespace:    %s\n", s.Names.SystemNS)
	fmt.Fprintf(w, "  Dataplane namespace: %s\n", s.Names.DataplaneNS)
	fmt.Fprintf(w, "  Helm status:         %s\n", s.HelmStatus)
	if s.InstalledEGVer != "" {
		drift := ""
		if s.VersionDrift {
			drift = fmt.Sprintf("  [DRIFT: gwp bundles %s]", s.BundledEGVer)
		}
		fmt.Fprintf(w, "  EG version:          %s%s\n", s.InstalledEGVer, drift)
	}
	fmt.Fprintf(w, "  Controller:          %s/envoy-gateway", s.Names.SystemNS)
	if s.Controller.Available {
		fmt.Fprintf(w, "  Available (%s)\n", s.Controller.Ready)
	} else {
		fmt.Fprintf(w, "  NOT AVAILABLE (%s)\n", s.Controller.Ready)
	}
	fmt.Fprintf(w, "  GatewayClass:        %s", s.Names.GatewayClass)
	if s.GatewayClass.Accepted {
		fmt.Fprintf(w, "  Accepted=True\n")
	} else {
		fmt.Fprintf(w, "  Accepted=False (%s)\n", s.GatewayClass.Reason)
	}
	if len(s.L3Gateways) == 0 {
		fmt.Fprintf(w, "\nLayer 3: no Gateways in %s\n", s.Names.DataplaneNS)
		return
	}
	fmt.Fprintf(w, "\nLayer 3 (in %s):\n", s.Names.DataplaneNS)
	for _, gw := range s.L3Gateways {
		programmedStr := "Programmed=False"
		if gw.Programmed {
			programmedStr = "Programmed=True"
		}
		fmt.Fprintf(w, "  Gateway %-20s  %-18s  parametersRef: %-12s  proxy: %s\n",
			gw.Name, programmedStr, gw.EnvoyProxyName, gw.ProxyReady)
	}
}

func printPairRow(w io.Writer, s *pair.Status) {
	controllerStr := "NotReady"
	if s.Controller.Available {
		controllerStr = "Available"
	}
	gcStr := "Unknown"
	if s.GatewayClass.Accepted {
		gcStr = "Accepted"
	}
	fmt.Fprintf(w, "%-6d %-20s %-22s %-12s %-10s %-10s %d\n",
		s.Index, s.Names.SystemNS, s.Names.DataplaneNS, s.Names.GatewayClass,
		controllerStr, gcStr, len(s.L3Gateways))
}

func parseIndex(s string) (int, error) {
	var i int
	if _, err := fmt.Sscanf(strings.TrimSpace(s), "%d", &i); err != nil || i < 1 {
		return 0, fmt.Errorf("pair index must be a positive integer, got %q", s)
	}
	return i, nil
}

// ensure os is used (printPairStatus previously used os.Stdout directly -- now uses w parameter)
var _ = os.Stdout
