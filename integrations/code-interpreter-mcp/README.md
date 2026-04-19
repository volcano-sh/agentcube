# AgentCube Code Interpreter MCP Server

A [Model Context Protocol](https://modelcontextprotocol.io/) server that exposes the AgentCube code interpreter (via the **Router** data plane and **Workload Manager** control plane) as MCP **tools**, so hosts such as Cursor or Claude Desktop can integrate without custom HTTP glue.

## Configuration (environment variables)

| Variable | Description |
|----------|-------------|
| `ROUTER_URL` | **Required.** AgentCube Router base URL, e.g. `http://localhost:8081` |
| `WORKLOAD_MANAGER_URL` | **Required.** Workload Manager base URL, e.g. `http://localhost:8080` |
| `API_TOKEN` | Optional. Bearer token for Workload Manager / Kubernetes (same semantics as the Python SDK) |
| `AGENTCUBE_NAMESPACE` | Default `default` |
| `CODE_INTERPRETER_NAME` | CodeInterpreter CRD name; default `my-interpreter` (E2E uses `e2e-code-interpreter`) |
| `CODE_INTERPRETER_SESSION_TTL` | New session TTL in seconds; default `3600` |
| `MCP_TRANSPORT` | `stdio` or `streamable-http` (or pass `--transport` on the CLI) |
| `MCP_HOST` / `MCP_PORT` | Listen address for HTTP mode |

## Run locally

1. Install the SDK and this package:

```bash
pip install -e ./sdk-python
pip install -e ./integrations/code-interpreter-mcp
```

2. **stdio** (typical for local MCP hosts such as Cursor):

```bash
export ROUTER_URL=http://localhost:8081
export WORKLOAD_MANAGER_URL=http://localhost:8080
export API_TOKEN=...   # if your environment requires it
python -m agentcube_code_interpreter_mcp --transport stdio
```

Example Cursor `mcp.json`:

```json
{
  "mcpServers": {
    "agentcube-code-interpreter": {
      "command": "python",
      "args": ["-m", "agentcube_code_interpreter_mcp", "--transport", "stdio"],
      "env": {
        "ROUTER_URL": "http://localhost:8081",
        "WORKLOAD_MANAGER_URL": "http://localhost:8080",
        "CODE_INTERPRETER_NAME": "e2e-code-interpreter",
        "AGENTCUBE_NAMESPACE": "agentcube"
      }
    }
  }
}
```

3. **Streamable HTTP** (debugging or remote access):

```bash
python -m agentcube_code_interpreter_mcp --transport streamable-http --host 0.0.0.0 --port 8000
```

The default MCP HTTP path is `/mcp` (per the official Python MCP SDK).

## Kubernetes

Build from the repository root (the context must include `sdk-python`):

```bash
docker build -f integrations/code-interpreter-mcp/Dockerfile -t agentcube-code-interpreter-mcp:latest .
kubectl apply -f integrations/code-interpreter-mcp/deployment.yaml
```

Edit `ROUTER_URL` and `WORKLOAD_MANAGER_URL` in the Deployment to match your cluster Services. **Session affinity:** with `session_reuse=true`, the process caches `CodeInterpreterClient` instances in memory—use **one replica** or configure sticky sessions at your gateway.

## Tools

Aligned with public methods on Python **`CodeInterpreterClient`** for predictable naming:

- `run_code`, `execute_command`, `write_file`, `list_files` — same names as the SDK methods
- `stop_session` — ends a session (maps to `CodeInterpreterClient.stop()`); call when you are done after `session_reuse=true`

All tools accept optional `session_id` and `session_reuse`, similar to the Dify plugin’s code / command / session semantics.
