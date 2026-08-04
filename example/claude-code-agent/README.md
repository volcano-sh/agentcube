# Claude Code AgentRuntime Example

This example runs a Claude Code SDK agent loop inside an AgentCube
`AgentRuntime` sandbox. AgentCube manages per-session sandbox lifecycle through
`x-agentcube-session-id`; the container exposes a small FastAPI service and
calls `claude-agent-sdk`.

## Files

```text
example/claude-code-agent/
├── README.md
├── agent.py
├── invoke_with_sdk.py
├── requirements.txt
├── Dockerfile
└── claude-code-agent.yaml
```

## Build

```bash
docker build -t claude-code-agent:latest \
  -f example/claude-code-agent/Dockerfile .
```

The Dockerfile defaults to Aliyun/npmmirror/PyPI mirrors for faster builds in
China. Override build args in the Dockerfile if your environment needs other
mirrors.

For minikube:

```bash
minikube image load claude-code-agent:latest
```

## Deploy

Create the API key secret:

```bash
kubectl create secret generic claude-code-agent-secrets \
  --from-literal=anthropic-auth-token=<YOUR_API_KEY>
```

Apply the runtime:

```bash
kubectl apply -f example/claude-code-agent/claude-code-agent.yaml
```

The default manifest uses:

```text
ANTHROPIC_BASE_URL=https://api.deepseek.com/anthropic
ANTHROPIC_MODEL=deepseek-v4-flash
```

## Invoke

Port-forward the AgentCube Router if needed:

```bash
kubectl -n agentcube port-forward deploy/agentcube-router 8081:8080
```

Health check:

```bash
curl -i \
  http://localhost:8081/v1/namespaces/default/agent-runtimes/claude-code-agent/invocations/health
```

Agent call:

```bash
curl -sS \
  http://localhost:8081/v1/namespaces/default/agent-runtimes/claude-code-agent/invocations/ \
  -H "Content-Type: application/json" \
  -H "x-agentcube-session-id: <SESSION_ID_FROM_HEALTH_RESPONSE>" \
  -d '{"prompt":"Reply with OK only.","max_turns":3}'
```

Pass the same `x-agentcube-session-id` header to reuse the same sandbox.

## Invoke With SDK

Install the AgentCube Python SDK, then run the example client:

```bash
pip install -e sdk-python

ROUTER_URL=http://localhost:8081 \
python example/claude-code-agent/invoke_with_sdk.py
```

Reuse a session:

```bash
AGENTCUBE_SESSION_ID=<SESSION_ID> \
python example/claude-code-agent/invoke_with_sdk.py
```
