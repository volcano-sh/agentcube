# Sandbox SDK

A Python SDK for managing Kubernetes sandboxes, providing easy-to-use interfaces for creating, controlling, and interacting with sandbox environments.

## Architecture

The SDK provides a clean separation between control plane and data plane operations:

- **`Sandbox`**: Base class providing lifecycle management (control plane)
  - Creating and deleting sandboxes
  - Checking status and retrieving information
  - Listing sandboxes
  
- **`CodeInterpreterClient`**: Extends `Sandbox` with code execution capabilities (data plane)
  - All lifecycle methods from `Sandbox`
  - Command execution
  - Code snippet execution
  - File upload/download

This architecture allows future extensions for different use cases (e.g., BrowserUse, ComputerUse) by creating new client classes that extend `Sandbox` with their specific data plane interfaces.

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

### Using CodeInterpreterClient (Recommended for Code Execution)

```python
from agentcube import CodeInterpreterClient

# Create a new code interpreter sandbox
sandbox = CodeInterpreterClient()

try:
    # Get sandbox information
    info = sandbox.get_info()
    print("Sandbox Info:", info)
    
    # Execute commands in the sandbox
    output = sandbox.execute_command("echo 'Hello from Sandbox!'")
    print("Command Output:", output)
    
    # Run Python code directly
    code = "print('Hello from Python!'); import sys; print(sys.version)"
    output = sandbox.run_code(language="python", code=code)
    print("Python Output:", output)
    
    # Upload a file
    script_content = "print('Hello from uploaded script!')"
    sandbox.write_file(script_content, "/workspace/test.py")
    
    # Execute the uploaded file
    output = sandbox.execute_command("python3 /workspace/test.py")
    print("Script Output:", output)
    
finally:
    # Stop and clean up the sandbox
    sandbox.stop()
```

### Using Base Sandbox (For Lifecycle Management Only)

```python
from agentcube import Sandbox

# Create a sandbox for lifecycle management only
sandbox = Sandbox(ttl=3600, image="python:3.9")

try:
    # Check if running
    if sandbox.is_running():
        print("Sandbox is running")
    
    # Get sandbox info
    info = sandbox.get_info()
    print(f"Status: {info['status']}")
    
    # List all sandboxes
    sandboxes = sandbox.list_sandboxes()
    print(f"Total sandboxes: {len(sandboxes)}")
    
finally:
    sandbox.stop()
```

## API Reference

### `Sandbox` Class (Base Class)

The base class provides lifecycle management for sandboxes.

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

**Lifecycle Management:**

- `is_running() -> bool`: Check if the sandbox is in running state
  ```python
  if sandbox.is_running():
      print("Sandbox is running")
  ```

- `get_info() -> Dict[str, Any]`: Retrieve detailed information about the sandbox
  ```python
  info = sandbox.get_info()
  ```

- `list_sandboxes() -> List[Dict[str, Any]]`: List all sandboxes from the server
  ```python
  sandboxes = sandbox.list_sandboxes()
  ```

- `stop() -> bool`: Stop and delete the sandbox
  ```python
  sandbox.stop()
  ```

- `cleanup()`: Clean up resources (called automatically by `stop()`)

### `CodeInterpreterClient` Class

Extends `Sandbox` with code execution and file management capabilities.

#### Initialization
```python
code_interpreter = CodeInterpreterClient(
    ttl=3600,  # Time-to-live in seconds
    image="sandbox:latest",  # Container image to use
    api_url="http://localhost:8080"  # API server address (optional)
)
```

#### Methods

**Inherits all lifecycle methods from `Sandbox`, plus:**

**Command Execution:**

- `execute_command(command: str) -> str`: Execute a shell command
  ```python
  output = code_interpreter.execute_command("ls -la")
  ```

- `execute_commands(commands: List[str]) -> Dict[str, str]`: Execute multiple commands
  ```python
  outputs = code_interpreter.execute_commands(["pwd", "whoami"])
  ```

- `run_code(language: str, code: str, timeout: float = 30) -> str`: Run code snippet
  ```python
  output = code_interpreter.run_code(
      language="python",
      code="print('Hello World!')"
  )
  ```

**File Operations:**

- `write_file(content: str, remote_path: str)`: Write content to a remote file
  ```python
  code_interpreter.write_file("file content", "/workspace/data.txt")
  ```

- `upload_file(local_path: str, remote_path: str)`: Upload a local file
  ```python
  code_interpreter.upload_file("/local/file.txt", "/workspace/file.txt")
  ```

- `download_file(remote_path: str, local_path: str) -> str`: Download a file
  ```python
  code_interpreter.download_file("/workspace/results.json", "/local/results.json")
  ```

## Examples

### Advanced Example: Fibonacci Generator

```python
from agentcube import CodeInterpreterClient
import json

# Create code interpreter sandbox
sandbox = CodeInterpreterClient()
print(f"Created sandbox with ID: {sandbox.id}")

try:
    # Write a Python script
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
    sandbox.write_file(script_content, "/workspace/fib.py")
    
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

### Running Examples

```bash
# Run the main example (code interpreter)
python examples/examples.py

# Run the refactoring examples (architecture demonstration)
python examples/refactoring_examples.py
```

## Future Extensions

The refactored architecture allows for easy extension to support different use cases:

```python
# Future: Browser automation
class BrowserUseClient(Sandbox):
    def navigate(self, url: str): ...
    def click(self, selector: str): ...
    def screenshot(self) -> bytes: ...

# Future: Computer use
class ComputerUseClient(Sandbox):
    def mouse_move(self, x: int, y: int): ...
    def keyboard_type(self, text: str): ...
    def screen_capture(self) -> bytes: ...

# Future: Agent hosting
class AgentHostClient(Sandbox):
    def deploy_agent(self, agent_config: dict): ...
    def invoke_agent(self, input: dict): ...
```

## Dependencies

- `paramiko`: For SSH connections and file transfers
- `requests`: For API communication with the sandbox server

## Development

### Running Tests

```bash
# Run refactoring tests
python -m unittest tests.test_sandbox_refactoring -v

# Run client tests
python tests/test_client.py
python tests/test_ssh_client.py
```

### Environment Variables

- `API_URL`: Default API server address (defaults to `http://localhost:8080`)

## License

[Add your license information here]