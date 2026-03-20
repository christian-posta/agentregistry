package k8s

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func newFakeClient(objs ...client.Object) client.Client {
	scheme := runtime.NewScheme()
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
}

func TestSanitizeName(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"user/my-server", "user-my-server"},
		{"io.example.com/weather-api", "io-example-com-weather-api"},
		{"My_Server/NAME", "my-server-name"},
		{strings.Repeat("a", 70), strings.Repeat("a", 63)},
	}
	for _, tc := range cases {
		got := SanitizeName(tc.input)
		assert.Equal(t, tc.want, got, "input: %q", tc.input)
	}
}

func TestParseRemoteURL(t *testing.T) {
	cases := []struct {
		rawURL   string
		wantHost string
		wantPort int32
		wantPath string
	}{
		{"https://example.com:8080/mcp", "example.com", 8080, "/mcp"},
		{"http://host.local/sse", "host.local", 80, "/sse"},
		{"https://api.example.com/v1/mcp", "api.example.com", 443, "/v1/mcp"},
	}
	for _, tc := range cases {
		host, port, path, err := parseRemoteURL(tc.rawURL)
		require.NoError(t, err, "url: %q", tc.rawURL)
		assert.Equal(t, tc.wantHost, host)
		assert.Equal(t, tc.wantPort, port)
		assert.Equal(t, tc.wantPath, path)
	}
}

func TestApplyMCPBackend_Create(t *testing.T) {
	ctx := context.Background()
	c := newFakeClient()

	req := ApplyRequest{
		Name:             "user-my-server",
		Namespace:        "agentgateway-system",
		GatewayName:      "my-gw",
		GatewayNamespace: "agentgateway-system",
		RemoteURL:        "https://example.com:8080/mcp",
		Protocol:         "StreamableHTTP",
		Path:             "/user-my-server/mcp",
	}

	err := ApplyMCPBackend(ctx, c, req)
	require.NoError(t, err)

	// Verify AgentgatewayBackend was created with correct spec.
	backend := &unstructured.Unstructured{}
	backend.SetGroupVersionKind(backendGVK)
	err = c.Get(ctx, client.ObjectKey{Name: req.Name, Namespace: req.Namespace}, backend)
	require.NoError(t, err)

	spec, ok := backend.Object["spec"].(map[string]interface{})
	require.True(t, ok, "spec must be a map")
	mcpSpec, ok := spec["mcp"].(map[string]interface{})
	require.True(t, ok, "spec.mcp must be a map")
	targets, ok := mcpSpec["targets"].([]interface{})
	require.True(t, ok, "spec.mcp.targets must be a slice")
	require.Len(t, targets, 1)

	target := targets[0].(map[string]interface{})
	assert.Equal(t, req.Name+"-target", target["name"])
	static := target["static"].(map[string]interface{})
	assert.Equal(t, "example.com", static["host"])
	assert.Equal(t, int64(8080), static["port"])
	assert.Equal(t, "/mcp", static["path"])
	assert.Equal(t, "StreamableHTTP", static["protocol"])

	// Verify HTTPRoute was created with correct spec.
	route := &unstructured.Unstructured{}
	route.SetGroupVersionKind(httpRouteGVK)
	err = c.Get(ctx, client.ObjectKey{Name: req.Name, Namespace: req.Namespace}, route)
	require.NoError(t, err)

	routeSpec, ok := route.Object["spec"].(map[string]interface{})
	require.True(t, ok)
	parentRefs, ok := routeSpec["parentRefs"].([]interface{})
	require.True(t, ok)
	require.Len(t, parentRefs, 1)
	parentRef := parentRefs[0].(map[string]interface{})
	assert.Equal(t, req.GatewayName, parentRef["name"])
	assert.Equal(t, req.GatewayNamespace, parentRef["namespace"])

	rules, ok := routeSpec["rules"].([]interface{})
	require.True(t, ok)
	require.Len(t, rules, 1)
	rule := rules[0].(map[string]interface{})
	backendRefs, ok := rule["backendRefs"].([]interface{})
	require.True(t, ok)
	backendRef := backendRefs[0].(map[string]interface{})
	assert.Equal(t, req.Name, backendRef["name"])
	assert.Equal(t, agentgatewayGroup, backendRef["group"])
	assert.Equal(t, backendKind, backendRef["kind"])
}

func TestApplyMCPBackend_Update(t *testing.T) {
	ctx := context.Background()

	existing := &unstructured.Unstructured{}
	existing.SetGroupVersionKind(backendGVK)
	existing.SetName("user-my-server")
	existing.SetNamespace("agentgateway-system")
	existing.Object["spec"] = map[string]interface{}{
		"mcp": map[string]interface{}{"targets": []interface{}{}},
	}

	existingRoute := &unstructured.Unstructured{}
	existingRoute.SetGroupVersionKind(httpRouteGVK)
	existingRoute.SetName("user-my-server")
	existingRoute.SetNamespace("agentgateway-system")
	existingRoute.Object["spec"] = map[string]interface{}{}

	c := newFakeClient(existing, existingRoute)

	req := ApplyRequest{
		Name:             "user-my-server",
		Namespace:        "agentgateway-system",
		GatewayName:      "my-gw",
		GatewayNamespace: "agentgateway-system",
		RemoteURL:        "https://new-host.example.com/mcp",
		Protocol:         "SSE",
		Path:             "/user-my-server/mcp",
	}

	err := ApplyMCPBackend(ctx, c, req)
	require.NoError(t, err)

	updated := &unstructured.Unstructured{}
	updated.SetGroupVersionKind(backendGVK)
	err = c.Get(ctx, client.ObjectKey{Name: req.Name, Namespace: req.Namespace}, updated)
	require.NoError(t, err)

	spec := updated.Object["spec"].(map[string]interface{})
	mcpSpec := spec["mcp"].(map[string]interface{})
	targets := mcpSpec["targets"].([]interface{})
	target := targets[0].(map[string]interface{})
	static := target["static"].(map[string]interface{})
	assert.Equal(t, "new-host.example.com", static["host"])
	assert.Equal(t, "SSE", static["protocol"])
}

func TestDeleteMCPBackend(t *testing.T) {
	ctx := context.Background()

	backend := &unstructured.Unstructured{}
	backend.SetGroupVersionKind(backendGVK)
	backend.SetName("user-my-server")
	backend.SetNamespace("agentgateway-system")
	backend.Object["spec"] = map[string]interface{}{}

	route := &unstructured.Unstructured{}
	route.SetGroupVersionKind(httpRouteGVK)
	route.SetName("user-my-server")
	route.SetNamespace("agentgateway-system")
	route.Object["spec"] = map[string]interface{}{}

	c := newFakeClient(backend, route)

	err := DeleteMCPBackend(ctx, c, "user-my-server", "agentgateway-system")
	require.NoError(t, err)

	checkBackend := &unstructured.Unstructured{}
	checkBackend.SetGroupVersionKind(backendGVK)
	err = c.Get(ctx, client.ObjectKey{Name: "user-my-server", Namespace: "agentgateway-system"}, checkBackend)
	assert.True(t, k8serrors.IsNotFound(err), "backend should be deleted")

	checkRoute := &unstructured.Unstructured{}
	checkRoute.SetGroupVersionKind(httpRouteGVK)
	err = c.Get(ctx, client.ObjectKey{Name: "user-my-server", Namespace: "agentgateway-system"}, checkRoute)
	assert.True(t, k8serrors.IsNotFound(err), "route should be deleted")
}

func TestDeleteMCPBackend_NotFound(t *testing.T) {
	ctx := context.Background()
	c := newFakeClient()

	// Should succeed even when resources don't exist.
	err := DeleteMCPBackend(ctx, c, "nonexistent", "default")
	require.NoError(t, err)
}

func TestDeriveMCPPathSuffix(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"dev.servereverything/server", "server"},
		{"user/my-server", "my-server"},
		{"io.example.com/weather-api", "weather-api"},
		{"ns/Name_With_Stuff", "name_with_stuff"},
	}
	for _, tc := range cases {
		got := DeriveMCPPathSuffix(tc.input)
		assert.Equal(t, tc.want, got, "input: %q", tc.input)
	}
}

func TestBuildSSOConfig(t *testing.T) {
	sso, err := BuildSSOConfig(
		"https://tenant.auth0.com/",
		"https://example.com/mcp",
		"Auth0",
		"https://my-gw.ngrok.io",
		"https://tenant.auth0.com/.well-known/jwks.json",
		"dev.servereverything/server",
	)
	require.NoError(t, err)
	assert.Equal(t, "https://tenant.auth0.com/", sso.Issuer)
	assert.Equal(t, "https://example.com/mcp", sso.Audience)
	assert.Equal(t, "Auth0", sso.Provider)
	assert.Equal(t, "https://my-gw.ngrok.io", sso.PublicBaseURL)
	assert.Equal(t, "server", sso.MCPPathSuffix)
	assert.Equal(t, "tenant.auth0.com", sso.JWKSHost)
	assert.Equal(t, int32(443), sso.JWKSPort)
	assert.Equal(t, ".well-known/jwks.json", sso.JWKSPath)
	assert.Equal(t, "https://my-gw.ngrok.io/server/mcp", sso.ResourceBaseURL)
}

func TestBuildSSOConfig_HandlesTrailingSlash(t *testing.T) {
	sso, err := BuildSSOConfig(
		"https://tenant.auth0.com/",
		"https://example.com/mcp",
		"Auth0",
		"https://my-gw.ngrok.io/",
		"https://tenant.auth0.com/.well-known/jwks.json",
		"dev.servereverything/server",
	)
	require.NoError(t, err)
	assert.Equal(t, "https://my-gw.ngrok.io", sso.PublicBaseURL)
}

func TestBuildSSOConfig_InvalidJWKSURL(t *testing.T) {
	_, err := BuildSSOConfig(
		"https://tenant.auth0.com/",
		"https://example.com/mcp",
		"Auth0",
		"https://my-gw.ngrok.io",
		"://invalid",
		"dev.servereverything/server",
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid JWKS URL")
}

func TestRenderSSOManifests(t *testing.T) {
	req := ApplyRequest{
		Name:             "dev-servereverything-server",
		Namespace:        "agentgateway-system",
		GatewayName:      "agentgateway",
		GatewayNamespace: "agentgateway-system",
		RemoteURL:        "https://mcp.example.com/mcp",
		Protocol:         "StreamableHTTP",
		Path:             "/server/mcp",
	}
	sso := SSOConfig{
		Issuer:          "https://tenant.auth0.com/",
		Audience:        "https://example.com/mcp",
		Provider:        "Auth0",
		PublicBaseURL:   "https://my-gw.ngrok.io",
		JWKSURL:         "https://tenant.auth0.com/.well-known/jwks.json",
		MCPPathSuffix:   "server",
		JWKSHost:        "tenant.auth0.com",
		JWKSPort:        443,
		JWKSPath:        ".well-known/jwks.json",
		ResourceBaseURL: "https://my-gw.ngrok.io/server/mcp",
	}

	yaml, err := RenderSSOManifests(req, sso)
	require.NoError(t, err)

	docs := strings.Split(strings.TrimSuffix(yaml, "\n"), "\n---\n")
	require.Len(t, docs, 4, "expected 4 YAML documents")

	// Document 1: HTTPRoute
	assert.Contains(t, docs[0], "kind: HTTPRoute")
	assert.Contains(t, docs[0], "name: dev-servereverything-server")
	assert.Contains(t, docs[0], "value: /server/mcp")
	assert.Contains(t, docs[0], "/.well-known/oauth-protected-resource/server/mcp")
	assert.Contains(t, docs[0], "/.well-known/oauth-authorization-server/server/mcp")

	// Document 2: MCP AgentgatewayBackend with TLS
	assert.Contains(t, docs[1], "kind: AgentgatewayBackend")
	assert.Contains(t, docs[1], "name: dev-servereverything-server")
	assert.Contains(t, docs[1], "policies:")
	assert.Contains(t, docs[1], "tls:")

	// Document 3: JWKS AgentgatewayBackend
	assert.Contains(t, docs[2], "kind: AgentgatewayBackend")
	assert.Contains(t, docs[2], "name: dev-servereverything-server-jwks")
	assert.Contains(t, docs[2], "tenant.auth0.com")

	// Document 4: EnterpriseAgentgatewayPolicy
	assert.Contains(t, docs[3], "kind: EnterpriseAgentgatewayPolicy")
	assert.Contains(t, docs[3], "name: dev-servereverything-server-policy")
	assert.Contains(t, docs[3], "issuer: https://tenant.auth0.com/")
	assert.Contains(t, docs[3], "dev-servereverything-server-jwks")
}

func TestApplySSOResources(t *testing.T) {
	ctx := context.Background()
	c := newFakeClient()

	req := ApplyRequest{
		Name:             "dev-servereverything-server",
		Namespace:        "agentgateway-system",
		GatewayName:      "agentgateway",
		GatewayNamespace: "agentgateway-system",
		RemoteURL:        "https://mcp.example.com/mcp",
		Protocol:         "StreamableHTTP",
		Path:             "/server/mcp",
	}
	sso := SSOConfig{
		Issuer:          "https://tenant.auth0.com/",
		Audience:        "https://example.com/mcp",
		Provider:        "Auth0",
		PublicBaseURL:   "https://my-gw.ngrok.io",
		JWKSURL:         "https://tenant.auth0.com/.well-known/jwks.json",
		MCPPathSuffix:   "server",
		JWKSHost:        "tenant.auth0.com",
		JWKSPort:        443,
		JWKSPath:        ".well-known/jwks.json",
		ResourceBaseURL: "https://my-gw.ngrok.io/server/mcp",
	}

	err := ApplySSOResources(ctx, c, req, sso)
	require.NoError(t, err)

	// Verify MCP backend has TLS in target static
	backend := &unstructured.Unstructured{}
	backend.SetGroupVersionKind(backendGVK)
	err = c.Get(ctx, client.ObjectKey{Name: req.Name, Namespace: req.Namespace}, backend)
	require.NoError(t, err)
	spec := backend.Object["spec"].(map[string]interface{})
	mcpSpec := spec["mcp"].(map[string]interface{})
	targets := mcpSpec["targets"].([]interface{})
	require.Len(t, targets, 1)
	static := targets[0].(map[string]interface{})["static"].(map[string]interface{})
	policies, ok := static["policies"].(map[string]interface{})
	require.True(t, ok)
	_, hasTLS := policies["tls"]
	assert.True(t, hasTLS, "MCP backend target must have policies.tls")
}
