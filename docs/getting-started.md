# Getting Started with AgentCube

This guide walks you through deploying AgentCube on a Kubernetes cluster and running your first code interpreter session.

## Prerequisites

Before you begin, ensure you have the following tools installed:

- **Kubernetes cluster**: v1.24 or later (local clusters like [Kind](https://kind.sigs.k8s.io/) or [minikube](https://minikube.sigs.k8s.io/) work well for testing)
- **kubectl**: Configured to access your cluster
- **Helm**: v3.x or later

## Architecture Overview

AgentCube consists of the following components:

| Component | Description |
|-----------|-------------|
| **Workload Manager** | Control plane - manages sandbox lifecycle, session registry |
| **AgentCube Router** | Data plane - handles request routing, authentication |
| **Redis** | Session store - synchronizes state across components |
| **agent-sandbox** | Third-party controller providing Sandbox CRDs ([kubernetes-sigs/agent-sandbox](https://github.com/kubernetes-sigs/agent-sandbox)) |

## Step 1: Install agent-sandbox

AgentCube relies on the [kubernetes-sigs/agent-sandbox](https://github.com/kubernetes-sigs/agent-sandbox) project for sandbox management. Install it first:

```bash
# Install agent-sandbox CRDs and controller
kubectl apply -f https://github.com/kubernetes-sigs/agent-sandbox/releases/download/v0.1.0/manifest.yaml
kubectl apply -f https://github.com/kubernetes-sigs/agent-sandbox/releases/download/v0.1.0/extensions.yaml
```

Verify the installation:

```bash
kubectl get pods -n agent-sandbox-system
```

## Step 2: Deploy Redis

AgentCube requires Redis for session state storage. Deploy Redis in your cluster:

```bash
kubectl create namespace agentcube

kubectl -n agentcube create deployment redis --image=redis:7-alpine --port=6379
kubectl -n agentcube expose deployment redis --port=6379 --target-port=6379

# Wait for Redis to be ready
kubectl -n agentcube rollout status deployment/redis
```

## Step 3: Deploy AgentCube

Clone the repository and install AgentCube using Helm:

```bash
git clone https://github.com/volcano-sh/agentcube.git
cd agentcube

helm install agentcube ./manifests/charts/base \
    --namespace agentcube \
    --create-namespace \
    --set redis.addr="redis.agentcube.svc.cluster.local:6379" \
    --set redis.password="''''" \
    --set router.rbac.create=true \
    --set router.serviceAccountName="agentcube-router"
```

This will install:

- AgentCube CRDs (`CodeInterpreter`, `AgentRuntime`)
- Workload Manager deployment
- AgentCube Router deployment

### Configuration Options

Key Helm values you can customize:

| Parameter | Default | Description |
|-----------|---------|-------------|
| `redis.addr` | `""` | Redis address (required) |
| `redis.password` | `""` | Redis password |
| `router.replicas` | `1` | Router replica count |
| `router.service.type` | `ClusterIP` | Router service type |
| `workloadmanager.replicas` | `1` | Workload Manager replica count |

For a complete list of options, see `manifests/charts/base/values.yaml`.

### Verify Installation

```bash
# Check all pods are running
kubectl get pods -n agentcube

# Expected output:
# NAME                                READY   STATUS    RESTARTS   AGE
# agentcube-router-xxx                1/1     Running   0          1m
# workloadmanager-xxx                 1/1     Running   0          1m
# redis-xxx                           1/1     Running   0          2m

# Verify CRDs are installed
kubectl get crd | grep agentcube
# Expected output:
# agentruntimes.runtime.agentcube.volcano.sh
# codeinterpreters.runtime.agentcube.volcano.sh
```

## Step 4: Create a CodeInterpreter

Create a CodeInterpreter resource that defines your sandbox template:

```bash
kubectl apply -f example/code-interpreter/code-interpreter.yaml
```

Or create your own:

```yaml
apiVersion: runtime.agentcube.volcano.sh/v1alpha1
kind: CodeInterpreter
metadata:
  name: my-interpreter
  namespace: default
spec:
  ports:
    - pathPrefix: "/"
      port: 8080
      protocol: "HTTP"
  template:
    image: ghcr.io/volcano-sh/picod:latest
    args:
      - --workspace=/root
    resources:
      limits:
        cpu: "500m"
        memory: "512Mi"
      requests:
        cpu: "100m"
        memory: "128Mi"
  sessionTimeout: "15m"
  maxSessionDuration: "8h"
```

Verify the CodeInterpreter is created:

```bash
kubectl get codeinterpreter
```

## Step 5: Use the Python SDK

### Install the SDK

```bash
pip install agentcube-sdk
```

### Run Your First Code

You need access to a running AgentCube instance (WorkloadManager and Router).

Set the following environment variables to point to your AgentCube services:

```bash
# To access the services from your local machine, you can use port-forwarding.

# In one terminal, forward the Workload Manager:
kubectl port-forward -n agentcube svc/workloadmanager 8080:8080

# In another terminal, forward the Router:
kubectl port-forward -n agentcube svc/agentcube-router 8081:8080

# Then, set your environment variables:
export WORKLOAD_MANAGER_URL="http://localhost:8080"
export ROUTER_URL="http://localhost:8081"

# Optional: If your instance requires authentication
# export API_TOKEN="your-token"
```

```python
from agentcube import CodeInterpreterClient

with CodeInterpreterClient(name="my-interpreter") as client:
    result = client.run_code("python", "print('Hello from AgentCube!')")
    print(result)
```

For detailed SDK usage, see the [Python SDK Guide](devguide/code-interpreter-python-sdk.md).

## Next Steps

- [Python SDK Guide](devguide/code-interpreter-python-sdk.md) - Detailed SDK documentation
- [Using LangChain with CodeInterpreter](devguide/code-interpreter-using-langchain.md) - Integration guide
- [Design Proposal](design/agentcube-proposal.md) - Architecture details

## Cleanup

To remove AgentCube from your cluster:

```bash
helm uninstall agentcube -n agentcube
kubectl delete namespace agentcube
kubectl delete -f https://github.com/kubernetes-sigs/agent-sandbox/releases/download/v0.1.0/extensions.yaml
kubectl delete -f https://github.com/kubernetes-sigs/agent-sandbox/releases/download/v0.1.0/manifest.yaml
```
