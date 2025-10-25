# SSH Key-based Authentication Integration Test

This test program demonstrates and validates SSH key-based authentication for pico-apiserver sessions.

## What it does

1. **Generates SSH Key Pair**: Creates an Ed25519 public/private key pair
2. **Creates Session**: Sends the public key to pico-apiserver when creating a session (waits for sandbox to be running)
3. **Establishes Tunnel**: Creates an HTTP CONNECT tunnel to the sandbox
4. **SSH Connection**: Connects via SSH using private key authentication (no password!)
5. **Executes Commands**: Runs test commands to verify everything works

## Prerequisites

- pico-apiserver running (locally or in Kubernetes)
- Sandbox image built with SSH key support
- Kubernetes cluster with agent-sandbox controller (if deploying sandboxes)

## Building

```bash
cd test-integration
go mod tidy
go build -o client client.go
```

## Running

### Default (local pico-apiserver)

```bash
./client
```

### Custom API URL

```bash
API_URL=http://your-server:8080 ./client
```

### From project root

```bash
# Run directly
go run ./test-integration/client.go

# Or use the helper target
make client
```

## Expected Output

```
===========================================
SSH Key-based Authentication Test
===========================================

Step 1: Generating SSH key pair...
✅ SSH key pair generated
   Public key: ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIB2VExampleBa...

Step 2: Creating session with SSH public key...
✅ Session created: d6bdc5a3-c963-4c0f-be75-bb8083739883

Step 3: Establishing HTTP CONNECT tunnel...
✅ HTTP CONNECT tunnel established

Step 4: Connecting via SSH with private key authentication...
✅ SSH connection established with key-based auth

Step 5: Executing test commands...
   [1/5] Executing: whoami
      Output: sandbox
   [2/5] Executing: pwd
      Output: /workspace
   [3/5] Executing: echo 'Hello from SSH with key auth!'
      Output: Hello from SSH with key auth!
   [4/5] Executing: python --version
      Output: Python 3.11.9
   [5/5] Executing: uname -a
      Output: Linux sandbox-d6bdc5a3 5.15.0-91-generic ...

===========================================
🎉 All tests passed successfully!
===========================================

Summary:
  ✅ SSH key pair generated
  ✅ Session created with public key
  ✅ HTTP CONNECT tunnel established
  ✅ SSH connection with key-based auth
  ✅ Commands executed successfully

Session ID: d6bdc5a3-c963-4c0f-be75-bb8083739883
```

## How it Works

### 1. Key Generation

The test generates an Ed25519 key pair (modern, secure, fast):

```go
pubKey, privKey, _ := ed25519.GenerateKey(rand.Reader)
signer, _ := ssh.NewSignerFromKey(privKey)
```

### 2. Session Creation with Public Key

The public key is sent in the session creation request:

```json
{
  "ttl": 3600,
  "image": "sandbox:latest",
  "sshPublicKey": "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIB2V..."
}
```

### 3. Sandbox Setup

创建session时，pico-apiserver会等待sandbox运行到running状态后才返回。它将公钥通过`SSH_PUBLIC_KEY`环境变量传递给sandbox。

sandbox的`entrypoint.sh`会安装公钥：

```bash
if [ -n "$SSH_PUBLIC_KEY" ]; then
    echo "$SSH_PUBLIC_KEY" > /home/sandbox/.ssh/authorized_keys
    chmod 600 /home/sandbox/.ssh/authorized_keys
fi
```

### 4. SSH Connection

The test connects using the private key (no password needed):

```go
config := &ssh.ClientConfig{
    User: "sandbox",
    Auth: []ssh.AuthMethod{
        ssh.PublicKeys(privateKey),
    },
}
```

## Troubleshooting

### Connection Refused

```
Error: failed to connect: connection refused
```

**解决方案**: 确保pico-apiserver正在运行:
```bash
# Local
make run

# Or check Kubernetes
kubectl get pods -l app=pico-apiserver
```

### Session Creation Timeout

```
Error: failed to create session: request timeout
```

**解决方案**: Sandbox创建可能需要较长时间（拉取镜像、启动pod等），检查pod状态：
```bash
kubectl get pods -l managed-by=pico-apiserver
kubectl logs <sandbox-pod-name>
```

### Authentication Failed

```
Error: SSH handshake failed: ssh: unable to authenticate
```

**Possible causes**:
1. Sandbox image doesn't have SSH key support (rebuild with updated Dockerfile)
2. Environment variable not being passed correctly
3. Permissions issue in sandbox

**Debug**:
```bash
# Check if env var is set
kubectl exec <sandbox-pod> -- env | grep SSH_PUBLIC_KEY

# Check authorized_keys file
kubectl exec <sandbox-pod> -- cat /home/sandbox/.ssh/authorized_keys

# Check permissions
kubectl exec <sandbox-pod> -- ls -la /home/sandbox/.ssh/
```

### Public Key Format Error

Ensure the public key is in OpenSSH format (single line):
```
ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIB2VExampleKeyMaterial
```

Not PEM format or multi-line format.

## Integration with CI/CD

```yaml
# .github/workflows/test-ssh-key.yml
name: Test SSH Key Auth

on: [push]

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v2
      
      - name: Set up Go
        uses: actions/setup-go@v2
        with:
          go-version: 1.24
      
      - name: Build sandbox image
        run: make sandbox-build
      
      - name: Start pico-apiserver
        run: |
          make build
          ./bin/pico-apiserver &
          sleep 5
      
      - name: Run SSH key test
        run: |
          cd test-integration
          go mod tidy
          go run client.go
```

## Security Notes

### For Testing
- ✅ Generates temporary keys for each test run
- ✅ Keys are only in memory, not saved to disk
- ✅ Uses modern Ed25519 algorithm

### For Production
- ⚠️  Client should generate and securely store their own keys
- ⚠️  Private keys should NEVER be transmitted
- ⚠️  Consider key rotation policies
- ⚠️  Use SSH certificates for larger deployments
- ⚠️  Enable SSH host key verification (not `InsecureIgnoreHostKey`)

## See Also

- [pico-apiserver README](../README.md)
- [Sandbox Image Documentation](../images/sandbox/README.md)
- [API Specification](../api-spec/sandbox-api-spec.yaml)

