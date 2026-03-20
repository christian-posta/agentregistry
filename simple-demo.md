# AgentRegistry Local Demo

## Prerequisites

- Docker Desktop running
- Go 1.25+
- Node.js / npm (for UI build)

---

## Start the Demo

### 1. Start PostgreSQL

> Note: Uses port 5433 to avoid conflicts with any existing postgres on 5432.

```bash
docker run -d \
  --name agent-registry-postgres \
  -e POSTGRES_DB=agent-registry \
  -e POSTGRES_USER=agentregistry \
  -e POSTGRES_PASSWORD=agentregistry \
  -p 5433:5432 \
  pgvector/pgvector:pg16
```

### 2. Build the CLI and server

```bash
make build-cli
make build-ui
make build-server   
```

### 3. Configure the environment

```bash
cp .env.example .env
```

Edit `.env` and set these values:

```
AGENT_REGISTRY_SERVER_ADDRESS=:12121
AGENT_REGISTRY_DATABASE_URL=postgres://agentregistry:agentregistry@localhost:5433/agent-registry?sslmode=disable
AGENT_REGISTRY_ENABLE_ANONYMOUS_AUTH=true
```

### 4. Start the server

```bash
set -a && source .env && set +a && ./bin/arctl-server
```

Verify it's up:

```bash
curl http://localhost:12121/v0/health
# expected: {"status":"ok"}
```

### 5. Import seed data (363 MCP servers)

```bash
AGENT_REGISTRY_DATABASE_URL=postgres://agentregistry:agentregistry@localhost:5433/agent-registry?sslmode=disable \
  ./bin/arctl import --source internal/registry/seed/seed.json --skip-validation
```

### 6. Verify

```bash
./bin/arctl mcp list
```

Open the web UI: http://localhost:12121

---

## Register a Remote MCP Server

Use `--remote-url` for servers already deployed in the cloud (e.g. Databricks, hosted SaaS).
No `--type` or `--package-id` needed.

The server name must use a **reverse-DNS namespace** matching the remote URL's domain.
For example, `https://my-workspace.cloud.databricks.com/mcp` → namespace `com.databricks.cloud`.

```bash
./bin/arctl mcp publish com.databricks.cloud/unity-catalog \
  --remote-url https://my-workspace.cloud.databricks.com/mcp \
  --version 1.0.0 \
  --description "Databricks Unity Catalog MCP server"
```



To remove that server from the registry (name and version must match what you published):

```bash
./bin/arctl mcp delete com.databricks.cloud/unity-catalog --version 1.0.0
```

Use `--dry-run` to preview without committing:

```bash
./bin/arctl mcp publish com.databricks.cloud/unity-catalog \
  --remote-url https://my-workspace.cloud.databricks.com/mcp \
  --version 1.0.0 \
  --description "Databricks Unity Catalog MCP server" \
  --dry-run
```

For SSE transport (default is streamable-http):

```bash
./bin/arctl mcp publish com.example/my-server \
  --remote-url https://my-server.example.com/sse \
  --transport sse \
  --version 1.0.0 \
  --description "My SSE MCP server"
```

---

## Register a Package-Based MCP Server

For servers distributed via npm, PyPI, or OCI/Docker, use `--type` and `--package-id`:

```bash
# npm
./bin/arctl mcp publish myorg/filesystem-server \
  --type npm \
  --package-id @modelcontextprotocol/server-filesystem \
  --version 1.0.0 \
  --description "Filesystem MCP server"

# PyPI
./bin/arctl mcp publish myorg/my-server \
  --type pypi \
  --package-id mcp-server-time \
  --version 1.0.0 \
  --description "Time MCP server"

# OCI/Docker
./bin/arctl mcp publish myorg/my-server \
  --type oci \
  --package-id docker.io/myorg/my-server:1.0.0 \
  --version 1.0.0 \
  --description "My MCP server"
```

---

## Delete an MCP server

Removal is by **server name** (`namespace/name`) and **`--version`** (required)—same values you used when publishing.

```bash
./bin/arctl mcp delete <namespace/name> --version <version>
```

For example, after the npm publish above:

```bash
./bin/arctl mcp delete myorg/filesystem-server --version 1.0.0
```

---

## Shut Down

```bash
# Stop the server
pkill -f arctl-server

# Stop and remove postgres (data is lost)
docker rm -f agent-registry-postgres
```

To keep data for next time, stop without removing:

```bash
pkill -f arctl-server
docker stop agent-registry-postgres
```

### Restart from a stopped state

```bash
docker start agent-registry-postgres
set -a && source .env && set +a && ./bin/arctl-server &
```


## Kubecon demo

Make sure `arctl` is in the PATH

```bash
arctl mcp publish dev.servereverything/server \
  --remote-url https://servereverything.dev/mcp \
  --version 1.0.0 \
  --description "A public, remote server-everything"
```

To delete and reset the demo:


```bash
arctl mcp delete dev.servereverything/server --version 1.0.0   
```

We can list gateways:

```bash
arctl mcp deploy agentgateway list-gateways --namespace agentgateway-system
```

preview the resources:

```bash
arctl mcp deploy agentgateway dev.servereverything/server \
  --gateway agentgateway \
  --gateway-namespace agentgateway-system \
  --dry-run
```

Or skip `--dry-run` to apply to the cluster. 

# Ship with SSO enabled:

```bash
arctl mcp deploy agentgateway dev.servereverything/server \
  --gateway agentgateway \
  --gateway-namespace agentgateway-system \
  --sso \
  --dry-run | k apply -f -
```



## Dev

if you want to run the UI locally while making changes, run the backend server and then the ui:

```bash
./bin/arctl-server

# in another terminal
make dev-ui
```

if you like wht you see:

```bash
make build-ui
make build-server
make build-cli
```