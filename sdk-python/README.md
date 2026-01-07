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

client = CodeInterpreterClient()

# Execute commands and code
client.execute_command("whoami")
output = client.run_code("python", "print('Hello, AgentCube!')")

# Cleanup when done
client.delete()
```

### With Context Manager

```python
with CodeInterpreterClient() as client:
    output = client.run_code("python", "print(2 + 2)")
# Connections closed automatically
```

## File Operations

```python
client = CodeInterpreterClient()

client.upload_file("./data.csv", "/workspace/data.csv")
client.run_code("python", """
import pandas as pd
df = pd.read_csv('/workspace/data.csv')
df.describe().to_csv('/workspace/summary.csv')
""")
client.download_file("/workspace/summary.csv", "./summary.csv")

client.delete()
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
| `close()` | Close connections |
| `delete()` | Delete session from server |

## Configuration

```python
client = CodeInterpreterClient(
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
# First invocation
client1 = CodeInterpreterClient()
session_id = client1.session_id
client1.run_code("python", "x = 42")
client1.close()

# Later invocation - reuse session
client2 = CodeInterpreterClient(session_id=session_id)
client2.run_code("python", "print(x)")  # x still exists
client2.delete()
```

## Development

```bash
make build-python-sdk
python3 tests/e2e_picod_test.py
```
