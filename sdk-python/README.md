# AgentCube Python SDK

The official Python SDK for **AgentCube**, enabling programmatic interaction with secure, isolated Code Interpreter environments.

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
    ttl=7200,                      # Session TTL (seconds)
    verbose=True                   # Enable debug logging
)
```

**Environment Variables**:

- `WORKLOAD_MANAGER_URL`: Control Plane URL
- `ROUTER_URL`: Data Plane URL  
- `API_TOKEN`: Authentication token

## Advanced: Session Reuse

For workflows requiring state persistence across multiple invocations:

```python
# Step 1: Create session, don't call stop()
client1 = CodeInterpreterClient()
client1.run_code("python", "x = 42")
session_id = client1.session_id  # Save for reuse

# Step 2: Reuse session
client2 = CodeInterpreterClient(session_id=session_id)
client2.run_code("python", "print(x)")  # x still exists
client2.stop()  # Cleanup when done
```

## Development

```bash
make build-python-sdk
python3 tests/e2e_picod_test.py
```
