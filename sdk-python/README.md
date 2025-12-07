# AgentCube Python SDK

The official Python SDK for **AgentCube**, enabling programmatic interaction with secure, isolated Code Interpreter environments.

This SDK creates a seamless bridge between your application and the AgentCube runtime, handling the complexity of:
*   **Session Management**: Automatically creating and destroying isolated environments.
*   **Security**: End-to-end encryption using client-generated RSA keys and JWTs.
*   **Execution**: Running shell commands and code (Python, Bash) remotely.
*   **File Management**: Uploading and downloading files to/from the sandbox.

## Features

*   **Secure by Design**: Uses asymmetric cryptography (RSA-2048) to authorize Data Plane requests. Only the client holding the private key can execute code.
*   **Simple API**: Pythonic context managers (`with` statement) for automatic resource cleanup.
*   **Flexible**: Supports both short-lived (ephemeral) and long-running sessions.
*   **Kubernetes Native**: Automatically authenticates using Service Account tokens when running in-cluster.

## Installation

**From Source**:
```bash
git clone https://github.com/volcano-sh/agentcube.git
cd agentcube/sdk-python
pip install .
```

**Development Mode**:
```bash
pip install -e .
```

## Usage

### Quick Start (Context Manager)

The recommended way to use the SDK is with a context manager, which ensures the session is properly closed (and the remote pod deleted) when done.

```python
from agentcube import CodeInterpreterClient

# Initialize client (uses env vars for configuration)
with CodeInterpreterClient() as client:
    # 1. Run a simple shell command
    print("User: whoami")
    print(client.execute_command("whoami"))

    # 2. Execute Python code
    code = """
    import math
    print(f"Pi is approximately {math.pi:.4f}")
    """
    output = client.run_code("python", code)
    print(f"Result: {output}")
```

### File Operations

You can easily move files in and out of the sandbox.

```python
with CodeInterpreterClient() as sandbox:
    # Upload a local dataset
    sandbox.upload_file("./data.csv", "/workspace/data.csv")
    
    # Process it with Python
    script = """
    import pandas as pd
    df = pd.read_csv('/workspace/data.csv')
    df.describe().to_csv('/workspace/summary.csv')
    """
    sandbox.run_code("python", script)
    
    # Download the result
    sandbox.download_file("/workspace/summary.csv", "./summary.csv")
```

### Manual Lifecycle Management

For long-running applications (like a web server managing user sessions), you can manually control the lifecycle.

```python
# Create a session with a 1-hour timeout
client = CodeInterpreterClient(ttl=3600) 

try:
    client.execute_command("echo 'Session started'")
    # ... perform operations ...
finally:
    client.stop() # CRITICAL: Ensure resources are released
```

### Customizing the Environment

```python
client = CodeInterpreterClient(
    name="custom-template",    # Name of the CodeInterpreter CRD template to use
    namespace="agentcube",     # Kubernetes namespace where AgentCube runs
    ttl=7200,                  # 2 hours Time-To-Live
    verbose=True               # Enable debug logging
)
```

## Architecture

The SDK operates on a **Split-Plane Architecture**:

1.  **Control Plane (Workload Manager)**: 
    *   The SDK authenticates via K8s Service Account Token.
    *   It requests a new session and sends a locally generated **Public Key**.
    *   The Workload Manager creates the Pod and injects this Public Key.
2.  **Data Plane (Router -> PicoD)**: 
    *   The SDK uses the corresponding **Private Key** to sign JWTs for every execution request.
    *   The agent inside the Pod (PicoD) validates the JWT using the injected Public Key.
    *   This ensures that **only** the SDK instance that created the session can execute code in it.

## Development

### Building the Package

Use the provided Makefile in the root directory to build the distribution packages (Wheel and Source):

```bash
# Build the Python SDK
make build-python-sdk
```

Artifacts will be generated in `sdk-python/dist/`.

### Running Tests

```bash
# Install test dependencies
pip install pytest requests PyJWT cryptography

# Run E2E tests (requires local Docker environment for mocking)
python3 tests/e2e_picod_test.py
```