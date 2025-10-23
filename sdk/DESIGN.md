# Python Sandbox SDK – Hybrid Design (REST + HTTP CONNECT + SSH/SFTP)

Version: 2.0  
Status: Implemented  
Compatibility: Sandbox API v1.0.0 (see `api-spec/sandbox-api-spec.yaml`)

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
- HTTPConnectTunnel: Issues `CONNECT /sessions/{sessionId}` to the API server with `Proxy-Authorization: Bearer <token>`, then exposes a socket-like object.
- SessionSSHClient: Builds an SSH/SFTP connection over the tunnel for commands and file transfers.

High-level flow:

1) Create session (REST) → receive `sessionId` and metadata.  
2) Open HTTP CONNECT tunnel to `/sessions/{sessionId}` on the same API server.  
3) Authenticate SSH over that tunnel (password or key).  
4) Execute commands and SFTP operations.  
5) Delete session (REST) to clean up.

## 4. Key Concepts

### Sandbox Isolation

Each session corresponds to a sandboxed environment on the server. The server enforces directory isolation and life-cycle cleanup. The SDK does not assume direct filesystem paths; it interacts via SSH/SFTP abstracted by the server-side mapping.

### HTTP CONNECT Tunnel (API Server as Proxy)

- The SDK connects to the API server (same `api_url`) and sends `CONNECT /sessions/{sessionId} HTTP/1.1` with `Proxy-Authorization: Bearer …`.
- On `200 Connection Established`, the socket becomes a raw TCP tunnel to the backend sandbox for that session.
- The SDK then performs a normal SSH handshake through this tunnel.

### Authentication

- REST: Bearer token (JWT or API key wrapper per deployment).
- SSH: Username + password or private key (Paramiko `PKey`). If the server supports token-based SSH auth mapping, the SDK can be extended to use that.

## 5. Public API (SDK)

### 5.1 SessionsClient (REST)

Constructor:

```
SessionsClient(
    api_url: str,
    bearer_token: str,
    timeout: int = 30,
    verify_ssl: bool = True,
)
```

Responsibilities:
- `create_session(ttl: int = 3600, image: Optional[str] = None, metadata: Optional[Dict[str, Any]] = None) -> Session`
    - Optional: `ssh_public_key: Optional[str]` — if provided, sent as `sshPublicKey` in the request body to authorize an SSH public key for the session.
- `list_sessions(limit: int = 50, offset: int = 0) -> Dict[str, Any]`  with keys: `sessions: List[Session]`, `total`, `limit`, `offset`
- `get_session(session_id: str) -> Session`
- `delete_session(session_id: str) -> None`

Validation and behavior:
- `ttl` accepted range typically `[60, 28800]`.
- `limit` in `[1, 100]`, `offset >= 0`.

Errors raised:
- `UnauthorizedError` (HTTP 401)
- `SessionNotFoundError` (HTTP 404)
- `RateLimitError` (HTTP 429) — exposes `limit`, `remaining`, `reset` from response headers
- `SandboxOperationError` (other 4xx/5xx)
- `SandboxConnectionError` (network/TLS issues)

### 5.2 HTTPConnectTunnel

Constructor:

```
HTTPConnectTunnel(
    api_url: str,
    bearer_token: str,
    session_id: str,
    connect_path_template: str = "/sessions/{sessionId}",
    timeout: int = 10,
    verify_ssl: bool = True,
    extra_headers: Optional[Dict[str, str]] = None,
)
```

Responsibilities:
- `open() -> socket.socket`: Issues `CONNECT` to the API server at `connect_path_template` with `Authorization: Bearer <token>`. Returns a socket suitable for SSH; HTTPS is used when `api_url` is `https` (TLS verification controlled by `verify_ssl`). Non-200 responses raise `SandboxConnectionError` with the status line.
- `close()`: Closes the tunnel.

Notes:
- Uses the same Bearer token as REST; no separate gateway is introduced.

### 5.3 SessionSSHClient

Constructor:

```
SessionSSHClient(
    api_url: str,
    bearer_token: str,
    session_id: str,
    username: str,
    password: Optional[str] = None,
    pkey: Optional[paramiko.PKey] = None,
    timeout: int = 20,
    verify_ssl: bool = True,
    connect_path_template: str = "/sessions/{sessionId}",
    host_key_policy: Optional[paramiko.MissingHostKeyPolicy] = None,
    get_pty: bool = False,
)
```

Responsibilities and behavior:
- Context manager support: `__enter__/__exit__` to automatically connect/close.
- `connect()`: Opens the HTTP CONNECT tunnel, then performs Paramiko SSH connection using the tunnel socket (`sock=...`). By default, agent and key lookups may be disabled; host key policy defaults to Paramiko's `AutoAddPolicy` if none is provided.
- `run_command(command: str, timeout: int = 60) -> Dict[str, Any]` returns `{ "stdout": str, "stderr": str, "exit_code": int }`. If `get_pty=True`, allocates a PTY for the command.
- `open_sftp() -> paramiko.SFTPClient`
- `upload_file(local_path: str, remote_path: str) -> None`
- `download_file(remote_path: str, local_path: str) -> None`
- `close()`: Closes SFTP, SSH, and the underlying tunnel in order.

Errors:
- `SandboxOperationError` on SSH/SFTP failures or misuse (e.g., calling operations before `connect`).
- `SandboxConnectionError` propagated from tunnel failures.

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

REST response handling:
- Maps common HTTP statuses to dedicated exceptions (`401` → `UnauthorizedError`, `404` → `SessionNotFoundError`, `429` → `RateLimitError`).
- `RateLimitError` exposes `limit`, `remaining`, and `reset` from `X-RateLimit-*` headers when present.
- Non-JSON error bodies are handled gracefully with generic messages.
- DELETE operations may return `200` or `204` on success.

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
- `verify_ssl` controls certificate verification for CONNECT/TLS (recommended `True` in production).
- The server must verify that the caller is authorized for the referenced `sessionId` during CONNECT and route only to the correct sandbox.
- Optionally enforce short tunnel timeouts and idle timeouts server-side.
- Host key policies: default is `AutoAddPolicy` for ease-of-use; production users may supply stricter policies.

## 10. Server-Side Expectations (for CONNECT)

- Endpoint: `CONNECT /sessions/{sessionId} HTTP/1.1` with `Proxy-Authorization: Bearer <token>`.
- On success: return `HTTP/1.1 200 Connection Established` and forward bytes bidirectionally to the sandbox’s SSH endpoint for that session.
- On failure: return appropriate status (e.g., 401, 404, 429, 5xx).

## 11. Environment Variables

These are used by examples and can simplify configuration during development:

- `SANDBOX_API_URL`: Default base URL for REST and tunnel target.
- `SANDBOX_API_TOKEN`: Default bearer token.

## 12. Limitations and Future Work

- `run_command` is synchronous and collects output at the end; streaming callbacks or non-blocking reads may be added.
- No direct wrappers for REST `/commands` and `/files` in this client; SSH/SFTP is the primary data plane, though REST endpoints exist on the server.
- Optional token-based SSH auth mapping (skip username/password if server supports it).
- Async SDK variant using `asyncssh` and an async HTTP client could be provided.

