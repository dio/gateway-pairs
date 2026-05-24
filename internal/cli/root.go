// Package cli wires the gwp cobra command tree.
package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	// Import charts to ensure embedded assets are linked into the binary.
	_ "github.com/dio/gateway-pairs/charts"

	"github.com/dio/gateway-pairs/gwpapi"
	"github.com/dio/gateway-pairs/crd"
	"github.com/dio/gateway-pairs/internal/kube"
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

	root.AddCommand(
		newVersionCmd(info),
		newCRDsCmd(),
		newPairCmd(),
	)

	return root.Execute()
}

func apiClient() *gwpapi.Client {
	return gwpapi.New(gwpapi.Options{
		KubeContext: globalContext,
		Kubeconfig:  globalKubeconfig,
		Prefix:      globalPrefix,
	})
}

// ── version ───────────────────────────────────────────────────────────────────

func newVersionCmd(info BuildInfo) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print gwp version and bundled component versions",
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintf(cmd.OutOrStdout(), "gwp %s\n", info.Version)
			fmt.Fprintf(cmd.OutOrStdout(), "  eg-version: %s\n", info.EGVersion)
			fmt.Fprintf(cmd.OutOrStdout(), "  commit:     %s\n", info.Commit)
			fmt.Fprintf(cmd.OutOrStdout(), "  built:      %s\n", info.Date)
			return nil
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

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Gateway API CRDs:\n")
			gapi := result.GatewayAPI
			switch gapi.State {
			case crd.NotInstalled:
				fmt.Fprintf(out, "  state: not installed\n")
			case crd.ProviderManaged:
				fmt.Fprintf(out, "  state:   provider-managed (%s)\n", gapi.ProviderManager)
				fmt.Fprintf(out, "  version: %s (%s)\n", gapi.BundleVersion, gapi.Channel)
				fmt.Fprintf(out, "  note:    gwp will not overwrite -- use --skip-gateway-api-crds\n")
			case crd.SelfManaged:
				fmt.Fprintf(out, "  state:   installed\n")
				fmt.Fprintf(out, "  version: %s (%s)\n", gapi.BundleVersion, gapi.Channel)
			}

			fmt.Fprintf(out, "\nEnvoy Gateway CRDs:\n")
			eg := result.EG
			switch eg.State {
			case crd.NotInstalled:
				fmt.Fprintf(out, "  state: not installed\n")
			default:
				fmt.Fprintf(out, "  state:   installed\n")
				fmt.Fprintf(out, "  version: %s\n", eg.Version)
			}
			return nil
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

func newPairCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pair",
		Short: "Manage eg-pair Helm releases",
	}
	cmd.AddCommand(
		newPairInstallCmd(),
		newPairDeleteCmd(),
		newPairStatusCmd(),
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

func newPairStatusCmd() *cobra.Command {
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
				printPairStatus(out, s, true)
				return nil
			}

			statuses, err := c.PairList(ctx)
			if err != nil {
				return err
			}
			if len(statuses) == 0 {
				fmt.Fprintln(out, "No pairs installed.")
				return nil
			}
			fmt.Fprintf(out, "%-6s %-20s %-22s %-12s %-10s %-10s %s\n",
				"PAIR", "SYSTEM-NS", "DATAPLANE-NS", "GW-CLASS", "CONTROLLER", "GW-CLASS", "L3-GATEWAYS")
			for _, s := range statuses {
				printPairRow(out, s)
			}
			return nil
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
				fmt.Fprintln(cmd.OutOrStdout(), "No pairs installed.")
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%-6s %-20s %-12s %-10s\n", "PAIR", "SYSTEM-NS", "GW-CLASS", "STATUS")
			for _, s := range statuses {
				fmt.Fprintf(cmd.OutOrStdout(), "%-6d %-20s %-12s %-10s\n",
					s.Index, s.Names.SystemNS, s.Names.GatewayClass, s.HelmStatus)
			}
			return nil
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
			n := pair.Info(globalPrefix, index)
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Pair %d:\n", index)
			fmt.Fprintf(out, "  gatewayClassName:    %s\n", n.GatewayClass)
			fmt.Fprintf(out, "  dataplaneNamespace:  %s\n", n.DataplaneNS)
			fmt.Fprintf(out, "  allowedRoutes label: tr/gateway-routes=true\n\n")
			fmt.Fprintf(out, "Use in your Gateway manifests:\n\n")
			fmt.Fprintf(out, "  spec:\n")
			fmt.Fprintf(out, "    gatewayClassName: %s\n", n.GatewayClass)
			fmt.Fprintf(out, "    infrastructure:\n")
			fmt.Fprintf(out, "      parametersRef:\n")
			fmt.Fprintf(out, "        group: gateway.envoyproxy.io\n")
			fmt.Fprintf(out, "        kind: EnvoyProxy\n")
			fmt.Fprintf(out, "        name: <your-tier-name>  # must exist in %s\n\n", n.DataplaneNS)
			fmt.Fprintf(out, "  listeners:\n")
			fmt.Fprintf(out, "  - allowedRoutes:\n")
			fmt.Fprintf(out, "      namespaces:\n")
			fmt.Fprintf(out, "        from: Selector\n")
			fmt.Fprintf(out, "        selector:\n")
			fmt.Fprintf(out, "          matchLabels:\n")
			fmt.Fprintf(out, "            tr/gateway-routes: \"true\"\n")
			return nil
		},
	}
}

// ── output helpers ────────────────────────────────────────────────────────────

func printPairStatus(out interface{ Write([]byte) (int, error) }, s *pair.Status, verbose bool) {
	fmt.Fprintf(os.Stdout, "Pair %d (%s):\n", s.Index, s.Names.GatewayClass)
	fmt.Fprintf(os.Stdout, "  System namespace:    %s\n", s.Names.SystemNS)
	fmt.Fprintf(os.Stdout, "  Dataplane namespace: %s\n", s.Names.DataplaneNS)
	fmt.Fprintf(os.Stdout, "  Helm status:         %s\n", s.HelmStatus)
	fmt.Fprintf(os.Stdout, "  Controller:          %s", s.Names.SystemNS+"/envoy-gateway")
	if s.Controller.Available {
		fmt.Fprintf(os.Stdout, "  Available (%s)\n", s.Controller.Ready)
	} else {
		fmt.Fprintf(os.Stdout, "  NOT AVAILABLE (%s)\n", s.Controller.Ready)
	}
	fmt.Fprintf(os.Stdout, "  GatewayClass:        %s", s.Names.GatewayClass)
	if s.GatewayClass.Accepted {
		fmt.Fprintf(os.Stdout, "  Accepted=True\n")
	} else {
		fmt.Fprintf(os.Stdout, "  Accepted=False (%s)\n", s.GatewayClass.Reason)
	}

	if len(s.L3Gateways) == 0 {
		fmt.Fprintf(os.Stdout, "\nLayer 3: no Gateways in %s\n", s.Names.DataplaneNS)
		return
	}
	fmt.Fprintf(os.Stdout, "\nLayer 3 (in %s):\n", s.Names.DataplaneNS)
	for _, gw := range s.L3Gateways {
		programmedStr := "Programmed=False"
		if gw.Programmed {
			programmedStr = "Programmed=True"
		}
		fmt.Fprintf(os.Stdout, "  Gateway %-20s  %-18s  parametersRef: %-12s  proxy: %s\n",
			gw.Name, programmedStr, gw.EnvoyProxyName, gw.ProxyReady)
	}
}

func printPairRow(out interface{ Write([]byte) (int, error) }, s *pair.Status) {
	controllerStr := "NotReady"
	if s.Controller.Available {
		controllerStr = "Available"
	}
	gcStr := "Unknown"
	if s.GatewayClass.Accepted {
		gcStr = "Accepted"
	}
	fmt.Fprintf(os.Stdout, "%-6d %-20s %-22s %-12s %-10s %-10s %d\n",
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
