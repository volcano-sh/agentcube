---
sidebar_position: 5
---

# API Reference

REST APIs for all AgentCube components. All bodies are JSON (`Content-Type: application/json`) unless noted.

---

## AgentCube Router API

The Router is the primary entry point for all client traffic. It validates authentication, manages session state, and proxies requests to the correct sandbox.

**Base URL**: `http://<agentcube-router>:<port>`

---

### Invoke AgentRuntime

Sends a request to a named `AgentRuntime`'s sandbox. If no `x-agentcube-session-id` header is provided, a new session (and sandbox) is created automatically.

```
POST /v1/namespaces/{namespace}/agent-runtimes/{name}/invocations/*path
```

**Path Parameters**

| Parameter   | Description                                                   |
| ----------- | ------------------------------------------------------------- |
| `namespace` | Kubernetes namespace where the `AgentRuntime` resource exists |
| `name`      | Name of the `AgentRuntime` resource                           |
| `*path`     | Any sub-path forwarded to the agent container                 |

**Request Headers**

| Header                   | Required    | Description                                                         |
| ------------------------ | ----------- | ------------------------------------------------------------------- |
| `x-agentcube-session-id` | No          | Session ID from a previous invocation. Omit to start a new session. |
| `Authorization`          | Conditional | Bearer JWT if external OIDC auth is configured on the Router.       |

**Response Headers**

| Header                   | Description                                                            |
| ------------------------ | ---------------------------------------------------------------------- |
| `x-agentcube-session-id` | Always present. The session ID for this interaction (new or existing). |

**Example — New Session:**

```bash
curl -X POST \
  http://agentcube-router:8080/v1/namespaces/default/agent-runtimes/my-agent/invocations/ \
  -H "Content-Type: application/json" \
  -d '{"message": "Hello, Agent!"}'
# Response header: x-agentcube-session-id: abc123def456
```

**Example — Resume Session:**

```bash
curl -X POST \
  http://agentcube-router:8080/v1/namespaces/default/agent-runtimes/my-agent/invocations/ \
  -H "x-agentcube-session-id: abc123def456" \
  -H "Content-Type: application/json" \
  -d '{"message": "Continue the task..."}'
```

---

### Invoke CodeInterpreter

Sends a request to a named `CodeInterpreter`'s sandbox.

```
POST /v1/namespaces/{namespace}/code-interpreters/{name}/invocations/*path
```

**Path Parameters**

| Parameter   | Description                                                                 |
| ----------- | --------------------------------------------------------------------------- |
| `namespace` | Kubernetes namespace where the `CodeInterpreter` resource exists            |
| `name`      | Name of the `CodeInterpreter` resource                                      |
| `*path`     | Sub-path forwarded to the PicoD daemon (e.g., `/api/execute`, `/api/files`) |

**Request / Response Headers**: Same as AgentRuntime invocation above.

---

## Workload Manager API

The Workload Manager is an **internal** control plane service. It is called by the Router — not directly by end users. These endpoints are documented here for operators and developers who need to understand or debug the system.

**Base URL**: `http://<workloadmanager>:8080`

---

### Create AgentRuntime Session

Provisions a new sandbox for an `AgentRuntime` resource.

```
POST /v1/agent-runtime
```

**Request Body:**

```json
{
  "namespace": "default",
  "name": "my-agent"
}
```

| Field       | Type     | Required | Description                        |
| ----------- | -------- | -------- | ---------------------------------- |
| `namespace` | `string` | Yes      | Namespace of the `AgentRuntime` CR |
| `name`      | `string` | Yes      | Name of the `AgentRuntime` CR      |

**Response Body (200 OK):**

```json
{
  "sessionId": "7f8b9c0d1e2f3g4h5i6j7k8l9m0n",
  "sandboxId": "abc123def456ghi789jkl012mno345",
  "sandboxName": "my-sandbox",
  "entryPoints": [
    {
      "path": "/",
      "protocol": "http",
      "endpoint": "10.0.0.5:8080"
    }
  ]
}
```

---

### Delete AgentRuntime Session

Deletes a sandbox and cleans up the associated session from the registry.

```
DELETE /v1/agent-runtime/sessions/{sessionId}
```

**Path Parameters**

| Parameter   | Description                 |
| ----------- | --------------------------- |
| `sessionId` | The session ID to terminate |

**Response**: `200 OK` on success.

---

### Create CodeInterpreter Session

Provisions a new sandbox for a `CodeInterpreter` resource. If `warmPoolSize > 0`, an already-warmed Pod is adopted.

```
POST /v1/code-interpreter
```

**Request Body:**

```json
{
  "namespace": "default",
  "name": "my-interpreter"
}
```

**Response Body (200 OK):**

```json
{
  "sessionId": "7f8b9c0d1e2f3g4h5i6j7k8l9m0n",
  "sandboxId": "abc123def456ghi789jkl012mno345",
  "sandboxName": "my-sandbox",
  "entryPoints": [
    {
      "path": "/",
      "protocol": "http",
      "endpoint": "10.0.0.6:8080"
    }
  ]
}
```

---

### Delete CodeInterpreter Session

```
DELETE /v1/code-interpreter/sessions/{sessionId}
```

**Response**: `200 OK` on success.

---

## PicoD (Sandbox Daemon) API

PicoD is the lightweight RESTful daemon running **inside every CodeInterpreter sandbox**. It handles command execution and file transfers.

**Base URL**: `http://<sandbox-pod-ip>:8080`

:::note
PicoD is accessed indirectly through the AgentCube Router. Direct access is only needed for debugging or local development.
:::

---

### Initialize Sandbox

**One-time endpoint** called by the Workload Manager when a sandbox is allocated to a user. Injects the user's session public key, making the sandbox exclusively accessible to that user.

```
POST /init
```

**Request Headers:**

```http
Authorization: Bearer <init_jwt>
```

The `<init_jwt>` is a JWT signed by the Workload Manager's bootstrap private key. Its claims contain the user's session public key:

```json
{
  "session_public_key": "LS0tLS1CRUdJTi...",
  "iat": 1732531800,
  "exp": 1732553400
}
```

**Response (200 OK):**

```json
{
  "message": "Server initialized successfully. This PicoD instance is now locked to your public key."
}
```

**Errors:**

- `401 Unauthorized` — Invalid or expired init JWT.
- `409 Conflict` — Sandbox is already initialized.

---

### Execute Command

Executes a shell command inside the sandbox and returns the output.

```
POST /api/execute
```

**Request Headers:**

```http
Authorization: Bearer <session_jwt>
Content-Type: application/json
```

**Request Body:**

```json
{
  "command": ["python3", "-c", "print('Hello World')"],
  "timeout": "30s",
  "working_dir": "/workspace",
  "env": {
    "MY_VAR": "value"
  }
}
```

| Field         | Type                | Required | Description                                                  |
| ------------- | ------------------- | -------- | ------------------------------------------------------------ |
| `command`     | `[]string`          | Yes      | Command and arguments as an array                            |
| `timeout`     | `string`            | No       | Execution timeout (e.g., `"30s"`, `"5m"`). Default: `"30s"`. |
| `working_dir` | `string`            | No       | Working directory for the command. Default: `/`              |
| `env`         | `map[string]string` | No       | Additional environment variables for the process             |

**Response (200 OK):**

```json
{
  "stdout": "Hello World\n",
  "stderr": "",
  "exit_code": 0,
  "duration": 0.12,
  "start_time": "2025-11-18T10:30:00Z",
  "end_time": "2025-11-18T10:30:00.12Z"
}
```

**Error Response (RFC 7807):**

```json
{
  "type": "https://example.com/errors/unauthorized",
  "title": "Unauthorized",
  "status": 401,
  "detail": "Invalid token"
}
```

---

### Upload File

Uploads a file to the sandbox filesystem. Supports two content formats.

```
POST /api/files
```

**Request Headers:**

```http
Authorization: Bearer <session_jwt>
```

**Option 1: Multipart Form Data** (recommended for binary files)

```http
Content-Type: multipart/form-data; boundary=----WebKitFormBoundary

------WebKitFormBoundary
Content-Disposition: form-data; name="path"
/workspace/data.csv
------WebKitFormBoundary
Content-Disposition: form-data; name="file"; filename="data.csv"
Content-Type: text/csv

[file content]
------WebKitFormBoundary
Content-Disposition: form-data; name="mode"
0644
------WebKitFormBoundary--
```

**Option 2: JSON with Base64** (for text files or API convenience)

```json
{
  "path": "/workspace/script.py",
  "content": "cHJpbnQoJ2hlbGxvJyk=",
  "mode": "0644"
}
```

| Field     | Type     | Description                                                     |
| --------- | -------- | --------------------------------------------------------------- |
| `path`    | `string` | Absolute path inside the sandbox where the file should be saved |
| `content` | `string` | Base64-encoded file content (JSON mode only)                    |
| `mode`    | `string` | Unix file permissions (e.g., `"0644"`)                          |

**Response (200 OK):**

```json
{
  "path": "/workspace/script.py",
  "size": 1024,
  "mode": "0644",
  "modified": "2025-11-18T10:30:00Z"
}
```

---

### Download File

Downloads a file from the sandbox filesystem.

```
GET /api/files/{path}
```

**Path Parameters:**

| Parameter | Description                                                      |
| --------- | ---------------------------------------------------------------- |
| `path`    | Absolute path inside the sandbox (e.g., `workspace/result.json`) |

**Request Headers:**

```http
Authorization: Bearer <session_jwt>
```

**Response:**

```http
HTTP/1.1 200 OK
Content-Type: text/plain
Content-Length: 1024
Content-Disposition: attachment; filename="result.json"

[file content]
```

For binary files, the appropriate `Content-Type` is set (e.g., `application/octet-stream`, `image/png`).

---

### List Files

Lists files in a directory within the sandbox.

```
GET /api/files?path={directory}
```

**Query Parameters:**

| Parameter | Required | Description                           |
| --------- | -------- | ------------------------------------- |
| `path`    | No       | Directory path to list (default: `/`) |

**Request Headers:**

```http
Authorization: Bearer <session_jwt>
```

**Response (200 OK):**

```json
[
  {
    "name": "result.json",
    "path": "/workspace/result.json",
    "size": 1024,
    "is_dir": false,
    "modified": "2025-11-18T10:30:00Z",
    "mode": "0644"
  },
  {
    "name": "data",
    "path": "/workspace/data",
    "is_dir": true,
    "modified": "2025-11-18T10:00:00Z"
  }
]
```

---

### Health Check

Returns the health status of the PicoD daemon. No authentication required.

```
GET /health
```

**Response (200 OK):**

```json
{
  "status": "ok",
  "uptime": "2h34m12s"
}
```

---

## Python SDK Reference

The AgentCube Python SDK provides a high-level wrapper over the Workload Manager and PicoD APIs.

### `CodeInterpreterClient`

**Constructor Parameters:**

| Parameter              | Type   | Default                    | Description                                          |
| ---------------------- | ------ | -------------------------- | ---------------------------------------------------- |
| `name`                 | `str`  | `"simple-codeinterpreter"` | Name of the `CodeInterpreter` CRD                    |
| `namespace`            | `str`  | `"default"`                | Kubernetes namespace                                 |
| `ttl`                  | `int`  | `3600`                     | Session time-to-live in seconds                      |
| `workload_manager_url` | `str`  | `None`                     | Workload Manager URL (or set `WORKLOAD_MANAGER_URL`) |
| `router_url`           | `str`  | `None`                     | Router URL (or set `ROUTER_URL`)                     |
| `auth_token`           | `str`  | `None`                     | Auth token (falls back to K8s ServiceAccount token)  |
| `session_id`           | `str`  | `None`                     | Resume an existing session                           |
| `verbose`              | `bool` | `False`                    | Enable debug logging                                 |

**Methods:**

| Method            | Signature                                              | Description                                           |
| ----------------- | ------------------------------------------------------ | ----------------------------------------------------- |
| `execute_command` | `(command: str) -> str`                                | Run a shell command and return stdout                 |
| `run_code`        | `(language: str, code: str, timeout: int = 30) -> str` | Execute a code block. Supported: `"python"`, `"bash"` |
| `upload_file`     | `(local_path: str, remote_path: str) -> None`          | Upload a local file to the sandbox                    |
| `download_file`   | `(remote_path: str, local_path: str) -> str`           | Download a file from the sandbox                      |
| `write_file`      | `(content: str, remote_path: str) -> None`             | Write string content directly to a remote path        |
| `list_files`      | `(path: str = "/") -> list`                            | List files in a sandbox directory                     |
| `stop`            | `() -> None`                                           | Terminate the session and free the sandbox            |

**Context Manager (recommended):**

```python
from agentcube import CodeInterpreterClient

with CodeInterpreterClient(name="my-interpreter") as client:
    output = client.run_code("python", "print('Hello from AgentCube!')")
    print(output)
# Session is automatically stopped when the `with` block exits
```
