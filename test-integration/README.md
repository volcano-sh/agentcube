# SSH Key-based Authentication Integration Test

This test program demonstrates and validates SSH key-based authentication for pico-apiserver sessions.

## What it does

1. **Generates SSH Key Pair**: Creates an Ed25519 public/private key pair
2. **Creates Session**: Sends the public key to pico-apiserver when creating a session (waits for sandbox to be running)
3. **Establishes Tunnel**: Creates an HTTP CONNECT tunnel to the sandbox
4. **SSH Connection**: Connects via SSH using private key authentication (no password!)
5. **Executes Commands**: Runs basic test commands to verify SSH connectivity
6. **Uploads File**: Uses SFTP to upload a Python script to the sandbox
7. **Executes Script**: Runs the Python script which generates output data
8. **Downloads File**: Uses SFTP to download the generated output file
9. **Verifies Output**: Validates the downloaded file content

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

Step 5: Executing basic test commands...
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

Step 6: Uploading Python script via SFTP...
✅ Python script uploaded to /workspace/fibonacci.py

Step 7: Executing Python script in sandbox...
   Script output:
   ✅ Generated 20 Fibonacci numbers
      Sum: 6765
      Output written to: /workspace/output.json

Step 8: Downloading generated output file...
✅ Output file downloaded to /tmp/sandbox_output.json

Step 9: Verifying downloaded file...
   File contents:
   {
     "algorithm": "Fibonacci Sequence",
     "count": 20,
     "message": "Generated successfully in sandbox!",
     "numbers": [0, 1, 1, 2, 3, 5, 8, 13, 21, 34, 55, 89, 144, 233, 377, 610, 987, 1597, 2584, 4181],
     "sum": 6765,
     "timestamp": "2025-10-25T12:34:56.789012"
   }
✅ Verified: Generated 20 Fibonacci numbers
✅ Verified: Sum = 6765
✅ Verified: Message = "Generated successfully in sandbox!"

===========================================
🎉 All tests passed successfully!
===========================================

Summary:
  ✅ SSH key pair generated
  ✅ Session created with public key
  ✅ HTTP CONNECT tunnel established
  ✅ SSH connection with key-based auth
  ✅ Basic commands executed successfully
  ✅ Python script uploaded via SFTP
  ✅ Python script executed in sandbox
  ✅ Output file downloaded via SFTP
  ✅ Downloaded file verified

Session ID: d6bdc5a3-c963-4c0f-be75-bb8083739883
Downloaded file: /tmp/sandbox_output.json
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

### 5. File Transfer via SFTP

The test uses SFTP (SSH File Transfer Protocol) over the established SSH connection:

**Upload**:
```go
sftpClient, _ := sftp.NewClient(sshClient)
remoteFile, _ := sftpClient.Create("/workspace/fibonacci.py")
remoteFile.Write([]byte(pythonScript))
```

**Download**:
```go
sftpClient, _ := sftp.NewClient(sshClient)
remoteFile, _ := sftpClient.Open("/workspace/output.json")
io.Copy(localFile, remoteFile)
```

### 6. Python Script Execution

The uploaded Python script:
- Generates 20 Fibonacci numbers
- Creates a JSON file with results
- Includes timestamp and metadata
- Demonstrates code execution capability

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

