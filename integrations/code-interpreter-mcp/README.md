# AgentCube Code Interpreter MCP Server

[MCP](https://modelcontextprotocol.io/) server that exposes the AgentCube code interpreter (Router + Workload Manager) as tools for Cursor, Claude Desktop, and other hosts.

**You need a running AgentCube cluster and a `CodeInterpreter` CR before this server is useful.** Follow the steps below in order.

---

## 0. Deploy AgentCube (same path as the project tutorial)

Do this once on a Kubernetes cluster (v1.24+). Full detail: [`docs/getting-started.md`](../../docs/getting-started.md).

1. **agent-sandbox** — install CRDs + controller (see getting-started **Step 1**).
2. **Redis** — create namespace `agentcube`, deploy Redis ( **Step 2** ).
3. **AgentCube** — `git clone` the repo, then `helm install agentcube ./manifests/charts/base ...` ( **Step 3** ).
4. **CodeInterpreter** — e.g. `kubectl apply -f example/code-interpreter/code-interpreter.yaml` ( **Step 4** ). That manifest creates `my-interpreter` in namespace **`default`**.

Check:

```bash
kubectl get pods -n agentcube
kubectl get codeinterpreter
```

---

## 1. Install the MCP server (your machine)

Python 3.10+. The server calls the Python SDK at runtime, so install **both** the SDK and this package.

**From a clone of this repository** (recommended while developing):

```bash
cd /path/to/agentcube
pip install -e ./sdk-python
pip install -e ./integrations/code-interpreter-mcp
```

**Released SDK only** (if you publish or install `agentcube-sdk` from PyPI): install the SDK, then install this package from the same repo revision so versions stay compatible.

---

## 2. Point the server at Router + Workload Manager

From your laptop, port-forward (same pattern as [getting-started **Step 5**](../../docs/getting-started.md)):

```bash
# Terminal A
kubectl port-forward -n agentcube svc/workloadmanager 8080:8080

# Terminal B — local 8081 → Router service port 8080 (avoids clashing with 8080)
kubectl port-forward -n agentcube svc/agentcube-router 8081:8080
```

Then:

```bash
export WORKLOAD_MANAGER_URL="http://localhost:8080"
export ROUTER_URL="http://localhost:8081"
```

If your cluster enforces auth for those APIs, set `API_TOKEN` (Bearer) the same way as the Python SDK.

---

## 3. Run the MCP server

**stdio** (typical for Cursor / Claude Desktop):

```bash
python -m agentcube_code_interpreter_mcp --transport stdio
```

**Streamable HTTP** (debugging; default path `/mcp` per the Python MCP SDK):

```bash
python -m agentcube_code_interpreter_mcp --transport streamable-http --host 0.0.0.0 --port 8000
```

---

## 4. Example: Cursor `mcp.json`

Match **namespace** and **interpreter name** to your `CodeInterpreter` CR. For `example/code-interpreter/code-interpreter.yaml` (`my-interpreter` in `default`):

```json
{
  "mcpServers": {
    "agentcube-code-interpreter": {
      "command": "python",
      "args": ["-m", "agentcube_code_interpreter_mcp", "--transport", "stdio"],
      "env": {
        "ROUTER_URL": "http://localhost:8081",
        "WORKLOAD_MANAGER_URL": "http://localhost:8080",
        "AGENTCUBE_NAMESPACE": "default",
        "CODE_INTERPRETER_NAME": "my-interpreter"
      }
    }
  }
}
```

If the CR name is not `my-interpreter`, set `CODE_INTERPRETER_NAME` accordingly. Keep the two `kubectl port-forward` terminals open while you use the IDE.

---

## 5. Optional: run the MCP server inside the cluster

Build from **repository root** (Docker context must include `sdk-python`):

```bash
docker build -f integrations/code-interpreter-mcp/Dockerfile -t agentcube-code-interpreter-mcp:latest .
kubectl apply -f integrations/code-interpreter-mcp/deployment.yaml
```

Edit `deployment.yaml`: `ROUTER_URL` / `WORKLOAD_MANAGER_URL` must match your Services (defaults target `agentcube` namespace). Set `CODE_INTERPRETER_NAME` to your CR name.

**Replicas:** with `session_reuse=true`, clients are cached in process memory — use **one** replica or sticky routing at your gateway.

---

## Configuration reference

| Variable | Description |
|----------|-------------|
| `ROUTER_URL` | **Required.** Router base URL |
| `WORKLOAD_MANAGER_URL` | **Required.** Workload Manager base URL |
| `API_TOKEN` | Optional Bearer token (same as SDK) |
| `AGENTCUBE_NAMESPACE` | Namespace for API paths; default `default` |
| `CODE_INTERPRETER_NAME` | CR name; default `my-interpreter` |
| `CODE_INTERPRETER_SESSION_TTL` | New session TTL (seconds); default `3600` |
| `MCP_TRANSPORT` | `stdio` or `streamable-http` (or `--transport` on CLI) |
| `MCP_HOST` / `MCP_PORT` | Listen address for HTTP mode |

---

## Tools

Names match **`CodeInterpreterClient`**: `run_code`, `execute_command`, `write_file`, `list_files`, `upload_file`, `download_file`, and `stop_session` (call when finished after `session_reuse=true`). Optional `session_id` / `session_reuse` on tools.

`upload_file` / `download_file` paths are on the **host that runs** the MCP process (not inside the sandbox).
