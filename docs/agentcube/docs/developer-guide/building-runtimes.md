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

### Command environments

Previously, all container environment variables reached executed commands. After this change, only the documented allowlist is inherited. Other variables must be supplied explicitly in the execution request.

PicoD inherits the following exact names when they are present in its environment:

| Purpose | Names |
| --- | --- |
| Shell and locale | `PATH`, `HOME`, `LANG`, `LC_ALL`, `LC_CTYPE`, `TERM` |
| Python paths | `PYTHONHOME`, `PYTHONPATH`, `PYTHONUSERBASE` |
| Python behavior and encoding | `PYTHONNOUSERSITE`, `PYTHONUNBUFFERED`, `PYTHONDONTWRITEBYTECODE`, `PYTHONIOENCODING`, `PYTHONUTF8` |

All other names, including `HTTPS_PROXY`, `SSL_CERT_FILE`, `REQUESTS_CA_BUNDLE`, and `TMPDIR`, are excluded from automatic inheritance. There are no prefix exceptions such as `PYTHON*`. Custom images relying on excluded container variables must supply their values explicitly for each execution or adjust their runtime configuration. Python compatibility checks cover selected configurations, not every custom runtime.

The existing HTTP execution request accepts an `env` object. Its values override inherited settings, and empty strings remain valid values. Current Python SDK execution methods do **not** expose an `env=` argument; use a raw HTTP request when you need explicit variables. For example, for an existing CodeInterpreter session in a deployment using external authentication:

```bash
# Set ROUTER_URL, NAMESPACE, CODE_INTERPRETER, SESSION_ID, and ACCESS_TOKEN
# to your deployment, runtime, existing session, and OIDC access token.
curl --fail-with-body --request POST \
  "${ROUTER_URL}/v1/namespaces/${NAMESPACE}/code-interpreters/${CODE_INTERPRETER}/invocations/api/execute" \
  --header "Authorization: Bearer ${ACCESS_TOKEN}" \
  --header "x-agentcube-session-id: ${SESSION_ID}" \
  --header 'Content-Type: application/json' \
  --data '{
    "command": ["python3", "-c", "import os; print(os.environ[\"TMPDIR\"])"],
    "env": {"TMPDIR": "/tmp", "HTTPS_PROXY": "http://proxy.example:3128"}
  }'
```

The Router verifies the external token and signs the forwarded request for PicoD. If external authentication is disabled, omit the `Authorization` header.

Explicit request variables are passed to that command and are not copied into PicoD's environment or automatically inherited by later commands. This does not revoke credentials or provide isolation between executions sharing a sandbox: code can save values in shared files, and other processes may be able to inspect them, including through Linux `/proc`.

Executable lookup is unchanged: PicoD resolves a bare command name using its own `PATH` before applying the request environment. A request's `PATH` override controls the launched command and its subprocesses.

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
