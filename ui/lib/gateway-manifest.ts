// Pure functions for generating AgentgatewayBackend + HTTPRoute YAML manifests.
// Mirrors internal/k8s/agentgateway.go exactly.

export interface GenerateManifestsParams {
  serverName: string
  gwName: string
  gwNamespace: string
  namespace: string
  url: string
  protocol: 'StreamableHTTP' | 'SSE'
}

/** Mirrors k8s.SanitizeName in internal/k8s/agentgateway.go */
export function sanitizeName(name: string): string {
  let n = name.toLowerCase().replace(/[/.]/g, '-').replace(/[^a-z0-9-]+/g, '-').replace(/^-+|-+$/g, '')
  if (n.length > 63) {
    n = n.slice(0, 63).replace(/-+$/, '')
  }
  return n
}

/** Mirrors k8s.DeriveMCPPathSuffix in internal/k8s/agentgateway.go */
export function deriveMCPPathSuffix(serverName: string): string {
  const idx = serverName.indexOf('/')
  if (idx < 0) return ''
  let suffix = serverName.slice(idx + 1).trim()
  if (!suffix) return ''
  suffix = suffix.toLowerCase().replace(/[^a-z0-9\-_]+/g, '-').replace(/^-+|-+$/g, '')
  return suffix
}

/** Mirrors parseRemoteURL in internal/k8s/agentgateway.go */
export function parseRemoteURL(rawURL: string): { host: string; port: number; path: string } {
  const u = new URL(rawURL)
  const port = u.port ? parseInt(u.port, 10) : (u.protocol === 'https:' ? 443 : 80)
  return { host: u.hostname, port, path: u.pathname || '/mcp' }
}

function defaultMCPPath(protocol: 'StreamableHTTP' | 'SSE'): string {
  return protocol === 'SSE' ? '/sse' : '/mcp'
}

/**
 * Generates YAML for AgentgatewayBackend + HTTPRoute.
 * Mirrors k8s.RenderManifests in internal/k8s/agentgateway.go.
 * Returns "---\n<backend YAML>---\n<route YAML>"
 */
export function generateManifests(params: GenerateManifestsParams): string {
  const { serverName, gwName, gwNamespace, namespace, url, protocol } = params
  const name = sanitizeName(serverName)
  const pathSuffix = deriveMCPPathSuffix(serverName)
  if (!pathSuffix) throw new Error("server name must contain '/' (e.g. namespace/name)")
  let parsed: { host: string; port: number; path: string }
  try {
    parsed = parseRemoteURL(url)
  } catch {
    throw new Error(`Invalid URL: ${url}`)
  }
  const urlPath = parsed.path || defaultMCPPath(protocol)

  const backendYAML = renderBackend({ name, namespace, host: parsed.host, port: parsed.port, urlPath, protocol })
  const routeYAML = renderHTTPRoute({ name, namespace, gwName, gwNamespace, path: `/${pathSuffix}/mcp` })

  return '---\n' + backendYAML + '---\n' + routeYAML
}

function renderBackend(p: {
  name: string
  namespace: string
  host: string
  port: number
  urlPath: string
  protocol: string
}): string {
  return [
    `apiVersion: agentgateway.dev/v1alpha1`,
    `kind: AgentgatewayBackend`,
    `metadata:`,
    `  name: ${p.name}`,
    `  namespace: ${p.namespace}`,
    `spec:`,
    `  mcp:`,
    `    targets:`,
    `    - name: ${p.name}-target`,
    `      static:`,
    `        host: ${p.host}`,
    `        port: ${p.port}`,
    `        path: ${p.urlPath}`,
    `        protocol: ${p.protocol}`,
    ``,
  ].join('\n')
}

function renderHTTPRoute(p: {
  name: string
  namespace: string
  gwName: string
  gwNamespace: string
  path: string
}): string {
  return [
    `apiVersion: gateway.networking.k8s.io/v1`,
    `kind: HTTPRoute`,
    `metadata:`,
    `  name: ${p.name}`,
    `  namespace: ${p.namespace}`,
    `spec:`,
    `  parentRefs:`,
    `  - name: ${p.gwName}`,
    `    namespace: ${p.gwNamespace}`,
    `  rules:`,
    `  - matches:`,
    `    - path:`,
    `        type: PathPrefix`,
    `        value: ${p.path}`,
    `    backendRefs:`,
    `    - name: ${p.name}`,
    `      group: agentgateway.dev`,
    `      kind: AgentgatewayBackend`,
    ``,
  ].join('\n')
}
