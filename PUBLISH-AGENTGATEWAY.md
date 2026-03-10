# Guide: Publishing a Remote MCP Server to AgentGateway

This guide walks through registering GitHub's MCP server with the AgentRegistry and exposing it through an AgentGateway instance running in Kubernetes.

You can generate the required Kubernetes manifests in two ways:

- **UI** — click the Network icon on any server card (or open the "Gateway" tab in server detail) to generate, copy, or download the YAML directly from the browser. No CLI or `kubectl` required.
- **CLI** — use `arctl mcp deploy agentgateway <server-name> --gateway <gw> --dry-run` to generate manifests, or omit `--dry-run` to apply them directly to the cluster.

---

## Prerequisites

- `arctl` installed and the local registry running (`arctl` starts it automatically)
- A Kubernetes cluster with AgentGateway installed and a `Gateway` resource already created
- `kubectl` configured pointing at that cluster (CLI path only)

---

---

## Option A — Generate Manifests via the UI

If you prefer not to use the CLI or don't have `kubectl` handy, the AgentRegistry UI lets you generate gateway manifests entirely in the browser.

### A1 — Open the UI

Start the registry (which also serves the UI):

```bash
set -a && source .env && set +a && ./bin/arctl-server
```

Verify it's up:

```bash
curl http://localhost:12121/v0/health
# expected: {"status":"ok"}
```

### A2 — Export Manifests from a Server Card

1. On the **Servers** tab, find the server you want to expose.
2. Click the **Network icon** (⬡) on the server card.
3. In the **Export Gateway Manifests** dialog:
   - **Gateway Name** — name of your existing `Gateway` resource (e.g. `my-gw`)
   - **Gateway Namespace** — namespace the `Gateway` lives in (e.g. `agentgateway-system`)
   - **Resource Namespace** — namespace where the `AgentgatewayBackend` and `HTTPRoute` will be created
   - **Remote URL** — pre-filled from the server's remotes if available; otherwise enter the MCP endpoint
   - **Protocol** — `StreamableHTTP` or `SSE`
4. Click **Generate Manifests**.
5. Use **Copy** to paste into your terminal, or **Download** to save a `.yaml` file.

### A3 — Export from Server Detail

Alternatively, click a server card to open the detail view, then select the **Gateway** tab for the same form inline.

### A4 — Apply the Manifests

```bash
kubectl apply -f com-githubcopilot-github-mcp-server-gateway.yaml
```

---

## Option B — CLI

## Step 1 — Find your Gateway

First, check what gateways are available in your cluster:

```bash
arctl mcp deploy agentgateway list-gateways --namespace agentgateway-system
```

Example output:
```
NAME                           NAMESPACE            CLASS                          LISTENERS
my-gw                          agentgateway-system  agentgateway                   HTTP:80, HTTPS:443
```

Note the name and namespace — you'll use them in the next steps.

---

## Step 2 — Publish the Remote MCP Server

Register GitHub's MCP server with the registry. No local files needed.

The server name must use a **reverse-DNS namespace** that matches the remote URL's domain. For `https://api.githubcopilot.com/mcp`, the domain is `githubcopilot.com`, so use namespace `com.githubcopilot` (reversed). Do not use `github/github-mcp-server` — that format is invalid.

```bash
arctl mcp publish com.githubcopilot/github-mcp-server \
  --remote-url https://api.githubcopilot.com/mcp \
  --transport streamable-http \
  --description "GitHub's official MCP server (Copilot, repos, issues, PRs)" \
  --version 1.0.0
```

Example output:
```
Publishing remote MCP server: com.githubcopilot/github-mcp-server
Server published successfully: com.githubcopilot/github-mcp-server
```

The server is now registered in the registry. You can verify:

```bash
arctl list
```

---

## Step 3 — Preview the k8s Manifests (dry-run)

Before touching the cluster, generate the manifests to review them:

```bash
arctl mcp deploy agentgateway com.githubcopilot/github-mcp-server \
  --gateway my-gw \
  --gateway-namespace agentgateway-system \
  --dry-run
```

Output:
```yaml
---
apiVersion: agentgateway.dev/v1alpha1
kind: AgentgatewayBackend
metadata:
  name: com-githubcopilot-github-mcp-server
  namespace: agentgateway-system
spec:
  mcp:
    targets:
    - name: com-githubcopilot-github-mcp-server-target
      static:
        host: api.githubcopilot.com
        port: 443
        path: /mcp
        protocol: StreamableHTTP
---
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: com-githubcopilot-github-mcp-server
  namespace: agentgateway-system
spec:
  parentRefs:
  - name: my-gw
    namespace: agentgateway-system
  rules:
  - matches:
    - path:
        type: PathPrefix
        value: /com-githubcopilot-github-mcp-server/mcp
    backendRefs:
    - name: com-githubcopilot-github-mcp-server
      group: agentgateway.dev
      kind: AgentgatewayBackend
```

---

## Step 4 — Save and Apply (GitOps style)

Pipe the output to a file, review it, then apply:

```bash
arctl mcp deploy agentgateway com.githubcopilot/github-mcp-server \
  --gateway my-gw \
  --gateway-namespace agentgateway-system \
  --dry-run > k8s/github-mcp-server.yaml

# Review
cat k8s/github-mcp-server.yaml

# Apply
kubectl apply -f k8s/github-mcp-server.yaml
```

---

## Step 5 — Apply Directly (optional)

Or skip the file and let `arctl` apply directly to the cluster:

```bash
arctl mcp deploy agentgateway com.githubcopilot/github-mcp-server \
  --gateway my-gw \
  --gateway-namespace agentgateway-system
```

Output:
```
Deployed AgentGateway config for com.githubcopilot/github-mcp-server at /com-githubcopilot-github-mcp-server/mcp via gateway my-gw
```

---

## Step 6 — Verify

```bash
kubectl get agentgatewaybackend -n agentgateway-system
kubectl get httproute -n agentgateway-system
```

The server is now reachable through the gateway at:
```
http://<gateway-ip>/com-githubcopilot-github-mcp-server/mcp
```

---

## Step 7 — Clean Up

To remove the gateway resources when you no longer need them, delete the resources manually:

```bash
kubectl delete agentgatewaybackend com-githubcopilot-github-mcp-server -n agentgateway-system
kubectl delete httproute com-githubcopilot-github-mcp-server -n agentgateway-system
```

---

## Quick Reference

### UI

| Goal | Action |
|------|--------|
| Generate manifests for a server | Click the Network icon on the server card |
| Generate manifests from detail view | Open server → Gateway tab |
| Copy YAML to clipboard | Click **Copy** after generating |
| Download YAML file | Click **Download** after generating |

### CLI

| Goal | Command |
|------|---------|
| List available gateways | `arctl mcp deploy agentgateway list-gateways --namespace <ns>` |
| Publish remote server | `arctl mcp publish <name> --remote-url <url> --transport streamable-http` |
| Preview k8s manifests | `arctl mcp deploy agentgateway <server> --gateway <gw> --dry-run` |
| Save manifests to file | `arctl mcp deploy agentgateway <server> --gateway <gw> --dry-run > manifests.yaml` |
| Apply directly to cluster | `arctl mcp deploy agentgateway <server> --gateway <gw>` |
| Remove gateway resources | `kubectl delete agentgatewaybackend,httproute <sanitized-name> -n <ns>` |
