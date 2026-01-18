---
sidebar_position: 2
---

# Getting Started

This guide will help you get AgentCube up and running on your Kubernetes cluster.

## Prerequisites

Before you begin, ensure you have the following:

- A Kubernetes cluster (v1.24+)
- `kubectl` installed and configured
- [Helm](https://helm.sh/docs/intro/install/) v3 installed
- [Volcano](https://volcano.sh/en/docs/installation/) installed on your cluster (AgentCube is a Volcano subproject)

## 1. Installation

AgentCube can be installed using Helm. Follow these steps:

### Using Helm (Recommended)

Add the Volcano Helm repository (if not already added):

```bash
helm repo add volcano-sh https://volcano-sh.github.io/volcano
helm repo update
```

Install AgentCube:

```bash
# Clone the repository
git clone https://github.com/volcano-sh/agentcube.git
cd agentcube

# Install the Helm chart
helm install agentcube ./manifests/charts/base -n agentcube --create-namespace
```

### Verify Installation

Check if the AgentCube components are running:

```bash
kubectl get pods -n agentcube
```

You should see pods for `workloadmanager`, `agentcube-router`, and `volcano-agent-scheduler`.

## 2. Deploy Your First Agent Runtime

AgentCube uses a custom resource called `AgentRuntime` to define how your AI Agents should run.

Create a file named `my-agent.yaml`:

```yaml
apiVersion: runtime.agentcube.volcano.sh/v1alpha1
kind: AgentRuntime
metadata:
  name: sample-agent
  namespace: default
spec:
  targetPort:
    - pathPrefix: "/"
      port: 8080
      protocol: "HTTP"
  podTemplate:
    spec:
      containers:
        - name: agent
          image: python:3.11-slim
          command: ["python3", "-m", "http.server", "8080"]
  sessionTimeout: "15m"
  maxSessionDuration: "1h"
```

Apply the manifest:

```bash
kubectl apply -f my-agent.yaml
```

## 3. Access Your Agent

Once the `AgentRuntime` is created, you can access it through the AgentCube Router.

The Router provides a stable entry point for your agents and handles dynamic scaling and lifecycle management (like sleep/resume).

Find the Router's address:

```bash
kubectl get svc -n agentcube agentcube-router
```

You can now send requests to your agent via the router!

## Next Steps

- Explore the [PCAP Analyzer Example](https://github.com/volcano-sh/agentcube/tree/main/example/pcap-analyzer) for a real-world use case.
- For more details on the architecture, see the **[Architecture Overview](./architecture/overview.md)** or the original **[Design Proposal](https://github.com/volcano-sh/agentcube/blob/main/docs/design/agentcube-proposal.md)**.
- Check out the [Python SDK](https://github.com/volcano-sh/agentcube/tree/main/sdk-python) to build your own agents.
