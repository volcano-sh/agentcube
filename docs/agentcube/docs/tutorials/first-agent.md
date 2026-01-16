# Your First Agent

This tutorial guides you through deploying your first AI Agent using the `AgentRuntime` Custom Resource Definition (CRD).

## What is an AgentRuntime?

`AgentRuntime` is the core configuration for your agents in AgentCube. It defines:

- The **Container Image** to run.
- **Resource Constraints** (CPU, Memory).
- **Network Access** (Ports and path prefixes).
- **Session Policies** (Lifecycle, timeouts).

## 1. Create the Manifest

Let's create a more detailed agent than the one in the Quick Start. This agent will run a simple Python-based API.

Create `my-agent-runtime.yaml`:

```yaml
apiVersion: runtime.agentcube.volcano.sh/v1alpha1
kind: AgentRuntime
metadata:
  name: interactive-python
  namespace: default
spec:
  targetPort:
    - pathPrefix: "/"
      port: 8000
      protocol: "HTTP"
  podTemplate:
    spec:
      containers:
        - name: agent
          image: python:3.11-slim
          command: ["python3", "-m", "http.server", "8000"]
          resources:
            requests:
              cpu: "100m"
              memory: "128Mi"
            limits:
              cpu: "500m"
              memory: "512Mi"
  sessionTimeout: "30m"      # Recycled after 30 mins of inactivity
  maxSessionDuration: "12h"  # Hard limit on session length
```

## 2. Deploy and Verify

Apply the configuration:

```bash
kubectl apply -f my-agent-runtime.yaml
```

Check the status:

```bash
kubectl get agentruntime interactive-python
```

## 3. Interaction Flow

When you send a request to the **AgentCube Router** for this agent:

1. **Request Arrival**: The Router receives a request (e.g., via a specific header or URL path).
2. **Pod Provisioning**: If no pod is currently running for this session, the **Workload Manager** instantly spins one up using the `AgentRuntime` template.
3. **Fast Startup**: AgentCube is optimized for low-latency startup, often bringing up the agent in sub-second time.
4. **Routing**: Once ready, the Router forwards your traffic to the pod.
5. **Idle Management**: If no requests are received for 30 minutes (as per `sessionTimeout`), the pod is hibernated to save cluster resources.

## 4. Cleaning Up

To remove your agent:

```bash
kubectl delete agentruntime interactive-python
```

## Next Steps

Now that you know how to deploy an agent, let's learn how to interact with it programmatically in the **[Python SDK Tutorial](./python-sdk.md)**.
