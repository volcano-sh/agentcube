# Python Sandbox SDK – Hybrid Design (REST + HTTP CONNECT + SSH/SFTP)

Version: 2.0  
Status: Proposed

## 1. Introduction

This document specifies a hybrid Python SDK for managing and using ephemeral sandboxes. The design separates concerns as follows:

- Session lifecycle (create/list/get/delete) is handled via a REST API.
- Command execution and file transfer are performed over SSH/SFTP, but tunneled through the same API server using an HTTP CONNECT tunnel. The API server acts as the HTTP CONNECT proxy and transparently forwards the TCP stream to the sandbox backend for the given session. No additional gateway is introduced.

This preserves the reliability and streaming characteristics of SSH/SFTP while keeping session control firewall-friendly (HTTPS) and easily scalable.

## 2. Goals and Non-Goals

### Goals

- Ease of Use: Simple, Pythonic API mirroring prior SSH SDK ergonomics.
- Isolation: Each session maps 1:1 to an isolated sandbox environment on the server side.
- Security: HTTP over TLS (REST) with Bearer tokens; SSH authentication (password or key) over a tunnel initiated with the same Bearer token.
- Automatic Cleanup: Sessions expire by TTL or explicit deletion; server removes resources.
- Streaming-Friendly: Real-time stdout/stderr via SSH channels; efficient SFTP transfers.

### Non-Goals

- Complex Provisioning: The SDK does not install packages or manage system configuration inside the sandbox.
- Persistent Environments: Sessions are ephemeral; no long-lived state management.
- Extra Gateways: The API server itself terminates CONNECT and forwards to the sandbox. We do not deploy or depend on any separate proxy/gateway.

## 3. Architecture Overview

Client-side SDK components:

- SessionsClient (REST): Manages sessions via HTTPS against `POST/GET/DELETE /sessions`.
- HTTPConnectTunnel: Issues `CONNECT /sessions/{sessionId}/tunnel` to the API server with `Authorization: Bearer <token>`, then exposes a socket-like object.
- SessionSSHClient: Builds an SSH/SFTP connection over the tunnel for commands and file transfers.

High-level flow:

1) Create session (REST) → receive `sessionId` and metadata.  
2) Open HTTP CONNECT tunnel to `/sessions/{sessionId}/tunnel` on the same API server.  
3) Authenticate SSH over that tunnel (password or key).  
4) Execute commands and SFTP operations.  
5) Delete session (REST) to clean up.

## 4. Key Concepts

### Sandbox Isolation

Each session corresponds to a sandboxed environment on the server. The server enforces directory isolation and life-cycle cleanup. The SDK does not assume direct filesystem paths; it interacts via SSH/SFTP abstracted by the server-side mapping.

### HTTP CONNECT Tunnel (API Server as Proxy)

- The SDK connects to the API server (same `api_url`) and sends `CONNECT /sessions/{sessionId}/tunnel HTTP/1.1` with `Authorization: Bearer …`.
- On `200 Connection Established`, the socket becomes a raw TCP tunnel to the backend sandbox for that session.
- The SDK then performs a normal SSH handshake through this tunnel.

### Authentication

- REST: Bearer token (JWT or API key wrapper per deployment).
- SSH: Username + password or private key (Paramiko `PKey`). If the server supports token-based SSH auth mapping, the SDK can be extended to use that.

## 5. Public API (SDK)

### 5.1 SessionsClient (REST)

Responsibilities:
- `create_session(ttl, image, metadata) -> Session`
- `list_sessions(limit, offset) -> {sessions, total, …}`
- `get_session(session_id) -> Session`
- `delete_session(session_id) -> None`

Errors: `UnauthorizedError`, `SessionNotFoundError`, `RateLimitError`, `SandboxOperationError`, `SandboxConnectionError`.

### 5.2 HTTPConnectTunnel

Responsibilities:
- `open() -> socket.socket`: Issues CONNECT to `/sessions/{sessionId}/tunnel` on the API server; returns a TLS-wrapped or plain socket based on `api_url`.
- `close()`: Closes the tunnel.

Notes: Uses the same Bearer token as REST; no new gateway introduced.

### 5.3 SessionSSHClient

Responsibilities:
- `connect() / close()` and context manager support.
- `run_command(command: str, timeout: int = 60) -> {stdout, stderr, exit_code}`
- `upload_file(local_path: str, remote_path: str) -> None`
- `download_file(remote_path: str, local_path: str) -> None`
- `open_sftp() -> paramiko.SFTPClient`

Construction parameters include: `api_url`, `bearer_token`, `session_id`, `username`, and optional `password` or `pkey`. A `get_pty` flag enables PTY allocation for interactive commands.

## 6. Error Handling

Exception hierarchy (client-side):

```
SandboxAPIError (Base)
├── SandboxConnectionError    # Networking / TLS / CONNECT issues
├── UnauthorizedError         # 401 REST auth failures
├── SessionNotFoundError      # 404 REST session missing
├── RateLimitError            # 429 + rate-limit headers
└── SandboxOperationError     # Other 4xx/5xx and SSH/SFTP operation failures
```

Examples:
- CONNECT returns non-200 → `SandboxConnectionError` with reason line.
- SSH handshake fails → `SandboxOperationError` with Paramiko error message.
- REST DELETE 404 → `SessionNotFoundError`.

## 7. Dependencies

- `requests` – REST client
- `paramiko` – SSH/SFTP client

## 8. Example Usage

```python
from sandbox_sessions_sdk import SessionsClient, SessionSSHClient

API_URL = "https://api.sandbox.example.com/v1"
TOKEN = "<bearer-jwt>"
USERNAME = "sandbox"  # or as required by backend

# 1) Create a session via REST
with SessionsClient(api_url=API_URL, bearer_token=TOKEN) as sessions:
    session = sessions.create_session(ttl=3600, image="python:3.11", metadata={"project": "demo"})

# 2) Use SSH/SFTP via HTTP CONNECT to the same API server
with SessionSSHClient(
    api_url=API_URL,
    bearer_token=TOKEN,
    session_id=session.session_id,
    username=USERNAME,
) as ssh:
    # Run a command
    res = ssh.run_command("python3 --version")
    print(res["stdout"])  # real-time-friendly (collects at end in this example)

    # Upload and execute a script
    ssh.upload_file("local_script.py", "remote_script.py")
    print(ssh.run_command("python3 remote_script.py")["stdout"]) 

    # Download results
    ssh.download_file("output.txt", "output.txt")

# 3) Cleanup via REST
with SessionsClient(api_url=API_URL, bearer_token=TOKEN) as sessions:
    sessions.delete_session(session.session_id)
```

## 9. Security Considerations

- All REST endpoints use HTTPS + Bearer tokens.
- The CONNECT handshake is sent over HTTPS; the tunnel then carries SSH.
- The server must verify that the caller is authorized for the referenced `sessionId` during CONNECT and route only to the correct sandbox.
- Optionally enforce short tunnel timeouts and idle timeouts server-side.
- Host key policies: default is `AutoAddPolicy` for ease-of-use; production users may supply stricter policies.

## 10. Server-Side Expectations (for CONNECT)

- Endpoint: `CONNECT /sessions/{sessionId}/tunnel HTTP/1.1` with `Authorization: Bearer <token>`.
- On success: return `HTTP/1.1 200 Connection Established` and forward bytes bidirectionally to the sandbox’s SSH endpoint for that session.
- On failure: return appropriate status (e.g., 401, 404, 429, 5xx).

## 11. Open Items / Future Work

- Optional token-based SSH auth mapping (skip username/password if server supports it).
- Command output streaming callbacks (line-by-line) using non-blocking channels.
- Async SDK variant using `asyncssh` and an async HTTP client.

