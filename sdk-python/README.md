# AgentCube Python SDK

A Python SDK for managing Kubernetes sandboxes, providing easy-to-use interfaces for creating, controlling, and interacting with sandbox environments.

## 1. Architecture

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

## 2. Installation

Ensure you have Python 3.8+ installed.

### From Source

1. Clone the repository:
```bash
git clone <repository-url>
cd sdk-python
```

2. Install dependencies:
```bash
pip install -r requirements.txt
```
*Key dependencies:* `requests`, `cryptography`, `paramiko`.

3. Install the SDK in development mode:
```bash
pip install -e .
```

## 3. Quick Start

### Using CodeInterpreterClient (Recommended for Code Execution)

The recommended way to use the SDK is via the `CodeInterpreterClient` with a context manager. This ensures that resources (like the sandbox environment) are properly created and cleaned up.

By default, the client uses the lightweight REST API (PicoD) for communication, which is faster and more secure than SSH.

```python
from agentcube import CodeInterpreterClient

# Initialize the client
# This automatically creates a sandbox and establishes a secure session
with CodeInterpreterClient() as client:
    
    # 1. Run Python Code
    print("--- Running Python Code ---")
    output = client.run_code("python", "print('Hello from AgentCube Sandbox!')")
    print(f"Output: {output}")

    # 2. Execute Shell Commands
    print("\n--- Executing Shell Command ---")
    sys_info = client.execute_command("uname -a")
    print(f"System Info: {sys_info}")
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

## 4. Core Features

### Code Execution

You can run code snippets in supported languages (currently Python and Bash).

```python
# Run a Python script with imports
python_code = """
import json
import sys
data = {"version": sys.version, "platform": sys.platform}
print(json.dumps(data, indent=2))
"""
output = client.run_code("python", python_code)

# Run a Bash script
bash_code = """
echo "Current directory:"
pwd
echo "Files:"
ls -F
"""
output = client.run_code("bash", bash_code)
```

### File Management

The SDK provides robust methods for transferring files between your local machine and the sandbox.

**Writing Content Directly:**
Write string content to a file in the sandbox.
```python
client.write_file("print('This is a generated file')", "/workspace/script.py")
```

**Uploading Files:**
Upload a file from your local filesystem to the sandbox. This supports large files via multipart upload.
```python
client.upload_file("./local_data.csv", "/workspace/data.csv")
```

**Downloading Files:**
Download a file from the sandbox to your local machine.
```python
client.download_file("/workspace/result.json", "./local_result.json")
```

## 5. Configuration & Modes

### SSH Mode

If you require a persistent shell connection or features specific to SSH, you can enable SSH mode. Note that this requires the sandbox image to have an SSH server installed and configured.

```python
# Initialize with use_ssh=True
with CodeInterpreterClient(use_ssh=True) as client:
    client.execute_command("echo 'I am using SSH!'")
```

### Custom Configuration

You can customize the sandbox environment using various parameters:

```python
with CodeInterpreterClient(
    image="my-custom-image:latest", # Custom docker image
    ttl=7200,                       # Sandbox lifetime in seconds (2 hours)
    namespace="agent-space",        # Kubernetes namespace
    api_url="http://localhost:8080" # Custom API server URL
) as client:
    # ... operations ...
    pass
```

Environment variable `API_URL` can also be used to set the default API server address.

## 6. API Reference

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

#### Methods

**Lifecycle Management:**

- `is_running() -> bool`: Check if the sandbox is in running state
- `get_info() -> Dict[str, Any]`: Retrieve detailed information about the sandbox
- `list_sandboxes() -> List[Dict[str, Any]]`: List all sandboxes from the server
- `stop() -> bool`: Stop and delete the sandbox
- `cleanup()`: Clean up resources (called automatically by `stop()`)

### `CodeInterpreterClient` Class

Extends `Sandbox` with code execution and file management capabilities.

#### Initialization
```python
code_interpreter = CodeInterpreterClient(
    ttl=3600,  # Time-to-live in seconds
    image="sandbox:latest",  # Container image to use
    api_url="http://localhost:8080",  # API server address (optional)
    use_ssh=False # Use SSH instead of REST API
)
```

#### Methods

**Inherits all lifecycle methods from `Sandbox`, plus:**

**Command Execution:**

- `execute_command(command: str) -> str`: Execute a shell command
- `execute_commands(commands: List[str]) -> Dict[str, str]`: Execute multiple commands
- `run_code(language: str, code: str, timeout: float = 30) -> str`: Run code snippet

**File Operations:**

- `write_file(content: str, remote_path: str)`: Write content to a remote file
- `upload_file(local_path: str, remote_path: str)`: Upload a local file
- `download_file(remote_path: str, local_path: str) -> str`: Download a file

## 7. Future Extensions

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

## 8. Examples

### Advanced Example: Fibonacci Generator

```python
from agentcube import CodeInterpreterClient
import json

# Create code interpreter sandbox
with CodeInterpreterClient() as sandbox:
    print(f"Created sandbox with ID: {sandbox.id}")

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
```

### Running Examples

```bash
# Run the main example (code interpreter)
python examples/examples.py
```

## 9. Error Handling

The SDK raises specific exceptions for different failure scenarios.

```python
from agentcube.utils.exceptions import SandboxNotReadyError, SandboxNotFoundError

try:
    with CodeInterpreterClient() as client:
        client.execute_command("some_command")
except SandboxNotReadyError:
    print("The sandbox is not in a running state.")
except SandboxNotFoundError:
    print("The specified sandbox ID was not found.")
except Exception as e:
    print(f"An unexpected error occurred: {e}")
```

## 10. Development

### Running Tests

```bash
# Run refactoring tests
python -m unittest tests.test_sandbox_refactoring -v

# Run client tests
python tests/test_client.py
python tests/test_ssh_client.py
```

## License

[Add your license information here]