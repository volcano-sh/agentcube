# AgentCube MCP Server Deployment

This directory contains manifests for deploying the AgentCube MCP Server in Kubernetes.

## Prerequisites

- AgentCube cluster (WorkloadManager and Router) should be running.
- The `agentcube-mcp-server` image should be built and available in your registry (or loaded into Kind).

To build the image:
```bash
make docker-build-mcp
```

## Deployment Options

### 1. Kubernetes Deployment

Apply the deployment manifest:

```bash
kubectl apply -f deployment.yaml
```

This will create a Deployment and a Service for the MCP server. The server will be accessible at `http://agentcube-mcp-server.agentcube.svc.cluster.local:8000`.

#### Configuration

The following environment variables can be configured in `deployment.yaml`:

- `WORKLOAD_MANAGER_URL`: URL of the AgentCube WorkloadManager.
- `ROUTER_URL`: URL of the AgentCube Router.
- `NAMESPACE`: Kubernetes namespace where AgentCube is running.
- `AUTH_TOKEN`: (Optional) Auth token for Kubernetes/WorkloadManager.

### 2. Local Deployment

You can run the MCP server locally using the Python SDK:

```bash
cd sdk-python
pip install -e .
python -m agentcube.mcp_server
```

By default, it uses `stdio` transport. You can also run it with `streamable-http` transport:

```bash
python -m agentcube.mcp_server --transport streamable-http --port 8000
```

Ensure `WORKLOAD_MANAGER_URL` and `ROUTER_URL` are set in your environment or passed as arguments if running programmatically.
