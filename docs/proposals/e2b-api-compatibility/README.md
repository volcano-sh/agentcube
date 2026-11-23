---
title: E2B API Compatibility Layer for AgentCube
authors:
  - "@acsoto"
reviewers:
  - TBD
approvers:
  - TBD
creation-date: 2026-11-23
last-updated: 2026-11-23
status: draft
tracking-issue: "https://github.com/volcano-sh/agentcube/issues/257"
---

# E2B API Compatibility Layer for AgentCube

## Summary

This proposal defines a layered architecture for making AgentCube compatible with the official E2B protocol and SDKs (`e2b` Python/JS SDKs and `e2b-code-interpreter`). Instead of embedding E2B control-plane logic into Router or re-implementing `envd` inside PicoD, AgentCube will:

1. Introduce an optional, stateless **E2B Gateway** component that translates E2B Platform API calls into **WorkloadManager** lifecycle operations.
2. Provide a dedicated E2B data-plane proxy for `<port>-<sandboxID>.<E2B_DOMAIN>` with exact port routing, streaming/WebSocket forwarding, and `envd`/`code-interpreter` header preservation.
3. Reuse the open-source (Apache-2.0) **`envd`** daemon (port `49983`) and compatible **`code-interpreter`** server (port `49999`) inside opt-in `CodeInterpreter` templates, keeping PicoD focused on AgentCube's native lightweight workflow.

## Motivation

E2B is a widely adopted sandbox protocol across AI frameworks (LangChain, AutoGen, Vercel AI SDK, etc.). Supporting unmodified official E2B SDKs lets users migrate to self-hosted, Kubernetes-native AgentCube by changing only `E2B_API_URL`, `E2B_DOMAIN`, and `E2B_API_KEY`.

Real E2B compatibility requires all three protocol layers to work together:
- **Platform API** (`api.<domain>`): sandbox lifecycle and template discovery.
- **Data Plane Proxy** (`<port>-<sandboxID>.<domain>`): exact port routing, HTTP/2 ConnectRPC streaming, and WebSocket/PTY forwarding.
- **In-Sandbox Runtime**: `envd` on port `49983` (ConnectRPC `process.Process/*`, `filesystem.Filesystem/*`, HTTP `/files`, `/init`) and optional Jupyter/code-interpreter on port `49999` (`/execute`, `/contexts`).

Putting E2B control-plane logic into Router or emulating a partial REST subset in PicoD breaks AgentCube's control-plane/data-plane separation (see #467), creates high maintenance drift against upstream E2B SDKs, and complicates the control-plane decoupling planned in #430.

### Goals

- Run unmodified official `e2b` (Python/JS v1.x/v2.x) and `e2b-code-interpreter` SDKs against AgentCube for sandbox lifecycle, command execution, file operations, stateful code execution, and custom port exposure (`get_host(port)`).
- Keep Router free of Kubernetes write permissions and E2B control-plane state.
- Strengthen shared AgentCube primitives (WorkloadManager session query/TTL/ownership checks and atomic Redis/Valkey store operations) rather than building E2B-only workarounds.
- Support both direct `Sandbox` creation and `SandboxWarmPool` claims, including per-sandbox `envVars` and `envdAccessToken` initialization.

### Non-Goals

- Re-implementing `envd` ConnectRPC/PTY/WatchDir or a Jupyter server inside PicoD.
- Mapping `AgentRuntime` CRDs to E2B sandboxes (only opt-in `CodeInterpreter` CRDs are mapped).
- E2B Template Build APIs (`POST /v3/templates`, Dockerfile cloud builds — deferred until #434 matures; return `501 Not Implemented`).
- Sandbox `pause` / `resume` / `autoPause` / snapshots (deferred until microVM snapshot & pool-based control plane in #430 are available; return `501 Not Implemented`).

## Proposal

```text
Official E2B SDKs (e2b / e2b-code-interpreter)
  │
  ├── Control Plane: https://api.<E2B_DOMAIN>  (X-API-Key)
  │        │
  │        ▼
  │   [agentcube-e2b-gateway]   (optional, stateless control-plane facade)
  │        │  HTTP/mTLS + signed caller identity
  │        ▼
  │   [WorkloadManager]         (single owner of K8s lifecycle, GC, envd /init)
  │        │
  │        ├──▶ Session Store (Redis & Valkey: atomic object + expiry/owner indexes)
  │        └──▶ Kubernetes (CodeInterpreter, Sandbox / SandboxClaim / WarmPool)
  │
  └── Data Plane: https://<port>-<sandboxID>.<E2B_DOMAIN>
           │  (X-Access-Token, Authorization: Basic <user>:, e2b-traffic-access-token)
           ▼
      [E2B Data Plane Proxy]    (dedicated routing path: exact port, streaming/WS)
           │
           ▼
      [Sandbox Pod (CodeInterpreter, authMode: none)]
           ├── :49983  envd (--isnotfc: ConnectRPC process/fs, /files, /init)
           ├── :49999  code-interpreter (Jupyter/FastAPI: /execute, /contexts)
           └── :<port> user services exposed via sandbox.get_host(port)
```

### Supported API Surface (Phase 1)

| Layer | Endpoints | Notes |
| :--- | :--- | :--- |
| **Sandboxes** | `POST /v2/sandboxes` (and `POST /sandboxes`), `GET /v2/sandboxes` (with `X-Next-Token` pagination) & `GET /sandboxes`, `GET /sandboxes/{id}`, `DELETE /sandboxes/{id}`, `POST /sandboxes/{id}/timeout`, `POST /v2/sandboxes/{id}/connect` (and `POST /sandboxes/{id}/connect`) | Scoped to caller tenant/API key. `connect` only extends deadline (`max(currentEndAt, now + timeout)`); `/timeout` resets deadline to `now + timeout`. `autoPause=true` returns `501`. |
| **Templates** | `GET /templates`, `GET /templates/{id}` | Read-only view over opt-in `CodeInterpreter` CRDs in authorized namespace(s). Write/build endpoints return `501`. |
| **Data Plane** | `<port>-<sandboxID>.<E2B_DOMAIN>/*` (and `E2b-Sandbox-Id` / `E2b-Sandbox-Port` headers) | Exact port forwarding for `49983`, `49999`, and allowed user ports across all HTTP methods (`GET/POST/PUT/PATCH/DELETE/OPTIONS`). |

## Design Details

### 1. Control Plane, Identity, and WorkloadManager Contract

`agentcube-e2b-gateway` never mutates Kubernetes resources or the session store directly. We extend WorkloadManager with generic session primitives:

- `POST /v1/code-interpreter`: reuse existing `TTL` (seconds); add optional `metadata`, `envVars`, `disableIdleTimeout`, and `envd` initialization options (`envdAccessToken`, `trafficAccessToken`).
- `GET /v1/code-interpreter/sessions` & `GET /v1/code-interpreter/sessions/:sessionId`: list/get active sessions scoped to caller `ownerId` and namespace (with cursor pagination for `X-Next-Token`).
- `PATCH /v1/code-interpreter/sessions/:sessionId/ttl`: update `ExpiresAt` with mode `set` (for `/timeout`) or `extend` (for `/connect`).
- `DELETE /v1/code-interpreter/sessions/:sessionId`: existing endpoint, hardened with ownership/namespace/kind checks.

**Identity & Ownership**: Because the gateway uses a shared Kubernetes ServiceAccount to call WorkloadManager, it must authenticate callers via `X-API-Key` (SHA-256 hashed in a K8s Secret, no plaintext env fallback in production) mapped to `(ownerId, namespace, permissions)`, and propagate caller identity to WorkloadManager using a signed identity token (reusing `X-AgentCube-User-Identity`). WorkloadManager enforces `ownerId` + namespace checks on `GET`, `PATCH /ttl`, and `DELETE` so tenants cannot inspect, extend, or delete another tenant's sessions.

**Session ID reuse**: Reuse AgentCube's existing 36-character UUID `SessionID` directly as `sandboxID`. With a 5-digit port prefix, `<port>-<uuid>` is 42 characters—well within the 63-character DNS label limit—eliminating dual-ID mappings and ID-collision retries. `POST /v2/sandboxes` returns `domain: <E2B_DOMAIN>` (the base domain, not `<id>.<domain>`), matching E2B SDK `get_host(port)`.

### 2. Store Contract, TTL Authority, and GC

- Apply all store changes consistently to both **Redis** (`pkg/store/store_redis.go`) and **Valkey** (`pkg/store/store_valkey.go`).
- Add Lua-backed atomic TTL updates (updating both session JSON `ExpiresAt` and `session:expiry` ZSet) and an `ownerId` active-session index.
- **Explicit idle-GC opt-out**: Because `garbage_collection.go` treats `IdleTimeout == 0` as a fallback to the 15-minute default, add an explicit `DisableIdleTimeout bool` field on `SandboxInfo` so E2B sessions are governed strictly by `ExpiresAt`.
- **Single deadline authority**: Set direct `Sandbox.Spec.Lifecycle.ShutdownTime` to `MaxSessionDuration` as a hard cluster safety cap, and treat Store `ExpiresAt` + WorkloadManager GC as the authoritative deadline for both direct Sandboxes and WarmPool Claims. This avoids patching Kubernetes objects on every `/timeout` or `/connect` call, eliminates dual-writer drift, and aligns with #430. Before deleting a Pod/Claim, GC verifies via Store that `ExpiresAt <= now` to avoid deleting a concurrently extended session.

### 3. In-Sandbox Runtime & WarmPool `envd /init`

- E2B-compatible `CodeInterpreter` templates set `spec.authMode: none` and run official `envd --isnotfc` on port `49983` (plus optional `code-interpreter` server on `49999`).
- **Readiness hook**: Because WarmPool Pods are already running before claim time, per-sandbox `envVars` and `envdAccessToken` cannot be injected via `PodSpec.Env`. Inside `WorkloadManager.createSandbox`, after Pod IP lookup and entrypoint TCP probes succeed—and before publishing the routable entry to Store—WorkloadManager calls `POST http://<podIP>:49983/init` once with `{envVars, accessToken}`. If `/init` fails or times out, `rollbackSandboxCreation` tears down the Pod/Claim cleanly.
- If `envd` or the container restarts mid-session, the sandbox is treated as unrecoverable (no silent re-init).
- **Network isolation**: WarmPool `SandboxTemplate` currently uses `NetworkPolicyManagementUnmanaged`. Deployments enabling E2B must apply Pod-level NetworkPolicy restricting ingress to port `49983` solely to WorkloadManager and the E2B Data Plane Proxy, preventing other Pods from calling `/init` before claim.

### 4. Dedicated E2B Data Plane Proxy

The native Router uses path-prefix port selection with a first-port fallback, registers only `GET`/`POST`, and injects PicoD RS256 JWTs based on sandbox kind. E2B traffic must use a **dedicated routing path** (separate listener or dedicated proxy component):

- Parse `(port, sandboxID)` from `<port>-<sandboxID>.<E2B_DOMAIN>` (requiring explicit `E2B_DOMAIN` configuration with no commercial `e2b.app` default) and verify consistency if `E2b-Sandbox-Id` / `E2b-Sandbox-Port` headers are also supplied.
- Resolve an active, unexpired session tied to its current Pod IP and forward to the exact requested port (no fallback to port 0), supporting all HTTP methods (including `DELETE` for `/contexts/{id}`).
- Block external requests to `/init` on port `49983`.
- **Header preservation**:
  - Do **not** inject PicoD JWTs.
  - On port `49983`, preserve `X-Access-Token` and `Authorization` (`Basic <user>:`).
  - On port `49999`, **also preserve `X-Access-Token`** (the `code-interpreter` server forwards it to `envd`'s `/envs` during `/execute`) while enforcing `e2b-traffic-access-token` at the proxy hop when private traffic is enabled.
  - On arbitrary user ports (`get_host(port)`), enforce `e2b-traffic-access-token` when private traffic is enabled and apply configurable port allow/deny rules.
- **Streaming**: Use `ReverseProxy.FlushInterval = -1`, support HTTP/2 (`h2c`/TLS) and WebSocket upgrades, and use `ReadHeaderTimeout` instead of a short `ReadTimeout` so long-running ConnectRPC streams and PTY sessions are not cut off.

### 5. Opt-In Template Mapping

- Only `CodeInterpreter` CRDs explicitly marked for E2B (e.g. label `e2b.agentcube.io/enabled: "true"`) in the API key's authorized namespace are exposed via `/templates`.
- Lookup resolves `templateID` deterministically by CRD name first, then by `e2b.agentcube.io/aliases` annotation (rejecting ambiguous duplicate aliases within a namespace).
- Reference manifests and docs provide an alias mapping for `code-interpreter-v1` (the default template requested by `e2b-code-interpreter`).

## Risks and Mitigations

| Risk | Impact | Mitigation |
| :--- | :--- | :--- |
| **Unprotected `envd /init` on warm-pool Pods** | Another Pod could pre-initialize a warm Pod | Enforce Pod-level NetworkPolicy restricting port `49983` to WorkloadManager and E2B Proxy; block `/init` at the proxy. |
| **Cross-tenant delete/extend via shared gateway SA** | Tenant A affects Tenant B's sandboxes | Propagate signed caller identity (`ownerId`) to WorkloadManager and enforce `ownerId` + namespace checks on every session operation. |
| **Pod/Claim leak on delete or `/init` failure** | Cluster resource exhaustion | Route all creates/deletes through WorkloadManager with rollback; never delete only Store entries. |
| **Streaming/PTY cut off by proxy or ingress** | Broken `commands.run`, `run_code`, or PTY | Disable short `ReadTimeout`, set `FlushInterval = -1`, and test HTTP/2 streaming + WebSocket through the deployed ingress. |
| **Tight coupling to per-session K8s objects** | Rework during #430 evolution | Keep E2B Gateway dependent only on WorkloadManager HTTP APIs and Store TTL authority. |

## Test Plan

- **Unit tests**: Host/header parsing & agreement, API key hashing & tenant mapping, atomic Redis & Valkey Lua TTL/owner-index operations, `DisableIdleTimeout` GC behavior, WorkloadManager `ownerId` authorization checks.
- **Integration tests**: `e2b-gateway` → mock WorkloadManager + miniredis covering `/v2/sandboxes`, `connect` vs `/timeout` deadline semantics, `X-Next-Token` pagination, cross-tenant `403`/`404` isolation, and `501` responses for unsupported features.
- **End-to-End tests (mandatory gate)**: Run unmodified official `e2b` (Python & JS) and `e2b-code-interpreter` SDKs against a Kind cluster with an `envd` + `code-interpreter` image:
  1. `Sandbox.create(template=..., env_vars=..., timeout=...)` for both cold start and WarmPool claim.
  2. `sandbox.commands.run(...)` with streaming stdout/stderr callbacks, exit codes, and background process termination.
  3. `sandbox.files.write/read/list/remove` verifying `/tmp/...` files are visible to executed code.
  4. `sandbox.run_code(...)` across multiple turns verifying persistent Jupyter kernel state, `env_vars` visibility (port `49999` → `envd /envs`), rich outputs, and context deletion (`DELETE /contexts/{id}`).
  5. `sandbox.get_host(port)` reaching an HTTP service inside the sandbox.
  6. `Sandbox.connect(...)` (extends only) and `sandbox.set_timeout(...)` (resets deadline), verifying the Pod survives past its initial TTL and beyond 15 minutes of inactivity.
  7. `sandbox.kill()` verifying both Store entry and K8s `Sandbox`/`SandboxClaim`/Pod are deleted with zero leaks.

## Alternatives

1. **Bundle E2B Control Plane + Templates CRUD into Router (PR #260 / PR #275)**: Rejected because it grants Router Kubernetes write permissions, duplicates lifecycle logic outside WorkloadManager, conflicts with #467/#430, and still requires a real `envd` runtime.
2. **Implement `envd` protocol inside PicoD**: Rejected because maintaining ConnectRPC (`process.Process/*`, `filesystem.Filesystem/*`), PTY, `WatchDir`, and Jupyter kernel parity in PicoD carries high ongoing maintenance cost when Apache-2.0 `envd` already exists.
3. **Python SDK-only Facade**: Rejected because it does not support `e2b-js-sdk` or third-party integrations that import the official `e2b` package directly.

## Implementation Plan

- **Phase 1 — Core Lifecycle & Store Primitives**: Add atomic Redis/Valkey TTL + owner-index operations, `DisableIdleTimeout`, WorkloadManager session query/TTL/ownership checks, and `envd /init` readiness hook.
- **Phase 2 — E2B Gateway & Data Plane Proxy**: Add optional `agentcube-e2b-gateway` (Platform API + API Key auth) and dedicated `<port>-<sandboxID>.<E2B_DOMAIN>` streaming proxy, plus opt-in Helm chart values (disabled by default).
- **Phase 3 — Reference Images & Official SDK E2E**: Publish reference `CodeInterpreter` manifests/Dockerfiles (`envd` + `code-interpreter`, including `code-interpreter-v1` alias) and wire official `e2b` Python/JS SDK E2E tests into CI.
