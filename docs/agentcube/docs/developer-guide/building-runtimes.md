# Building Agent Runtimes

AgentCube allows you to run custom agent runtimes by providing your own container images. To ensure your image works correctly with AgentCube's control plane and router, it must follow a few simple requirements.

## 1. The Code Interpreter Daemon (PicoD)

CodeInterpreter sandboxes use PicoD to handle command execution and file management. You have two options:

### Option A: Use the AgentCube Base Image (Recommended)

You can build your custom code-interpreter image on top of the PicoD image:

```dockerfile
FROM ghcr.io/volcano-sh/picod:latest

# Install your dependencies
RUN apt-get update && apt-get install -y python3-pip
RUN pip install numpy pandas

# The base image already sets up the ENTRYPOINT for picod
```

### Option B: Manually Include PicoD

If you prefer a different base image, copy the `picod` binary into your image and set it as the entrypoint.

```dockerfile
# ... build your image ...
COPY --from=ghcr.io/volcano-sh/picod:latest /usr/local/bin/picod /usr/local/bin/picod

ENTRYPOINT ["/usr/local/bin/picod"]
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
