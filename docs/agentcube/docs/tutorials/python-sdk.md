# Using the Python SDK

The **AgentCube Python SDK** allows you to manage isolated sandboxes and execute code programmatically. This is perfect for building LLM-powered applications that need a secure "Code Interpreter".

## 1. Installation

First, install the SDK from source:

```bash
git clone https://github.com/volcano-sh/agentcube.git
cd agentcube/sdk-python
pip install .
```

## 2. Basic Code Execution

The SDK uses a **Context Manager** pattern to handle the lifecycle of the remote environment automatically.

```python
from agentcube import CodeInterpreterClient

# This creates a remote sandbox on Kubernetes
with CodeInterpreterClient() as client:
    # Run a shell command
    print("Executing: uname -a")
    result = client.execute_command("uname -a")
    print(f"Output: {result}")

    # Run Python code
    py_code = """
import os
print(f'Current Directory: {os.getcwd()}')
print(f'Files in /workspace: {os.listdir("/workspace")}')
    """
    output = client.run_code("python", py_code)
    print(f"Python Result: {output}")
```

## 3. Working with Files

One of the most powerful features is the ability to securely transfer files to and from the sandbox.

```python
with CodeInterpreterClient() as sandbox:
    # 1. Upload a local file
    sandbox.upload_file("./local_document.txt", "/workspace/doc.txt")
    
    # 2. Process the file remotely
    sandbox.execute_command("cat /workspace/doc.txt | tr 'a-z' 'A-Z' > /workspace/UPPER.txt")
    
    # 3. Download the result
    sandbox.download_file("/workspace/UPPER.txt", "./upper_document.txt")
```

## 4. Why is this Secure?

Every session created by the SDK is:

- **Isolated**: Running in its own Kubernetes Pod with limited permissions.
- **Authorized**: Uses RSA-2048 key pairs. The SDK generates a private key locally. Only requests signed by this key are accepted by the remote agent.
- **Ephemeral**: By default, the environment is deleted as soon as the `with` block finishes.

## Next Steps

Now you're ready to see a complex application in action! Check out the **[PCAP Analyzer Walkthrough](./pcap-analyzer.md)**.
