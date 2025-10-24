# Tunnel Test Tool

A simple Go program to test the HTTP CONNECT tunnel functionality of pico-apiserver.

## Features

- Establishes HTTP CONNECT tunnel to pico-apiserver
- Creates SSH connection over the tunnel
- Executes test commands to verify functionality
- Provides detailed logging of each step

## Usage

### Basic Usage

```bash
# Build the test tool
go build -o bin/test-tunnel ./cmd/test-tunnel

# Run with session ID
./bin/test-tunnel -session <session-id>
```

### With All Options

```bash
./bin/test-tunnel \
  -api http://localhost:8080 \
  -session d6bdc5a3-c963-4c0f-be75-bb8083739883 \
  -token "your-auth-token" \
  -user sandbox \
  -password sandbox \
  -cmd "echo 'Hello from sandbox'"
```

### Using Makefile

```bash
# Add to Makefile for convenience
make test-tunnel SESSION_ID=<session-id>
```

## Command Line Flags

| Flag        | Default                     | Description                              |
| ----------- | --------------------------- | ---------------------------------------- |
| `-api`      | `http://localhost:8080`     | pico-apiserver URL                       |
| `-session`  | *required*                  | Session ID to connect to                 |
| `-token`    | `""`                        | Authorization token (if auth is enabled) |
| `-user`     | `sandbox`                   | SSH username                             |
| `-password` | `sandbox`                   | SSH password                             |
| `-cmd`      | `echo 'Hello from sandbox'` | Command to execute                       |

## Example Output

```
2025/10/24 03:45:00 Testing tunnel connection to session: d6bdc5a3-c963-4c0f-be75-bb8083739883
2025/10/24 03:45:00 Connecting to localhost:8080
2025/10/24 03:45:00 Sending CONNECT request to /v1/sessions/d6bdc5a3-c963-4c0f-be75-bb8083739883/tunnel
2025/10/24 03:45:00 Received response: 200 Connection Established
2025/10/24 03:45:00 ✅ HTTP CONNECT tunnel established successfully
2025/10/24 03:45:00 Establishing SSH connection as user: sandbox
2025/10/24 03:45:01 ✅ SSH connection established successfully
2025/10/24 03:45:01 ✅ Command executed successfully

--- Command Output ---
Command: echo 'Hello from sandbox'
Exit Code: 0
Stdout:
Hello from sandbox

--- End Output ---

2025/10/24 03:45:01 Running additional tests...
2025/10/24 03:45:01 ✅ Current directory: /workspace
2025/10/24 03:45:01 ✅ Current user: sandbox
2025/10/24 03:45:01 ✅ Python version: Python 3.11.x

🎉 All tests completed successfully!
```

## Testing Workflow

### Step 1: Start pico-apiserver

```bash
# Local development
make run

# Or in Kubernetes
make k8s-deploy
```

### Step 2: Create a Session

```bash
# Create session
curl -X POST http://localhost:8080/v1/sessions \
  -H "Content-Type: application/json" \
  -d '{
    "ttl": 3600,
    "image": "sandbox:latest"
  }'

# Response will contain session_id
```

### Step 3: Wait for Sandbox to be Ready

```bash
# Check sandbox pod status
kubectl get pods -l managed-by=pico-apiserver

# Wait for Running status
```

### Step 4: Run Tunnel Test

```bash
# Use the session_id from step 2
./bin/test-tunnel -session <session-id>
```

## What This Tool Tests

1. **HTTP CONNECT Tunnel**
   - TCP connection to pico-apiserver
   - CONNECT request formatting
   - Response parsing
   - Connection hijacking

2. **SSH Protocol**
   - SSH handshake over tunnel
   - Authentication
   - Session creation

3. **Command Execution**
   - Running commands in sandbox
   - Capturing stdout/stderr
   - Exit code handling

4. **Integration**
   - End-to-end workflow
   - Connection lifecycle
   - Data transfer

## Troubleshooting

### Connection Refused

```
Error: failed to connect to server: connection refused
```

**Solution**: Ensure pico-apiserver is running
```bash
# Check if server is running
curl http://localhost:8080/health

# Or check pod status
kubectl get pods -l app=pico-apiserver
```

### Session Not Found

```
Error: CONNECT failed with status 404: Session not found
```

**Solution**: Verify session ID is correct and session exists
```bash
# List sessions
curl http://localhost:8080/v1/sessions
```

### SSH Connection Failed

```
Error: failed to establish SSH connection: ssh: handshake failed
```

**Possible causes**:
1. Sandbox pod not ready yet (wait a bit longer)
2. SSH service not started in sandbox
3. Wrong credentials

**Debug**:
```bash
# Check pod status
kubectl get pod <sandbox-pod-name>

# Check pod logs
kubectl logs <sandbox-pod-name>

# Check SSH service in pod
kubectl exec <sandbox-pod-name> -- ps aux | grep sshd
```

### Authentication Failed

```
Error: failed to establish SSH connection: ssh: unable to authenticate
```

**Solution**: Check username and password
```bash
# Verify credentials
./bin/test-tunnel -session <id> -user sandbox -password sandbox
```

## Advanced Usage

### Test with Custom Command

```bash
# Execute Python code
./bin/test-tunnel -session <id> -cmd "python -c 'print(2+2)'"

# List files
./bin/test-tunnel -session <id> -cmd "ls -la /workspace"

# Check environment
./bin/test-tunnel -session <id> -cmd "env"
```

### Test Long-Running Command

```bash
# Sleep test (connection stability)
./bin/test-tunnel -session <id> -cmd "sleep 5 && echo 'Done'"

# Large output test
./bin/test-tunnel -session <id> -cmd "dd if=/dev/zero bs=1M count=10 | base64"
```

### Automated Testing Script

```bash
#!/bin/bash
# test_tunnel_all.sh

SESSION_ID=$1

if [ -z "$SESSION_ID" ]; then
    echo "Usage: $0 <session-id>"
    exit 1
fi

echo "Running tunnel tests for session: $SESSION_ID"

# Test 1: Basic command
echo "Test 1: Basic command"
./bin/test-tunnel -session $SESSION_ID -cmd "echo 'Test 1 passed'"

# Test 2: Python execution
echo "Test 2: Python execution"
./bin/test-tunnel -session $SESSION_ID -cmd "python -c 'print(\"Test 2 passed\")'"

# Test 3: File operations
echo "Test 3: File operations"
./bin/test-tunnel -session $SESSION_ID -cmd "echo 'test data' > /tmp/test.txt && cat /tmp/test.txt"

# Test 4: Network check
echo "Test 4: Network check"
./bin/test-tunnel -session $SESSION_ID -cmd "ping -c 1 8.8.8.8"

echo "All tests completed"
```

## Integration with CI/CD

```yaml
# .github/workflows/test-tunnel.yml
name: Test Tunnel

on: [push, pull_request]

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v2
      
      - name: Set up Go
        uses: actions/setup-go@v2
        with:
          go-version: 1.24
      
      - name: Build test tool
        run: go build -o bin/test-tunnel ./cmd/test-tunnel
      
      - name: Start pico-apiserver
        run: |
          make build
          make run &
          sleep 5
      
      - name: Create session
        run: |
          SESSION_ID=$(curl -X POST http://localhost:8080/v1/sessions \
            -H "Content-Type: application/json" \
            -d '{"ttl": 3600}' | jq -r '.session_id')
          echo "SESSION_ID=$SESSION_ID" >> $GITHUB_ENV
      
      - name: Run tunnel test
        run: ./bin/test-tunnel -session $SESSION_ID
```

## See Also

- [pico-apiserver README](../../README.md)
- [Tunnel Implementation](../../pkg/pico-apiserver/tunnel.go)
- [Sandbox Image](../../images/sandbox/README.md)

