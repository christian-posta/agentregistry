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
	"sigs.k8s.io/controller-runtime/pkg/client"
	sigsyaml "sigs.k8s.io/yaml"
)

const (
	agentgatewayGroup   = "agentgateway.dev"
	agentgatewayVersion = "v1alpha1"
	backendKind         = "AgentgatewayBackend"

	enterprisePolicyGroup   = "enterpriseagentgateway.solo.io"
	enterprisePolicyVersion = "v1alpha1"
	enterprisePolicyKind    = "EnterpriseAgentgatewayPolicy"

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
	enterprisePolicyGVK = schema.GroupVersionKind{
		Group:   enterprisePolicyGroup,
		Version: enterprisePolicyVersion,
		Kind:    enterprisePolicyKind,
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

// SSOConfig holds OAuth/OIDC configuration for SSO-enabled MCP deploy.
// All fields are required when --sso is set.
type SSOConfig struct {
	Issuer          string // e.g. https://tenant.auth0.com/
	Audience        string // e.g. https://example.com/mcp
	Provider        string // e.g. Auth0
	PublicBaseURL   string // e.g. https://my-gw.ngrok.io (trailing slash normalized in code)
	JWKSURL         string // e.g. https://tenant.auth0.com/.well-known/jwks.json
	MCPPathSuffix   string // e.g. server (from dev.servereverything/server), used to build /{suffix}/mcp
	JWKSHost        string // parsed from JWKSURL
	JWKSPort        int32  // parsed from JWKSURL
	JWKSPath        string // path component of JWKSURL, no leading / (e.g. .well-known/jwks.json)
	ResourceBaseURL string // PublicBaseURL + /{suffix}/mcp
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

// RenderSSOManifests returns the YAML for the four SSO resources (HTTPRoute,
// MCP backend with TLS, JWKS backend, EnterpriseAgentgatewayPolicy).
// No cluster connection is required.
func RenderSSOManifests(req ApplyRequest, sso SSOConfig) (string, error) {
	host, port, urlPath, err := parseRemoteURL(req.RemoteURL)
	if err != nil {
		return "", fmt.Errorf("invalid remote URL %q: %w", req.RemoteURL, err)
	}
	if urlPath == "" {
		urlPath = defaultMCPPath(req.Protocol)
	}

	routeYAML, err := marshalUnstructured(buildSSOHTTPRoute(req, sso))
	if err != nil {
		return "", fmt.Errorf("failed to render SSO HTTPRoute: %w", err)
	}
	backendYAML, err := marshalUnstructured(buildSSOBackend(req, host, port, urlPath))
	if err != nil {
		return "", fmt.Errorf("failed to render SSO AgentgatewayBackend: %w", err)
	}
	jwksYAML, err := marshalUnstructured(buildJWKSBackend(req, sso))
	if err != nil {
		return "", fmt.Errorf("failed to render JWKS AgentgatewayBackend: %w", err)
	}
	policyYAML, err := marshalUnstructured(buildSSOPolicy(req, sso))
	if err != nil {
		return "", fmt.Errorf("failed to render EnterpriseAgentgatewayPolicy: %w", err)
	}

	return "---\n" + routeYAML + "---\n" + backendYAML + "---\n" + jwksYAML + "---\n" + policyYAML, nil
}

// ApplySSOResources creates or updates the four SSO resources in the cluster.
func ApplySSOResources(ctx context.Context, c client.Client, req ApplyRequest, sso SSOConfig) error {
	host, port, urlPath, err := parseRemoteURL(req.RemoteURL)
	if err != nil {
		return fmt.Errorf("invalid remote URL %q: %w", req.RemoteURL, err)
	}
	if urlPath == "" {
		urlPath = defaultMCPPath(req.Protocol)
	}

	// Apply order: backends first (JWKS, then MCP), then route, then policy (references route).
	if err := applyUnstructured(ctx, c, buildJWKSBackend(req, sso)); err != nil {
		return fmt.Errorf("failed to apply JWKS AgentgatewayBackend: %w", err)
	}
	if err := applyUnstructured(ctx, c, buildSSOBackend(req, host, port, urlPath)); err != nil {
		return fmt.Errorf("failed to apply MCP AgentgatewayBackend: %w", err)
	}
	if err := applyUnstructured(ctx, c, buildSSOHTTPRoute(req, sso)); err != nil {
		return fmt.Errorf("failed to apply SSO HTTPRoute: %w", err)
	}
	if err := applyUnstructured(ctx, c, buildSSOPolicy(req, sso)); err != nil {
		return fmt.Errorf("failed to apply EnterpriseAgentgatewayPolicy: %w", err)
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

// DeriveMCPPathSuffix extracts the part of the server name after the first "/"
// and sanitizes it for use in URL paths (e.g. dev.servereverything/server -> server).
func DeriveMCPPathSuffix(serverName string) string {
	idx := strings.Index(serverName, "/")
	if idx < 0 {
		return ""
	}
	suffix := strings.TrimSpace(serverName[idx+1:])
	if suffix == "" {
		return ""
	}
	suffix = strings.ToLower(suffix)
	re := regexp.MustCompile(`[^a-z0-9\-_]+`)
	suffix = re.ReplaceAllString(suffix, "-")
	suffix = strings.Trim(suffix, "-")
	return suffix
}

// BuildSSOConfig constructs SSOConfig from env-derived values.
// publicBaseURL is normalized (trailing slash removed). JWKS URL is parsed for host, port, path.
func BuildSSOConfig(issuer, audience, provider, publicBaseURL, jwksURL, serverName string) (SSOConfig, error) {
	suffix := DeriveMCPPathSuffix(serverName)
	if suffix == "" {
		return SSOConfig{}, fmt.Errorf("server name must contain '/' (e.g. namespace/name)")
	}

	base := strings.TrimSuffix(strings.TrimSpace(publicBaseURL), "/")
	if base == "" {
		return SSOConfig{}, fmt.Errorf("public base URL cannot be empty")
	}

	jwksHost, jwksPort, jwksPath, err := parseJWKSURL(jwksURL)
	if err != nil {
		return SSOConfig{}, fmt.Errorf("invalid JWKS URL: %w", err)
	}

	return SSOConfig{
		Issuer:          strings.TrimSpace(issuer),
		Audience:        strings.TrimSpace(audience),
		Provider:        strings.TrimSpace(provider),
		PublicBaseURL:   base,
		JWKSURL:         jwksURL,
		MCPPathSuffix:   suffix,
		JWKSHost:        jwksHost,
		JWKSPort:        jwksPort,
		JWKSPath:        jwksPath,
		ResourceBaseURL: base + "/" + suffix + "/mcp",
	}, nil
}

func parseJWKSURL(rawURL string) (host string, port int32, path string, err error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", 0, "", err
	}
	host = u.Hostname()
	portStr := u.Port()
	var portInt int
	if portStr != "" {
		portInt, err = strconv.Atoi(portStr)
		if err != nil {
			return "", 0, "", fmt.Errorf("invalid port %q: %w", portStr, err)
		}
	} else {
		if u.Scheme == "https" {
			portInt = 443
		} else {
			portInt = 80
		}
	}
	path = strings.TrimPrefix(u.Path, "/")
	return host, int32(portInt), path, nil
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

func buildSSOHTTPRoute(req ApplyRequest, sso SSOConfig) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(httpRouteGVK)
	obj.SetName(req.Name)
	obj.SetNamespace(req.Namespace)

	basePath := "/" + sso.MCPPathSuffix + "/mcp"
	matches := []interface{}{
		map[string]interface{}{"path": map[string]interface{}{"type": "Exact", "value": basePath}},
		map[string]interface{}{"path": map[string]interface{}{"type": "Exact", "value": "/.well-known/oauth-protected-resource" + basePath}},
		map[string]interface{}{"path": map[string]interface{}{"type": "Exact", "value": "/.well-known/oauth-authorization-server" + basePath}},
	}

	parentRef := map[string]interface{}{
		"name":      req.GatewayName,
		"namespace": req.GatewayNamespace,
	}

	rule := map[string]interface{}{
		"matches": matches,
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

func buildSSOBackend(req ApplyRequest, host string, port int32, urlPath string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(backendGVK)
	obj.SetName(req.Name)
	obj.SetNamespace(req.Namespace)

	target := map[string]interface{}{
		"name": req.Name + "-target",
		"static": map[string]interface{}{
			"host":     host,
			"port":     int64(port),
			"path":     urlPath,
			"protocol": req.Protocol,
			"policies": map[string]interface{}{"tls": map[string]interface{}{}},
		},
	}
	obj.Object["spec"] = map[string]interface{}{
		"mcp": map[string]interface{}{
			"targets": []interface{}{target},
		},
	}
	return obj
}

func buildJWKSBackend(req ApplyRequest, sso SSOConfig) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(backendGVK)
	obj.SetName(req.Name + "-jwks")
	obj.SetNamespace(req.Namespace)

	obj.Object["spec"] = map[string]interface{}{
		"static": map[string]interface{}{
			"host": sso.JWKSHost,
			"port": int64(sso.JWKSPort),
		},
		"policies": map[string]interface{}{"tls": map[string]interface{}{}},
	}
	return obj
}

func buildSSOPolicy(req ApplyRequest, sso SSOConfig) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(enterprisePolicyGVK)
	obj.SetName(req.Name + "-policy")
	obj.SetNamespace(req.Namespace)

	resourceBase := sso.ResourceBaseURL
	obj.Object["spec"] = map[string]interface{}{
		"targetRefs": []interface{}{
			map[string]interface{}{
				"group": gatewayAPIGroup,
				"kind":  httpRouteKind,
				"name":  req.Name,
			},
		},
		"backend": map[string]interface{}{
			"tls": map[string]interface{}{},
			"mcp": map[string]interface{}{
				"authentication": map[string]interface{}{
					"mode":      "Optional",
					"issuer":    sso.Issuer,
					"audiences": []interface{}{sso.Audience},
					"jwks": map[string]interface{}{
						"backendRef": map[string]interface{}{
							"name":  req.Name + "-jwks",
							"kind":  backendKind,
							"group": agentgatewayGroup,
						},
						"jwksPath": sso.JWKSPath,
					},
					"provider": sso.Provider,
					"resourceMetadata": map[string]interface{}{
						"authorizationServers":   []interface{}{resourceBase},
						"resource":               resourceBase,
						"scopesSupported":        []interface{}{"profile", "openid", "offline_access"},
						"bearerMethodsSupported": []interface{}{"header", "body", "query"},
						"resourceDocumentation":  resourceBase + "/docs",
						"resourcePolicyUri":      resourceBase + "/policies",
					},
				},
			},
		},
		"traffic": map[string]interface{}{
			"cors": map[string]interface{}{
				"allowOrigins":     []interface{}{"*"},
				"allowHeaders":     []interface{}{"*"},
				"allowMethods":     []interface{}{"*"},
				"allowCredentials": false,
			},
			"headerModifiers": map[string]interface{}{
				"request": map[string]interface{}{
					"remove": []interface{}{"x-forwarded-for", "x-forwarded-host", "x-forwarded-proto"},
				},
			},
		},
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
