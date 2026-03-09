package k8s

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	sigsyaml "sigs.k8s.io/yaml"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	agentgatewayGroup   = "agentgateway.dev"
	agentgatewayVersion = "v1alpha1"
	backendKind         = "AgentgatewayBackend"

	gatewayAPIGroup   = "gateway.networking.k8s.io"
	gatewayAPIVersion = "v1"
	httpRouteKind     = "HTTPRoute"
)

var (
	backendGVK = schema.GroupVersionKind{
		Group:   agentgatewayGroup,
		Version: agentgatewayVersion,
		Kind:    backendKind,
	}
	httpRouteGVK = schema.GroupVersionKind{
		Group:   gatewayAPIGroup,
		Version: gatewayAPIVersion,
		Kind:    httpRouteKind,
	}
)

// ApplyRequest contains all the inputs needed to create or update an
// AgentgatewayBackend + HTTPRoute pair.
type ApplyRequest struct {
	// Name is a DNS-1123-safe label derived from the server name.
	Name string
	// Namespace is the namespace where the Gateway lives (and where these
	// resources will be created).
	Namespace string
	// GatewayName is the name of the existing Gateway resource to attach to.
	GatewayName string
	// GatewayNamespace is the namespace of the Gateway (may differ from Namespace).
	GatewayNamespace string
	// RemoteURL is the full URL of the remote MCP server (e.g. "https://host:8080/mcp").
	RemoteURL string
	// Protocol is the CRD protocol string: "StreamableHTTP" or "SSE".
	Protocol string
	// Path is the HTTP path prefix to expose on the gateway (e.g. "/my-server/mcp").
	Path string
}

// ApplyMCPBackend creates or updates an AgentgatewayBackend and an HTTPRoute.
// Both resources share the same name and are placed in req.Namespace.
func ApplyMCPBackend(ctx context.Context, c client.Client, req ApplyRequest) error {
	host, port, urlPath, err := parseRemoteURL(req.RemoteURL)
	if err != nil {
		return fmt.Errorf("invalid remote URL %q: %w", req.RemoteURL, err)
	}
	if urlPath == "" {
		urlPath = defaultMCPPath(req.Protocol)
	}

	backend := buildBackend(req, host, port, urlPath)
	if err := applyUnstructured(ctx, c, backend); err != nil {
		return fmt.Errorf("failed to apply AgentgatewayBackend: %w", err)
	}

	route := buildHTTPRoute(req)
	if err := applyUnstructured(ctx, c, route); err != nil {
		return fmt.Errorf("failed to apply HTTPRoute: %w", err)
	}

	return nil
}

// DeleteMCPBackend removes both the AgentgatewayBackend and HTTPRoute by name.
func DeleteMCPBackend(ctx context.Context, c client.Client, name, namespace string) error {
	backend := &unstructured.Unstructured{}
	backend.SetGroupVersionKind(backendGVK)
	backend.SetName(name)
	backend.SetNamespace(namespace)
	if err := c.Delete(ctx, backend); err != nil && !k8serrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete AgentgatewayBackend %q: %w", name, err)
	}

	route := &unstructured.Unstructured{}
	route.SetGroupVersionKind(httpRouteGVK)
	route.SetName(name)
	route.SetNamespace(namespace)
	if err := c.Delete(ctx, route); err != nil && !k8serrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete HTTPRoute %q: %w", name, err)
	}

	return nil
}

// RenderManifests returns the YAML for the AgentgatewayBackend and HTTPRoute
// that would be created by ApplyMCPBackend, separated by "---".
// No cluster connection is required.
func RenderManifests(req ApplyRequest) (string, error) {
	host, port, urlPath, err := parseRemoteURL(req.RemoteURL)
	if err != nil {
		return "", fmt.Errorf("invalid remote URL %q: %w", req.RemoteURL, err)
	}
	if urlPath == "" {
		urlPath = defaultMCPPath(req.Protocol)
	}

	backendYAML, err := marshalUnstructured(buildBackend(req, host, port, urlPath))
	if err != nil {
		return "", fmt.Errorf("failed to render AgentgatewayBackend: %w", err)
	}

	routeYAML, err := marshalUnstructured(buildHTTPRoute(req))
	if err != nil {
		return "", fmt.Errorf("failed to render HTTPRoute: %w", err)
	}

	return "---\n" + backendYAML + "---\n" + routeYAML, nil
}

func marshalUnstructured(obj *unstructured.Unstructured) (string, error) {
	b, err := sigsyaml.Marshal(obj.Object)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// SanitizeName converts an MCP server name (e.g. "user/my-server") to a
// DNS-1123-compliant label suitable for use as a Kubernetes resource name.
func SanitizeName(serverName string) string {
	name := strings.ToLower(serverName)
	name = strings.NewReplacer("/", "-", ".", "-").Replace(name)
	re := regexp.MustCompile(`[^a-z0-9-]+`)
	name = re.ReplaceAllString(name, "-")
	name = strings.Trim(name, "-")
	if len(name) > 63 {
		name = strings.TrimRight(name[:63], "-")
	}
	return name
}

// --- private helpers ---

func parseRemoteURL(rawURL string) (host string, port int32, path string, err error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", 0, "", err
	}

	hostname := u.Hostname()
	portStr := u.Port()

	var portInt int
	if portStr != "" {
		portInt, err = strconv.Atoi(portStr)
		if err != nil {
			return "", 0, "", fmt.Errorf("invalid port %q: %w", portStr, err)
		}
	} else {
		switch u.Scheme {
		case "https":
			portInt = 443
		default:
			portInt = 80
		}
	}

	return hostname, int32(portInt), u.Path, nil
}

func defaultMCPPath(protocol string) string {
	if protocol == "SSE" {
		return "/sse"
	}
	return "/mcp"
}

func buildBackend(req ApplyRequest, host string, port int32, urlPath string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(backendGVK)
	obj.SetName(req.Name)
	obj.SetNamespace(req.Namespace)

	// Use int64 for port — unstructured deep copy only handles JSON-native numeric types.
	target := map[string]interface{}{
		"name": req.Name + "-target",
		"static": map[string]interface{}{
			"host":     host,
			"port":     int64(port),
			"path":     urlPath,
			"protocol": req.Protocol,
		},
	}
	obj.Object["spec"] = map[string]interface{}{
		"mcp": map[string]interface{}{
			"targets": []interface{}{target},
		},
	}
	return obj
}

func buildHTTPRoute(req ApplyRequest) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(httpRouteGVK)
	obj.SetName(req.Name)
	obj.SetNamespace(req.Namespace)

	parentRef := map[string]interface{}{
		"name":      req.GatewayName,
		"namespace": req.GatewayNamespace,
	}

	rule := map[string]interface{}{
		"matches": []interface{}{
			map[string]interface{}{
				"path": map[string]interface{}{
					"type":  "PathPrefix",
					"value": req.Path,
				},
			},
		},
		"backendRefs": []interface{}{
			map[string]interface{}{
				"name":  req.Name,
				"group": agentgatewayGroup,
				"kind":  backendKind,
			},
		},
	}

	obj.Object["spec"] = map[string]interface{}{
		"parentRefs": []interface{}{parentRef},
		"rules":      []interface{}{rule},
	}
	return obj
}

func applyUnstructured(ctx context.Context, c client.Client, obj *unstructured.Unstructured) error {
	existing := &unstructured.Unstructured{}
	existing.SetGroupVersionKind(obj.GroupVersionKind())
	err := c.Get(ctx, client.ObjectKeyFromObject(obj), existing)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return c.Create(ctx, obj)
		}
		return err
	}
	obj.SetResourceVersion(existing.GetResourceVersion())
	return c.Update(ctx, obj)
}
