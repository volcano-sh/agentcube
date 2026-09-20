# AgentCube Python SDK

The official Python SDK for **AgentCube**, enabling programmatic interaction with secure, isolated Code Interpreter environments.

This SDK creates a seamless bridge between your application and the AgentCube runtime, handling the complexity of:

* **Session Management**: Automatically creating and destroying isolated environments.
* **Execution**: Running shell commands and code (Python, Bash) remotely.
* **File Management**: Uploading and downloading files to/from the sandbox.

## Features

* **Simple API**: Pythonic context managers (`with` statement) for automatic resource cleanup.
* **Flexible**: Supports both short-lived (ephemeral) and long-running sessions.
* **Kubernetes Native**: Automatically authenticates using Service Account tokens when running in-cluster.

## Installation

```bash
git clone https://github.com/volcano-sh/agentcube.git
cd agentcube/sdk-python
pip install .
```

## Quick Start

```python
from agentcube import CodeInterpreterClient

with CodeInterpreterClient() as client:
    output = client.run_code("python", "print('Hello, AgentCube!')")
    print(output)
# Session automatically deleted on exit
```

### Manual Lifecycle Management

For long-running applications, you can manually control the lifecycle:

```python
# Request a session with a 1-hour maximum lifetime
client = CodeInterpreterClient(ttl=3600)

try:
    client.run_code("python", "print('Session started')")
    # ... perform operations ...
    # File system state persists within session
    client.write_file("42", "value.txt")
    client.run_code("python", "print(open('value.txt').read())")
finally:
    client.stop()  # CRITICAL: Ensure resources are released
```

## File Operations

```python
with CodeInterpreterClient() as client:
    client.upload_file("./data.csv", "/workspace/data.csv")
    client.run_code("python", """
import pandas as pd
df = pd.read_csv('/workspace/data.csv')
df.describe().to_csv('/workspace/summary.csv')
""")
    client.download_file("/workspace/summary.csv", "./summary.csv")
```

## API Reference

| Method | Description |
|--------|-------------|
| `execute_command(cmd)` | Execute shell command |
| `run_code(language, code)` | Execute code (python/bash) |
| `upload_file(local, remote)` | Upload file to sandbox |
| `download_file(remote, local)` | Download file from sandbox |
| `write_file(content, path)` | Write string to file |
| `list_files(path)` | List directory contents |
| `stop()` | Delete session and release resources |

## Configuration

```python
CodeInterpreterClient(
    name="custom-template",        # CodeInterpreter CRD template name
    namespace="agentcube",         # Kubernetes namespace
    ttl=7200,                      # Requested maximum lifetime (seconds)
    session_id="existing-id",      # Optional: reuse existing session
    verbose=True                   # Enable debug logging
)
```

If `ttl` is omitted, the CodeInterpreter CRD's `spec.maxSessionDuration`
determines the maximum lifetime. Otherwise, the effective maximum lifetime is
the smaller of `ttl` and `spec.maxSessionDuration`. A session can be reclaimed
earlier when it is idle for `spec.sessionTimeout`.

**Environment Variables**:

* `WORKLOAD_MANAGER_URL`: Control Plane URL
* `ROUTER_URL`: Data Plane URL  

## Advanced: Session Reuse

For workflows requiring **file system** state persistence across multiple client instances:

> **Note**: Each `run_code` call spawns a new process. Python variables do NOT persist. Only file system state is preserved.

```python
# Step 1: Create session and save state to file
client1 = CodeInterpreterClient()
session_id = client1.session_id  # Save for reuse
client1.write_file("42", "value.txt")
# Don't call stop() - let session persist

# Step 2: Reuse session with new client
client2 = CodeInterpreterClient(session_id=session_id)
client2.run_code("python", "print(open('value.txt').read())")  # File persists
client2.stop()  # Cleanup when done
```

### Expired Sessions

When a reused session has been reclaimed, the SDK clears the stale session ID
and raises `SessionNotFoundError`. It does not create a replacement implicitly
because the previous sandbox state has been lost.

```python
from agentcube import CodeInterpreterClient, SessionNotFoundError

client = CodeInterpreterClient(session_id=saved_session_id)
try:
    client.list_files()
except SessionNotFoundError:
    # The old ID is no longer usable. Create a new client only if the
    # application can continue without the previous sandbox state.
    client = CodeInterpreterClient()
```

## Development

```bash
make build-python-sdk
python3 -m pytest sdk-python/tests/
```
