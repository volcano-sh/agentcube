# Sandbox SDK

A Python SDK for managing Kubernetes sandboxes, providing easy-to-use interfaces for creating, controlling, and interacting with sandbox environments.

## Installation

### From Source

1. Clone the repository:
```bash
git clone <repository-url>
cd sdk-python
```

2. Install the SDK in development mode:
```bash
pip install -e .
```

## Quick Start

### Basic Usage

```python
from agentcube_sdk.sandbox import Sandbox

# Create a new sandbox instance
sandbox = Sandbox()

try:
    # Get sandbox information
    info = sandbox.get_info()
    print("Sandbox Info:", info)
    
    # Execute commands in the sandbox
    output = sandbox.execute_command("echo 'Hello from Sandbox!'")
    print("Command Output:", output)
    
    # Upload a file
    script_content = "print('Hello from uploaded script!')"
    sandbox.upload_file(script_content, "/workspace/test.py")
    
    # Execute the uploaded file
    output = sandbox.execute_command("python3 /workspace/test.py")
    print("Script Output:", output)
    
finally:
    # Stop and clean up the sandbox
    sandbox.stop()
```

## API Reference

### `Sandbox` Class

#### Initialization
```python
sandbox = Sandbox(
    ttl=3600,  # Time-to-live in seconds
    image="sandbox:latest",  # Container image to use
    api_url="http://localhost:8080"  # API server address (optional)
)
```

Environment variable `API_URL` can be used to set the default API server address.

#### Methods

- `is_running()`: Check if the sandbox is in running state
  ```python
  if sandbox.is_running():
      print("Sandbox is running")
  ```

- `get_info()`: Retrieve detailed information about the sandbox
  ```python
  info = sandbox.get_info()
  ```

- `list_sandboxes()`: List all sandboxes from the server
  ```python
  sandboxes = sandbox.list_sandboxes()
  ```

- `execute_command(command: str) -> str`: Execute a command in the sandbox
  ```python
  output = sandbox.execute_command("ls -la")
  ```

- `upload_file(content: str, remote_path: str)`: Upload content to a remote file
  ```python
  sandbox.upload_file("file content", "/workspace/data.txt")
  ```

- `download_file(remote_path: str, local_path: str)`: Download a file from the sandbox
  ```python
  sandbox.download_file("/workspace/results.json", "/local/path/results.json")
  ```

- `stop()`: Stop and delete the sandbox
  ```python
  sandbox.stop()
  ```

## Examples

### Advanced Example: Fibonacci Generator

```python
from agentcube_sdk.sandbox import Sandbox
import json

# Create sandbox
sandbox = Sandbox()
print(f"Created sandbox with ID: {sandbox.id}")

try:
    # Upload a Python script
    script_content = """
import json
from datetime import datetime

def generate_fibonacci(n):
    fib = [0, 1]
    for i in range(2, n):
        fib.append(fib[i-1] + fib[i-2])
    return fib[:n]

n = 20
fib = generate_fibonacci(n)
with open('/workspace/output.json', 'w') as f:
    json.dump({
        "timestamp": datetime.now().isoformat(),
        "count": n,
        "numbers": fib,
        "sum": sum(fib)
    }, f, indent=2)
"""
    sandbox.upload_file(script_content, "/workspace/fib.py")
    
    # Execute the script
    output = sandbox.execute_command("python3 /workspace/fib.py")
    print("Script output:", output)
    
    # Download and print results
    sandbox.download_file("/workspace/output.json", "/tmp/fib_results.json")
    with open("/tmp/fib_results.json", "r") as f:
        results = json.load(f)
    print(f"Generated {results['count']} Fibonacci numbers. Sum: {results['sum']}")

finally:
    # Clean up
    sandbox.stop()
```
Running Example
```
python examples/examples.py
```
## Dependencies

- `paramiko`: For SSH connections and file transfers
- `requests`: For API communication with the sandbox server

## Development

### Running Tests

```bash
python tests/clients/test_client.py
python tests/clients/test_ssh_client.py
```

### Environment Variables

- `API_URL`: Default API server address (defaults to `http://localhost:8080`)

## License

[Add your license information here]