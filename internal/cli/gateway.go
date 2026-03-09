package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/agentregistry-dev/agentregistry/internal/k8s"
	"github.com/agentregistry-dev/agentregistry/internal/printer"
	"github.com/spf13/cobra"
)

var gatewayCmd = &cobra.Command{
	Use:   "gateway",
	Short: "Manage AgentGateway resources",
	Long:  `Manage AgentGateway resources in a Kubernetes cluster.`,
	// Override the root PersistentPreRunE so gateway commands do not
	// require the local registry daemon to be running.
	PersistentPreRunE: func(_ *cobra.Command, _ []string) error { return nil },
}

var gatewayListCmd = &cobra.Command{
	Use:   "list",
	Short: "List Gateway resources in the cluster",
	Long:  `List all Gateway resources visible via the current kubeconfig, optionally scoped to a namespace.`,
	RunE:  runGatewayList,
}

var (
	gatewayListNamespace  string
	gatewayListKubeconfig string
)

func init() {
	rootCmd.AddCommand(gatewayCmd)
	gatewayCmd.AddCommand(gatewayListCmd)

	gatewayListCmd.Flags().StringVar(&gatewayListNamespace, "namespace", "", "Namespace to search for Gateway resources (default: all namespaces)")
	gatewayListCmd.Flags().StringVar(&gatewayListKubeconfig, "kubeconfig", "", "Path to kubeconfig (default: KUBECONFIG env var or ~/.kube/config)")
}

func runGatewayList(cmd *cobra.Command, _ []string) error {
	c, err := k8s.NewClient(gatewayListKubeconfig)
	if err != nil {
		return fmt.Errorf("failed to connect to cluster: %w", err)
	}

	gateways, err := k8s.ListGateways(context.Background(), c, gatewayListNamespace)
	if err != nil {
		return fmt.Errorf("failed to list gateways: %w", err)
	}

	if len(gateways) == 0 {
		printer.PrintInfo("No Gateway resources found.")
		return nil
	}

	printer.PrintInfo(fmt.Sprintf("%-30s %-20s %-30s %s", "NAME", "NAMESPACE", "CLASS", "LISTENERS"))
	printer.PrintInfo(strings.Repeat("-", 90))
	for _, gw := range gateways {
		printer.PrintInfo(fmt.Sprintf("%-30s %-20s %-30s %s",
			gw.Name,
			gw.Namespace,
			gw.Class,
			strings.Join(gw.Listeners, ", "),
		))
	}
	return nil
}
