package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/agentregistry-dev/agentregistry/internal/k8s"
	"github.com/agentregistry-dev/agentregistry/pkg/printer"
	"github.com/modelcontextprotocol/registry/pkg/model"
	"github.com/spf13/cobra"
)

const defaultGatewayNamespace = "agentgateway-system"

var (
	agentgatewayGateway         string
	agentgatewayGatewayNs       string
	agentgatewayResourceNs      string
	agentgatewayKubeconfig      string
	agentgatewayDryRun          bool
	agentgatewayVersion         string
	agentgatewayDeployName      string
	agentgatewayDeployURL       string
	agentgatewayDeployProtocol  string
	listGatewaysNamespace       string
	listGatewaysKubeconfig      string
)

var DeployAgentgatewayCmd = &cobra.Command{
	Use:   "agentgateway [server-name]",
	Short: "Deploy AgentGateway config for an MCP server",
	Long: `Deploy AgentGateway config for an MCP server (apply AgentgatewayBackend + HTTPRoute).

Resolve server metadata either by name from the registry, or via --name, --url, and --protocol flags.
Use --dry-run to preview manifests without applying.
`,
	Args: cobra.MaximumNArgs(1),
	RunE: runDeployAgentgateway,
}

var deployAgentgatewayListGatewaysCmd = &cobra.Command{
	Use:   "list-gateways",
	Short: "List Gateway resources in the cluster",
	Long:  `List all Gateway resources visible via the current kubeconfig, optionally scoped to a namespace.`,
	RunE:  runDeployAgentgatewayListGateways,
}

func init() {
	DeployCmd.AddCommand(DeployAgentgatewayCmd)

	DeployAgentgatewayCmd.Flags().StringVar(&agentgatewayGateway, "gateway", "", "Name of the existing Gateway resource to attach to")
	DeployAgentgatewayCmd.Flags().StringVar(&agentgatewayGatewayNs, "gateway-namespace", defaultGatewayNamespace, "Namespace of the Gateway")
	DeployAgentgatewayCmd.Flags().StringVar(&agentgatewayResourceNs, "resource-namespace", "", "Namespace for AgentgatewayBackend and HTTPRoute (defaults to gateway-namespace)")
	DeployAgentgatewayCmd.Flags().StringVar(&agentgatewayKubeconfig, "kubeconfig", "", "Path to kubeconfig (default: KUBECONFIG env var or ~/.kube/config)")
	DeployAgentgatewayCmd.Flags().BoolVar(&agentgatewayDryRun, "dry-run", false, "Print manifests to stdout instead of applying to the cluster")
	DeployAgentgatewayCmd.Flags().StringVar(&agentgatewayVersion, "version", "latest", "Server version (when resolving by name from registry)")
	DeployAgentgatewayCmd.Flags().StringVar(&agentgatewayDeployName, "name", "", "Server name (use with --url when not resolving from registry)")
	DeployAgentgatewayCmd.Flags().StringVar(&agentgatewayDeployURL, "url", "", "Remote URL of the MCP server (use with --name when not resolving from registry)")
	DeployAgentgatewayCmd.Flags().StringVar(&agentgatewayDeployProtocol, "protocol", "streamable-http", "Transport protocol: streamable-http or sse (when using --url)")

	deployAgentgatewayListGatewaysCmd.Flags().StringVar(&listGatewaysNamespace, "namespace", "", "Namespace to search for Gateway resources (default: all namespaces)")
	deployAgentgatewayListGatewaysCmd.Flags().StringVar(&listGatewaysKubeconfig, "kubeconfig", "", "Path to kubeconfig (default: KUBECONFIG env var or ~/.kube/config)")

	DeployAgentgatewayCmd.AddCommand(deployAgentgatewayListGatewaysCmd)
}

func runDeployAgentgateway(cmd *cobra.Command, args []string) error {
	var serverName, remoteURL, protocolCRD string

	if len(args) >= 1 && args[0] != "" {
		// Resolve by name from registry
		serverName = args[0]
		if apiClient == nil {
			return fmt.Errorf("API client not initialized (registry required when using server name)")
		}
		server, err := apiClient.GetServerByNameAndVersion(serverName, agentgatewayVersion)
		if err != nil {
			return fmt.Errorf("failed to get server from registry: %w", err)
		}
		if server == nil {
			return fmt.Errorf("server %s version %s not found in registry", serverName, agentgatewayVersion)
		}
		remoteURL, protocolCRD, err = selectTransport(server.Server.Remotes)
		if err != nil {
			return err
		}
	} else if agentgatewayDeployURL != "" && agentgatewayDeployName != "" {
		// Resolve by flags (no registry)
		serverName = agentgatewayDeployName
		remoteURL = agentgatewayDeployURL
		protocolCRD = normalizeProtocolToCRD(agentgatewayDeployProtocol)
	} else {
		return fmt.Errorf("either provide a server name as argument (must exist in registry) or use --name and --url flags")
	}

	if agentgatewayDryRun {
		// Dry-run: only need gateway for output; --gateway optional for dry-run
	} else {
		if agentgatewayGateway == "" {
			return fmt.Errorf("--gateway is required when applying to the cluster (omit --dry-run)")
		}
	}

	namespace := agentgatewayResourceNs
	if namespace == "" {
		namespace = agentgatewayGatewayNs
	}

	safeName := k8s.SanitizeName(serverName)
	exposedPath := fmt.Sprintf("/%s/mcp", safeName)

	applyReq := k8s.ApplyRequest{
		Name:             safeName,
		Namespace:        namespace,
		GatewayName:      agentgatewayGateway,
		GatewayNamespace: agentgatewayGatewayNs,
		RemoteURL:        remoteURL,
		Protocol:         protocolCRD,
		Path:             exposedPath,
	}

	if agentgatewayDryRun {
		manifests, err := k8s.RenderManifests(applyReq)
		if err != nil {
			return fmt.Errorf("failed to render manifests: %w", err)
		}
		fmt.Print(manifests)
		return nil
	}

	c, err := k8s.NewClient(agentgatewayKubeconfig)
	if err != nil {
		return fmt.Errorf("failed to connect to cluster: %w", err)
	}

	gateways, err := k8s.ListGateways(context.Background(), c, agentgatewayGatewayNs)
	if err != nil {
		return fmt.Errorf("failed to query cluster for Gateway: %w", err)
	}
	found := false
	for _, gw := range gateways {
		if gw.Name == agentgatewayGateway && gw.Namespace == agentgatewayGatewayNs {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("gateway %q not found in namespace %q", agentgatewayGateway, agentgatewayGatewayNs)
	}

	if err := k8s.ApplyMCPBackend(context.Background(), c, applyReq); err != nil {
		return fmt.Errorf("failed to apply AgentGateway config: %w", err)
	}

	printer.PrintSuccess(fmt.Sprintf("Deployed AgentGateway config for %s at %s via gateway %s", serverName, exposedPath, agentgatewayGateway))
	return nil
}

func runDeployAgentgatewayListGateways(cmd *cobra.Command, _ []string) error {
	c, err := k8s.NewClient(listGatewaysKubeconfig)
	if err != nil {
		return fmt.Errorf("failed to connect to cluster: %w", err)
	}

	gateways, err := k8s.ListGateways(context.Background(), c, listGatewaysNamespace)
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

// selectTransport picks the first streamable-http or sse remote.
// Returns (url, crdProtocol, error). CRD protocol is "StreamableHTTP" or "SSE".
func selectTransport(remotes []model.Transport) (string, string, error) {
	for _, r := range remotes {
		t := strings.ToLower(r.Type)
		if (t == "streamable-http" || t == "sse") && r.URL != "" {
			protocol := "StreamableHTTP"
			if t == "sse" {
				protocol = "SSE"
			}
			return r.URL, protocol, nil
		}
	}
	return "", "", fmt.Errorf("server has no remote URL (streamable-http or sse); use --name and --url flags instead")
}

func normalizeProtocolToCRD(p string) string {
	switch strings.ToLower(p) {
	case "sse":
		return "SSE"
	default:
		return "StreamableHTTP"
	}
}
