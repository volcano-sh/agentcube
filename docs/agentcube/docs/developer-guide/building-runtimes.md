# Building Agent Runtimes

AgentCube allows you to run custom agent runtimes by providing your own container images. To ensure your image works correctly with AgentCube's control plane and router, it must follow a few simple requirements.

## 1. The Agent Daemon (PicoD/AgentD)

Every AgentCube sandbox needs a daemon running inside it to handle command execution and file management. You have two options:

### Option A: Use the AgentCube Base Image (Recommended)

You can build your custom agent on top of our pre-built images which already include `agentd`:

```dockerfile
FROM ghcr.io/volcano-sh/agentd:latest

# Install your dependencies
RUN apt-get update && apt-get install -y python3-pip
RUN pip install langchain

# Copy your agent code
COPY ./my_agent.py /app/my_agent.py

# The base image already sets up the ENTRYPOINT for agentd
```

### Option B: Manually Include AgentD

If you prefer a different base image, you must copy the `agentd` binary into your image and set it as the entrypoint.

```dockerfile
# ... build your image ...
COPY --from=ghcr.io/volcano-sh/agentd:latest /usr/local/bin/agentd /usr/local/bin/agentd

ENTRYPOINT ["/usr/local/bin/agentd"]
```

## 2. Configuration via AgentRuntime

Once your image is built and pushed to a registry, you can define it using the `AgentRuntime` CRD:

```yaml
apiVersion: runtime.agentcube.volcano.sh/v1alpha1
kind: AgentRuntime
metadata:
  name: my-custom-runtime
spec:
  podTemplate:
    spec:
      containers:
        - name: agent
          image: my-registry/my-custom-agent:latest
          # AgentCube will automatically inject required env vars and keys
```

## 3. Security Considerations

When building your runtime, keep the following in mind:

- **Non-Root User**: For better security, it is highly recommended to run your agent processes as a non-root user.
- **Resource Limits**: Ensure your agent is optimized to stay within the CPU and Memory limits defined in your `AgentRuntime` manifest.
- **Network Access**: By default, egress traffic might be restricted depending on your cluster's NetworkPolicies.
