# Browser Agent with Playwright MCP Tool

> An AI-powered browser agent that handles web search and analysis requests,
> using the official [Playwright MCP](https://github.com/microsoft/playwright-mcp)
> tool running in an isolated AgentCube sandbox.

## Architecture

```
┌───────────────┐        ┌────────────────┐        ┌───────────────────────────────┐
│    Client      │──HTTP──▶  Browser Agent  │──HTTP──▶  Router (AgentCube)           │
│  (curl/SDK)    │        │  (Deployment)   │        │  session mgmt + JWT + proxy   │
└───────────────┘        └────────────────┘        └───────────────┬───────────────┘
                                                                   │ reverse proxy
                                                   ┌───────────────▼───────────────┐
                                                   │  Playwright MCP Tool (sandbox) │
                                                   │  AgentRuntime microVM pod      │
                                                   │  official MCP browser service  │
                                                   └───────────────────────────────┘
```

### Components

| Component | Type | Image | Description |
|-----------|------|-------|-------------|
| **Playwright MCP Tool** | `AgentRuntime` CRD | `mcr.microsoft.com/playwright/mcp:latest` | Official Playwright MCP container from Microsoft. Runs as a real browser tool server in the sandbox, not as a custom in-repo agent. |
| **Browser Agent** | `Deployment` | `browser-agent:latest` | LLM-powered orchestrator that receives user requests, plans browser tasks, and calls the Playwright MCP tool via the AgentCube Router. |

### How It Works

1. **User sends a request** (e.g., "Search for the latest Kubernetes release notes")
2. **Browser Agent** uses an LLM to plan a concrete browser task  
3. **Browser Agent** connects to the Playwright MCP tool via the AgentCube Router  
4. **Router** provisions a sandbox pod (or reuses an existing session), signs a JWT, and proxies the request  
5. **Playwright MCP Tool** inside the sandbox exposes browser automation tools over MCP  
6. **Browser Agent** summarizes the result using the LLM and returns it to the user  

Session reuse: the `session_id` returned in the first response can be passed in subsequent requests to reuse the same browser sandbox. The MCP server is started with `--shared-browser-context`, so repeated requests can keep the same browser state inside that sandbox.

## Prerequisites

- AgentCube deployed in a Kubernetes cluster (Router + Workload Manager running)
- An OpenAI-compatible LLM API key
- `kubectl` configured to access the cluster

## Quick Start

### 1. Create the API key secret

```bash
kubectl create secret generic browser-agent-secrets \
  --from-literal=openai-api-key=<YOUR_API_KEY>
```

### 2. Deploy the Playwright MCP Tool (AgentRuntime)

```bash
# Create the AgentRuntime CRD using the official Microsoft image
kubectl apply -f example/browser-agent/browser-use-tool.yaml
```

### 3. Deploy the Browser Agent

```bash
# Build the agent image (from repo root)
docker build -t browser-agent:latest \
  -f example/browser-agent/Dockerfile .

# Deploy
kubectl apply -f example/browser-agent/deployment.yaml
```

### 4. Test

```bash
# Port-forward to the agent
kubectl port-forward deploy/browser-agent 8000:8000

# Send a search request
curl -s http://localhost:8000/chat \
  -H 'Content-Type: application/json' \
  -d '{"message": "Search for the latest news about Kubernetes 1.33 release"}' \
  | python -m json.tool

# Reuse the same browser session (pass session_id from previous response)
curl -s http://localhost:8000/chat \
  -H 'Content-Type: application/json' \
  -d '{"message": "Now find the Patch Releases list from the same release", "session_id": "<SESSION_ID>"}' \
  | python -m json.tool
```

## Configuration

### Browser Agent (Deployment)

| Env Var | Default | Description |
|---------|---------|-------------|
| `OPENAI_API_KEY` | (required) | LLM API key |
| `OPENAI_API_BASE` | `https://api.openai.com/v1` | LLM API base URL |
| `OPENAI_MODEL` | `gpt-4o` | LLM model name |
| `ROUTER_URL` | `http://router.agentcube.svc.cluster.local:8080` | AgentCube Router URL |
| `PLAYWRIGHT_MCP_NAME` | `browser-use-tool` | Name of the Playwright MCP AgentRuntime CRD |
| `PLAYWRIGHT_MCP_NAMESPACE` | `default` | Namespace of the AgentRuntime |
| `BROWSER_TASK_TIMEOUT` | `300` | Timeout (seconds) for browser task execution |
| `MAX_TOOL_ROUNDS` | `10` | Maximum LLM-to-tool interaction rounds |

### Playwright MCP Tool (AgentRuntime)

| Env Var | Default | Description |
|---------|---------|-------------|
| `--port` | `8931` | MCP HTTP endpoint port |
| `--host` | `0.0.0.0` | Bind address |
| `--shared-browser-context` | enabled | Reuse the same browser context for repeat clients in the same sandbox |
| `--caps=vision` | enabled | Coordinate-based actions and screenshots |

## Files

```
example/browser-agent/
├── README.md                   # This file
├── browser_agent.py            # Browser Agent: LLM planner + MCP client
├── browser-use-tool.yaml       # AgentRuntime CRD for the Playwright MCP tool
├── deployment.yaml             # K8s Deployment for the browser agent
├── Dockerfile                  # Dockerfile for browser agent
├── requirements.txt            # Python deps for browser agent
```

## Why This Design

- `playwright-python` is a library, not a tool server. By itself it does not give AgentCube an MCP or HTTP endpoint to proxy.
- `microsoft/playwright-mcp` is already a real browser tool server with official Docker packaging and HTTP transport support.
- This removes the custom in-repo tool wrapper and keeps the sandboxed browser component as a pure tool.
