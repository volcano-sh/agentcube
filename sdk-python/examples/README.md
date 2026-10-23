# AgentCube SDK Examples

This directory contains examples of how to use the AgentCube Python SDK.

Run all commands in this guide from the repository root.

## Prerequisites

1.  **Install the SDK**:
    
    Install the SDK from the repository checkout:
    ```bash
    pip install ./sdk-python
    ```

2.  **AgentCube Environment**:
    You need access to a running AgentCube instance (WorkloadManager and Router).
    
    Set the following environment variables to point to your AgentCube services:
    ```bash
    export WORKLOAD_MANAGER_URL="http://<your-workload-manager-host>:<port>"
    export ROUTER_URL="http://<your-router-host>:<port>"
    
    # Optional: If your instance requires authentication
    ```

## Running the Examples

### Basic Usage

`basic_usage.py` demonstrates the core features:
*   Connecting to the Control Plane (WorkloadManager)
*   Creating a secure session
*   Executing shell commands
*   Running Python code
*   Managing files
*   Automatic session cleanup

To run it:
```bash
python sdk-python/examples/basic_usage.py
```

### Agent Runtime Usage

`agent_runtime_usage.py` creates a session for the runnable echo AgentRuntime,
invokes it, closes the local client, and then reuses the same remote session with
a second client. Calling `AgentRuntimeClient.close()` releases local HTTP
resources; the remote session remains available until its configured timeout.

Deploy the example runtime and forward the Router before running the script:

```bash
kubectl apply -f example/agent-runtime/agent-runtime.yaml
kubectl port-forward -n agentcube svc/agentcube-router 8081:8080
```

In another terminal:

```bash
export ROUTER_URL="http://localhost:8081"
python sdk-python/examples/agent_runtime_usage.py
```

The runtime name and namespace default to `simple-agentruntime` and `default`.
Override them with `AGENT_RUNTIME_NAME` and `SANDBOX_NAMESPACE` if needed.
The created session is removed automatically after its configured timeout. Remove
the runtime definition when you are done:

```bash
kubectl delete -f example/agent-runtime/agent-runtime.yaml
```
