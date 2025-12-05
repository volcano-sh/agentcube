# PicoD Client Examples

This directory contains example code for using the PicoD client with RSA authentication.

## Quick Start

### Prerequisites

```bash
# Install dependencies
pip install cryptography requests
```

### Running the Example

```bash
# Navigate to the SDK directory
cd sdk-python

# Make sure PicoD server is running (in another terminal)
go run cmd/picod/main.go

# Run the example
python3 examples/picod_example.py
```

## What the Example Does

The `picod_example.py` demonstrates:

1. **RSA Key Management**
   - Generate RSA-2048 key pair
   - Load existing keys
   - Save keys securely (600 permissions)

2. **Server Initialization**
   - Send public key to PicoD server
   - Lock server to your public key

3. **Authenticated Operations**
   - Execute commands with RSA signature verification
   - Upload/download files securely
   - Run Python and Bash code snippets

4. **API Compatibility**
   - Same interface as SSH-based client
   - Context manager support
   - Automatic authentication detection

## Example Output

```
============================================================
PicoD Client RSA Authentication Example
============================================================

Configuration:
  Host: localhost
  Port: 8080
  Key File: picod_client_keys.pem

Step 1: Loading/Generating RSA key pair...
  ✓ Loaded existing key pair

  Authentication Status: Enabled

Step 2: Initializing PicoD server...
  Note: This sends your public key to the server
  ✓ Server initialized successfully

...

============================================================
✓ All operations completed successfully!
============================================================
```

## Environment Variables

You can customize the connection using environment variables:

```bash
export PICOD_HOST=localhost
export PICOD_PORT=8080
export PICOD_KEY_FILE=my_custom_keys.pem

python3 examples/picod_example.py
```

## API Usage

```python
from agentcube.clients.picod_client import PicoDClient

# Create client
client = PicoDClient(host="localhost", port=8080)

# Generate or load keys
client.generate_rsa_key_pair()

# Initialize server
client.initialize_server()

# Execute commands
result = client.execute_command("echo 'Hello'")

# File operations
client.write_file("content", "/tmp/file.txt")
client.upload_file("local.py", "/tmp/remote.py")
client.download_file("/tmp/output.txt", "local.txt")

# Cleanup
client.cleanup()

# Or use context manager
with PicoDClient(host="localhost", port=8080) as client:
    client.generate_rsa_key_pair()
    result = client.execute_command("whoami")
```

## Security Features

- **RSA-2048**: Industry-standard 2048-bit RSA encryption
- **SHA-256**: Cryptographic hashing for integrity
- **Timestamp**: 5-minute window to prevent replay attacks
- **Automatic signature**: Transparent to the user

## Troubleshooting

### 401 Unauthorized
- Ensure server is initialized: `client.initialize_server()`
- Check that server is running: `go run cmd/picod/main.go`

### 403 Forbidden
- Server already initialized by another client
- Delete server's `picod_public_key.pem` file and retry

### Import Errors
- Install dependencies: `pip install cryptography requests`
- Set PYTHONPATH: `export PYTHONPATH=/path/to/sdk-python`

## Migration from SSH

If you're migrating from SSH-based client:

```python
# Old (SSH)
from agentcube.clients.ssh_client import SandboxSSHClient
client = SandboxSSHClient(host, port)
client.generate_ssh_key_pair()

# New (RSA)
from agentcube.clients.picod_client import PicoDClient
client = PicoDClient(host, port)
client.generate_rsa_key_pair()
```

The API remains the same for most operations!

## Files

- `picod_example.py` - Complete RSA authentication example
- `examples.py` - Original examples (legacy)
- `README.md` - This file

## Learn More

- Review `picod_client.py` source code for implementation details
