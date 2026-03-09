package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/agentregistry-dev/agentregistry/internal/cli/mcp"
	"github.com/agentregistry-dev/agentregistry/internal/cli/mcp/manifest"
	"github.com/agentregistry-dev/agentregistry/internal/k8s"
	"github.com/agentregistry-dev/agentregistry/internal/printer"
	v0 "github.com/modelcontextprotocol/registry/pkg/api/v0"
	"github.com/modelcontextprotocol/registry/pkg/model"
	"github.com/spf13/cobra"
)

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Manage MCP servers",
	Long:  `Manage MCP servers.`,
	Args:  cobra.ExactArgs(1),
}

func init() {
	rootCmd.AddCommand(mcpCmd)
	mcpCmd.AddCommand(mcp.AddToolCmd, mcp.BuildCmd, mcp.InitCmd, publishCmd, mcpRemoveCmd)

	// publish flags
	publishCmd.Flags().StringVar(&publishName, "name", "", "Server name in 'author/server' format (required when no folder path is given)")
	publishCmd.Flags().StringVar(&publishURL, "url", "", "Remote URL of the MCP server (e.g. https://api.example.com/mcp)")
	publishCmd.Flags().StringVar(&publishProtocol, "protocol", "streamable-http", "Transport protocol: streamable-http or sse")
	publishCmd.Flags().StringVar(&publishDescription, "description", "", "Short description of the server")
	publishCmd.Flags().StringVar(&publishVersion, "version", "latest", "Version string")
	publishCmd.Flags().StringVar(&publishGateway, "gateway", "", "Name of the AgentGateway Gateway to attach to")
	publishCmd.Flags().StringVar(&publishGatewayNamespace, "gateway-namespace", "default", "Namespace of the Gateway")
	publishCmd.Flags().StringVar(&publishKubeconfig, "kubeconfig", "", "Path to kubeconfig (default: KUBECONFIG env var or ~/.kube/config)")
	publishCmd.Flags().BoolVar(&publishDryRun, "dry-run", false, "Print k8s manifests to stdout instead of applying them to the cluster")

	// remove flags
	mcpRemoveCmd.Flags().StringVar(&removeGateway, "gateway", "", "Name of the AgentGateway Gateway whose resources should be cleaned up")
	mcpRemoveCmd.Flags().StringVar(&removeGatewayNamespace, "gateway-namespace", "default", "Namespace of the Gateway")
	mcpRemoveCmd.Flags().StringVar(&removeKubeconfig, "kubeconfig", "", "Path to kubeconfig (default: KUBECONFIG env var or ~/.kube/config)")
}

// --- publish ---

var (
	publishName             string
	publishURL              string
	publishProtocol         string
	publishDescription      string
	publishVersion          string
	publishGateway          string
	publishGatewayNamespace string
	publishKubeconfig       string
	publishDryRun           bool
)

var publishCmd = &cobra.Command{
	Use:   "publish [mcp-folder-path]",
	Short: "Publish an MCP server to the registry",
	Long: `Publish an MCP server to the registry.

Local project:
  arctl mcp publish ./my-server

Remote server (no local project required):
  arctl mcp publish --name user/my-server --url https://api.example.com/mcp`,
	Args: cobra.MaximumNArgs(1),
	RunE: runPublish,
}

func runPublish(cmd *cobra.Command, args []string) error {
	var serverJSON v0.ServerJSON

	if len(args) == 1 {
		// --- local project path ---
		absPath, err := filepath.Abs(args[0])
		if err != nil {
			return fmt.Errorf("failed to resolve MCP path: %w", err)
		}

		mgr := manifest.NewManager(absPath)
		if !mgr.Exists() {
			return fmt.Errorf(
				"mcp.yaml not found in %s. Run 'arctl mcp init' first or specify a valid path",
				absPath,
			)
		}

		m, err := mgr.Load()
		if err != nil {
			return fmt.Errorf("failed to load project manifest: %w", err)
		}

		author := "user"
		if m.Author != "" {
			author = m.Author
		}
		ver := m.Version
		if ver == "" {
			ver = "latest"
		}

		printer.PrintInfo(fmt.Sprintf("Publishing MCP server from: %s", absPath))

		serverJSON = v0.ServerJSON{
			Schema:      model.CurrentSchemaURL,
			Name:        fmt.Sprintf("%s/%s", strings.ToLower(author), strings.ToLower(m.Name)),
			Description: m.Description,
			Version:     ver,
		}
		if publishURL != "" {
			serverJSON.Remotes = []model.Transport{{Type: normalizeProtocol(publishProtocol), URL: publishURL}}
		}
	} else {
		// --- remote server via flags ---
		if publishName == "" {
			return fmt.Errorf("--name is required when no folder path is given")
		}
		if publishURL == "" {
			return fmt.Errorf("--url is required when no folder path is given")
		}

		printer.PrintInfo(fmt.Sprintf("Publishing remote MCP server: %s", publishName))

		serverJSON = v0.ServerJSON{
			Schema:      model.CurrentSchemaURL,
			Name:        publishName,
			Description: publishDescription,
			Version:     publishVersion,
			Remotes:     []model.Transport{{Type: normalizeProtocol(publishProtocol), URL: publishURL}},
		}
	}

	resp, err := APIClient.PublishServer(&serverJSON)
	if err != nil {
		return fmt.Errorf("failed to publish server: %w", err)
	}

	printer.PrintInfo(fmt.Sprintf("Server published successfully: %s", resp.Server.Name))

	if publishGateway == "" {
		return nil
	}

	// -- k8s / gateway integration --
	proto, remoteURL, err := selectTransport(resp.Server.Remotes)
	if err != nil {
		return fmt.Errorf("cannot configure gateway: %w", err)
	}

	safeName := k8s.SanitizeName(resp.Server.Name)
	exposedPath := fmt.Sprintf("/%s/mcp", safeName)

	applyReq := k8s.ApplyRequest{
		Name:             safeName,
		Namespace:        publishGatewayNamespace,
		GatewayName:      publishGateway,
		GatewayNamespace: publishGatewayNamespace,
		RemoteURL:        remoteURL,
		Protocol:         proto,
		Path:             exposedPath,
	}

	if publishDryRun {
		manifests, err := k8s.RenderManifests(applyReq)
		if err != nil {
			return fmt.Errorf("failed to render manifests: %w", err)
		}
		fmt.Print(manifests)
		return nil
	}

	c, err := k8s.NewClient(publishKubeconfig)
	if err != nil {
		return fmt.Errorf("failed to connect to cluster: %w", err)
	}

	// Verify the target Gateway exists.
	gateways, err := k8s.ListGateways(context.Background(), c, publishGatewayNamespace)
	if err != nil {
		return fmt.Errorf("failed to query cluster for Gateway: %w", err)
	}
	found := false
	for _, gw := range gateways {
		if gw.Name == publishGateway && gw.Namespace == publishGatewayNamespace {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("gateway %q not found in namespace %q", publishGateway, publishGatewayNamespace)
	}

	if err := k8s.ApplyMCPBackend(context.Background(), c, applyReq); err != nil {
		return fmt.Errorf("failed to configure AgentGateway: %w", err)
	}

	printer.PrintInfo(fmt.Sprintf("Exposed at: %s via gateway %s", exposedPath, publishGateway))
	return nil
}

// normalizeProtocol maps user-facing protocol strings to model.Transport type values.
func normalizeProtocol(p string) string {
	switch strings.ToLower(p) {
	case "sse":
		return "sse"
	default:
		return "streamable-http"
	}
}

// selectTransport picks the first streamable-http transport, falling back to sse.
// Returns the CRD protocol string ("StreamableHTTP" or "SSE") and the transport URL.
func selectTransport(transports []model.Transport) (proto, rawURL string, err error) {
	for _, t := range transports {
		if t.Type == "streamable-http" {
			return "StreamableHTTP", t.URL, nil
		}
	}
	for _, t := range transports {
		if t.Type == "sse" {
			return "SSE", t.URL, nil
		}
	}
	return "", "", fmt.Errorf("server has no streamable-http or sse transport configured; " +
		"use --url to specify a remote URL")
}

// --- remove ---

var (
	removeGateway          string
	removeGatewayNamespace string
	removeKubeconfig       string
)

var mcpRemoveCmd = &cobra.Command{
	Use:   "remove [server-name]",
	Short: "Remove an MCP server from the registry and optionally clean up gateway resources",
	Long:  `Remove an MCP server and, when --gateway is provided, delete the associated AgentgatewayBackend and HTTPRoute.`,
	Args:  cobra.ExactArgs(1),
	RunE:  runMCPRemove,
}

func runMCPRemove(cmd *cobra.Command, args []string) error {
	serverName := args[0]

	if removeGateway == "" {
		printer.PrintInfo(fmt.Sprintf("Note: registry server removal is not yet supported. "+
			"Use --gateway to clean up gateway resources for %q.", serverName))
		return nil
	}

	c, err := k8s.NewClient(removeKubeconfig)
	if err != nil {
		return fmt.Errorf("failed to connect to cluster: %w", err)
	}

	safeName := k8s.SanitizeName(serverName)
	if err := k8s.DeleteMCPBackend(context.Background(), c, safeName, removeGatewayNamespace); err != nil {
		return fmt.Errorf("failed to remove gateway resources: %w", err)
	}

	printer.PrintInfo(fmt.Sprintf("Gateway resources for %q removed from namespace %q.", serverName, removeGatewayNamespace))
	return nil
}
