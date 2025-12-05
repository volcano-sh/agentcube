# AgentCube Python SDK

A lightweight Python SDK for interacting with AgentCube Code Interpreters.

## Features

- **Simple API**: Manage sessions with a pythonic context manager.
- **Secure**: Automatic RSA key generation and JWT signing.
- **Two-Plane Architecture**: Separates Control Plane (creation) from Data Plane (execution).

## Installation

```bash
pip install .
```

## Configuration

The SDK uses environment variables for configuration in a cluster environment:

- `WORKLOADMANAGER_URL`: URL of the Workload Manager service (e.g., `http://workload-manager:8080`).
- `ROUTER_URL`: URL of the Router service (e.g., `http://router:8080`).
- `API_TOKEN`: (Optional) Kubernetes Service Account token path is automatically detected.

## Usage

### Basic Example

```python
from agentcube import CodeInterpreterClient

# The client automatically reads WORKLOADMANAGER_URL and ROUTER_URL from env.
with CodeInterpreterClient() as client:
    # 1. Execute Shell Command
    print(client.execute_command("echo 'Hello World'"))

    # 2. Run Python Code
    output = client.run_code("python", "print(1 + 1)")
    print(f"Result: {output}")

    # 3. Write File
    client.write_file("content", "/tmp/file.txt")
```

### Manual Lifecycle

```python
# Creates session immediately upon instantiation
client = CodeInterpreterClient()
try:
    client.run_code("python", "print('manual start')")
finally:
    client.stop() # Deletes session
```

## Development

To run tests:

```bash
python3 -m unittest tests/test_code_interpreter.py
```
