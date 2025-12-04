# Example to test api of workloadmanager

This test program demonstrates and validates the api of workloadmanager

## What it does

1. **Generates SSH Key Pair**: Creates an Ed25519 public/private key pair
2. **Creates Session**: Sends the public key to workloadmanager when creating a session (waits for sandbox to be running)
3. **Establishes Tunnel**: Creates an HTTP CONNECT tunnel to the sandbox
4. **SSH Connection**: Connects via SSH using private key authentication (no password!)
5. **Executes Commands**: Runs basic test commands to verify SSH connectivity
6. **Uploads File**: Uses SFTP to upload a Python script to the sandbox
7. **Executes Script**: Runs the Python script which generates output data
8. **Downloads File**: Uses SFTP to download the generated output file
9. **Verifies Output**: Validates the downloaded file content

## Prerequisites

- workloadmanager running (locally or in Kubernetes)
- Sandbox image built with SSH key support
- Kubernetes cluster with agent-sandbox controller (if deploying sandboxes)
- Service Account with appropriate RBAC permissions (see below)

## Permission Model

**Important:** workloadmanager now uses a caller-permission-based operation model. This means:

1. **All operations are executed using the caller's Service Account Token**
   - When creating a Sandbox, it's created in the caller's namespace using their permissions
   - When deleting a Sandbox, it's deleted using the caller's permissions

2. **The caller's Service Account must have appropriate RBAC permissions**
   - Must have `create` permission for `sandboxes` resources (in their namespace)
   - Must have `delete` permission for `sandboxes` resources (in their namespace)
   - Must have `get` and `list` permissions to view Sandbox status

3. **Sandboxes are created in the caller's namespace**
   - No longer centrally created in workloadmanager's namespace
   - Each Service Account manages Sandboxes in their own namespace

### RBAC Configuration Example

Configure necessary permissions for your Service Account:

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: my-app
  namespace: default
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: sandbox-manager
  namespace: default
rules:
- apiGroups: ["agents.x-k8s.io"]
  resources: ["sandboxes"]
  verbs: ["create", "delete", "get", "list"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: my-app-sandbox-manager
  namespace: default
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: sandbox-manager
subjects:
- kind: ServiceAccount
  name: my-app
  namespace: default
```

## Building

```bash
cd example
go mod tidy
go build -o client client.go
```

## Running

### Using Your Service Account Token

**Important:** Use a Service Account Token that has permissions to create/delete Sandboxes.

```bash
# 1. Generate token for your Service Account
# Replace my-app if using a different Service Account name
export API_TOKEN=$(kubectl create token my-app -n default --duration=24h)

# 2. Run client
./client
```

Sandboxes will be created in the `default` namespace.

### Using Custom API URL

```bash
# Replace with your Service Account information
export API_TOKEN=$(kubectl create token my-app -n default --duration=24h)
API_URL=http://your-server:8080 ./client
```

### From Project Root

```bash
# 1. Generate token
export API_TOKEN=$(kubectl create token my-app -n default --duration=24h)

# 2. Run directly
go run ./example/client.go
```

## Expected Output

```
===========================================
SSH Key-based Authentication Test
===========================================

✅ API authentication token loaded from environment

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