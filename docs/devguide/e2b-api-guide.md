# Using the E2B-Compatible API

AgentCube exposes an E2B-compatible REST API on the Router so you can manage sandboxes, run commands, and manipulate files with the standard E2B Python SDK (or any HTTP client). This guide covers the minimum needed to point the SDK at AgentCube and start a sandbox.

For runnable, end-to-end usage (full lifecycle, template management, multi-turn code-interpreter workflow), see the examples in [`example/e2b/`](../../example/e2b/README.md).

## Prerequisites

- **Python**: Version 3.8 or later.
- **Network access** to the AgentCube Router endpoint that exposes the E2B API (default port `:8081`).
- **API key** issued by your cluster administrator (see [Get an API Key](#get-an-api-key)).
- **SDK installation**:

  ```bash
  pip install e2b e2b-code-interpreter
  ```

## Architecture in a Nutshell

The E2B API surface is split across two layers, both reachable through the AgentCube Router on the same port (`:8081`):

| Layer            | Backend          | Responsibility                                  |
| ---------------- | ---------------- | ----------------------------------------------- |
| **Platform API** | Router (handler) | Sandbox lifecycle, templates, API key auth      |
| **Sandbox API**  | PicoD (proxied)  | In-sandbox filesystem, process, environment ops |

The E2B SDK transparently calls both layers using the `domain` field returned when a sandbox is created — you do not construct sandbox URLs manually.

For the complete architectural design, see [E2B API Architecture Design](../design/e2b-api-architecture.md).

## Get an API Key

> **Important:** AgentCube **never stores raw API keys** in the cluster. Only the SHA-256 hash of each key is persisted (in Secret `e2b-api-keys`) along with its status (`valid` / `revoked` / `expired`); the namespace mapping lives in ConfigMap `e2b-api-key-config`. The raw key value is shown to the operator **once** at provisioning time and cannot be recovered from Kubernetes afterward. If you lose it, revoke the hash and issue a new key.

### Provisioning a Key (Admin)

Use the `kubectl agentcube` CLI to provision a key. The CLI generates a cryptographically random key, computes its SHA-256 hash, writes the status into Secret `e2b-api-keys` and the namespace mapping into ConfigMap `e2b-api-key-config`, and prints the raw key **once** for you to deliver to the consumer:

```bash
# Create a key bound to a specific namespace
kubectl agentcube apikey create --namespace team-ml

# Output:
# API Key:     e2b_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
# Hash:        a1b2c3d4e5f6789...
# Namespace:   team-ml
# Status:      valid
#
# WARNING: this is the only time the raw key is shown.
#          Store it securely - it cannot be retrieved later.
```

If `--namespace` is omitted, the key is bound to the cluster's `defaultNamespace` (configured in ConfigMap `e2b-api-key-config`, falling back to the Router's `E2B_DEFAULT_NAMESPACE` env var, then `default`).

### Inspecting and Revoking Keys

```bash
# List keys (shows hash, namespace, status - never the raw key)
kubectl agentcube apikey list

# Revoke a key by its hash prefix
kubectl agentcube apikey revoke a1b2c3d4
```

Revocation flips the Secret entry to `revoked`; the Router's informer detects the change and rejects subsequent requests with `401`.

### Using a Key (Consumer)

Set the key the admin gave you as `E2B_API_KEY`:

```bash
export E2B_API_KEY="<key-value-delivered-by-admin>"
```

## Configure the Client

Point the E2B SDK at your AgentCube Router rather than the public E2B endpoint via environment variables:

| Variable      | Description                                                        |
| ------------- | ------------------------------------------------------------------ |
| `E2B_API_KEY` | API key for authentication                                         |
| `E2B_DOMAIN`  | Host[:port] of the AgentCube Router (e.g. `agentcube.example.com`) |
| `E2B_HTTPS`   | Set to `true` if the Router endpoint uses HTTPS                    |

Common SDK parameters:

| Parameter     | Type   | Default                      | Description                                    |
| ------------- | ------ | ---------------------------- | ---------------------------------------------- |
| `api_key`     | `str`  | `None`                       | API key (or `E2B_API_KEY` env var)             |
| `template_id` | `str`  | `"default/code-interpreter"` | Plain template name (e.g., `code-interpreter`) |
| `timeout`     | `int`  | `900`                        | Sandbox time-to-live in seconds                |
| `metadata`    | `dict` | `None`                       | Custom metadata stored on the sandbox          |

## Quickstart

A minimal end-to-end snippet:

```python
import os
from e2b_code_interpreter import Sandbox

os.environ["E2B_DOMAIN"] = "agentcube.example.com"
os.environ["E2B_HTTPS"] = "true"

with Sandbox.create(
    api_key=os.environ["E2B_API_KEY"],
    template_id="default/code-interpreter",
    timeout=300,
) as sandbox:
    print(f"Sandbox ID: {sandbox.sandbox_id}")

    execution = sandbox.run_code("print('Hello from AgentCube!')")
    print(execution.logs.stdout)

    sandbox.files.write("/workspace/hello.txt", "hi")
    print(sandbox.files.read("/workspace/hello.txt").decode())
# Sandbox is deleted automatically here.
```

For complete examples covering sandbox lifecycle (`set_timeout`, `refresh`, listing), template CRUD, multi-turn code execution with persistent kernel state, error handling, and concurrent sandboxes, see [`example/e2b/`](../../example/e2b/README.md):

- `01_sandbox_lifecycle.py` — full sandbox lifecycle and context-manager pattern.
- `02_template_management.py` — template CRUD, build polling, and aliases.
- `03_code_interpreter_workflow.py` — multi-turn code execution, filesystem I/O, error handling.

## Error Handling

The Router maps internal failures to HTTP status codes that match E2B's conventions; the SDK translates them into `SandboxException` and its subclasses.

| HTTP Status | E2B SDK Exception          | Cause                                                                 |
| ----------- | -------------------------- | --------------------------------------------------------------------- |
| `400`       | `InvalidArgumentException` | Malformed request body, unsupported feature flag (e.g., `auto_pause`) |
| `401`       | `AuthenticationException`  | Missing or invalid `X-API-Key`                                        |
| `404`       | `NotFoundException`        | Sandbox or template not found, or not owned by the caller             |
| `409`       | `SandboxException`         | Conflicting resource state                                            |
| `429`       | `RateLimitException`       | API key validation rate limit exceeded                                |
| `500`       | `SandboxException`         | Internal failure (Workload Manager, store, or PicoD)                  |
| `503`       | `SandboxException`         | Service temporarily unavailable                                       |

See `example/e2b/01_sandbox_lifecycle.py` and `example/e2b/03_code_interpreter_workflow.py` for structured error-handling patterns.

## Supported Endpoints

| Endpoint                                | Method | Description                            |
| --------------------------------------- | ------ | -------------------------------------- |
| `/sandboxes`                            | POST   | Create sandbox                         |
| `/sandboxes`, `/v2/sandboxes`           | GET    | List sandboxes (scoped to the API key) |
| `/sandboxes/{id}`                       | GET    | Get sandbox details                    |
| `/sandboxes/{id}`                       | DELETE | Delete sandbox                         |
| `/sandboxes/{id}/timeout`               | POST   | Set timeout                            |
| `/sandboxes/{id}/refreshes`             | POST   | Refresh keepalive                      |
| `/templates`, `/v3/templates`           | POST   | Create template                        |
| `/templates`                            | GET    | List templates                         |
| `/templates/{id}`                       | GET    | Get template details                   |
| `/templates/{id}`, `/v2/templates/{id}` | PATCH  | Update template                        |
| `/templates/{id}`                       | DELETE | Delete template                        |

## Unsupported Features

The following endpoints and fields are not supported. Requests to unimplemented endpoints receive `404`; unsupported fields in a create request receive `400`.

| Field / Endpoint                | Behavior                               |
| ------------------------------- | -------------------------------------- |
| `auto_pause: true` on create    | Returns `400 auto_pause not supported` |
| `/sandboxes/{id}/metrics`       | `404 Not Found`                        |
| `/sandboxes/{id}/logs`          | `404 Not Found`                        |
| `/snapshots/*`, `/volumes/*`    | `404 Not Found`                        |
| Pause / Resume                  | `404 Not Found`                        |
| Network configuration           | `404 Not Found`                        |
| Volume mounts on sandbox create | Ignored                                |

For the in-sandbox layer, command execution and filesystem operations rely on PicoD's envd-compatible endpoints — see the [Sandbox API support matrix](../design/e2b-api-architecture.md#sandbox-api--envd-api-support-status-picod-layer) for the current implementation status.

### Falling Back to AgentCube Native APIs

Operations not covered by the E2B layer are still available through AgentCube's native APIs:

| Need                 | Native Endpoint                                                         |
| -------------------- | ----------------------------------------------------------------------- |
| Code execution       | `POST /v1/namespaces/{ns}/code-interpreters/{name}/invocations/execute` |
| File upload/download | PicoD `/api/files` (multipart or base64 JSON) and `/api/files/{path}`   |
| Direct shell exec    | PicoD `/api/execute`                                                    |

These endpoints require AgentCube's JWT authentication rather than the E2B `X-API-Key` header. See [Code Interpreter via Python SDK](./code-interpreter-python-sdk.md) for the higher-level SDK that wraps them.

## See Also

- [`example/e2b/`](../../example/e2b/README.md) — runnable end-to-end examples and extended usage scenarios.
- [E2B API Architecture Design](../design/e2b-api-architecture.md) — full design, data model, and Sandbox API roadmap.
- [Code Interpreter via Python SDK](./code-interpreter-python-sdk.md) — AgentCube native SDK for code execution and file management.
- [Code Interpreter with LangChain](./code-interpreter-using-langchain.md) — wrapping AgentCube as a LangChain tool.
