---
title: E2B API Compatibility Layer for AgentCube
authors:
  - "@acsoto"
reviewers:
  - TBD
approvers:
  - TBD
creation-date: 2026-11-23
last-updated: 2026-11-24
status: draft
tracking-issue: "https://github.com/volcano-sh/agentcube/issues/257"
---

# E2B API Compatibility Layer for AgentCube

## Summary

This proposal defines a minimal, layered architecture for making AgentCube compatible with the official E2B protocol and SDKs (`e2b` Python/JS SDKs and `e2b-code-interpreter`) **without adding a new runtime component** or re-implementing `envd` inside PicoD:

1. **Opt-in E2B Module inside `agentcube-router`**: Gate E2B handling behind `--e2b-domain` (disabled by default). Dispatch by HTTP `Host` at Router's ingress layer so `api.<E2B_DOMAIN>` serves the E2B Platform API, `<port>-<sandboxID>.<E2B_DOMAIN>` serves a dedicated streaming data-plane proxy, and native AgentCube paths continue through the existing Router Gin engine unchanged—reusing Router's Redis/Valkey store, TLS listener, `JWTManager` identity Secret, and WorkloadManager HTTP/2+mTLS client.
2. **WorkloadManager as Single Lifecycle Owner**: Extend WorkloadManager and the Redis/Valkey stores with generic session listing, ownership enforcement, atomic TTL updates, explicit idle-GC opt-out (`DisableIdleTimeout`), and an `envd /init` readiness hook. Router keeps **zero Kubernetes write permissions**.
3. **Reuse Open-Source `envd` + `code-interpreter` Inside Opt-In Templates**: Run Apache-2.0 `envd` (port `49983`) and compatible `code-interpreter` (port `49999`) in opt-in `CodeInterpreter` templates (`authMode: none`), keeping PicoD focused on AgentCube's native lightweight workflow.

## Motivation

E2B is a widely adopted sandbox protocol across AI frameworks (LangChain, AutoGen, Vercel AI SDK, etc.). Supporting unmodified official E2B SDKs lets users migrate to self-hosted, Kubernetes-native AgentCube by setting `E2B_API_URL`, `E2B_DOMAIN`, and `E2B_API_KEY`.

Deploying a separate stateless `e2b-gateway` service would duplicate `agentcube-router`'s core plumbing (Redis/Valkey store connection, WorkloadManager HTTP/2 + mTLS + ServiceAccount client, `picod-router-identity` JWT signing/Secret management, TLS serving, Deployment/Service/Helm packaging) only to translate REST requests. Consolidating the E2B module inside `router`—while isolating dispatch cleanly by `Host` and leaving non-E2B deployments 100% untouched—minimizes operational and code overhead while preserving the control-plane/lifecycle boundary in #467 and #430.

### Goals

- Run unmodified official `e2b` (Python/JS) and `e2b-code-interpreter` SDKs against AgentCube for sandbox lifecycle, command execution, file operations, stateful code execution, and custom port exposure (`get_host(port)`).
- Keep the existing two-component topology (`workloadmanager` + `router`) with zero E2B impact when `--e2b-domain` is unset.
- Keep Router free of Kubernetes write permissions; delegate all Kubernetes mutations and Pod `/init` exclusively to WorkloadManager.
- Strengthen shared WorkloadManager and Redis/Valkey primitives rather than building E2B-only sidecars or bypassing GC.
- Support both direct `Sandbox` creation and `SandboxWarmPool` claims, including per-sandbox `envVars` and `envdAccessToken` initialization.

### Non-Goals

- Adding a new standalone gateway microservice/container image alongside `router` and `workloadmanager`.
- Re-implementing `envd` ConnectRPC/PTY/WatchDir or a Jupyter server inside PicoD.
- Mapping `AgentRuntime` CRDs to E2B sandboxes (only opt-in `CodeInterpreter` CRDs are mapped).
- E2B Template Build APIs (`POST /v3/templates`, cloud image builds — deferred until #434 matures; return `501 Not Implemented`).
- Sandbox `pause` / `resume` / `autoPause` / snapshots and requested network isolation policies (`allowInternetAccess: false`, `network`) — return `501 Not Implemented` rather than silently accepting unenforced policies.

## Proposal

```text
Official E2B SDKs (e2b / e2b-code-interpreter)
  │
  └──▶ [agentcube-router] (TLS listener; E2B enabled iff --e2b-domain is set)
         │
         ├── Host: api.<E2B_DOMAIN>  (X-API-Key auth)
         │     └── E2B Control Plane Handler (read-only CodeInterpreter lookup)
         │           │  reused HTTP/2+mTLS + signed X-AgentCube-User-Identity
         │           ▼
         │     [WorkloadManager] (single owner of K8s lifecycle, GC, envd /init)
         │       ├──▶ Session Store (Redis & Valkey: atomic Lua TTL, owner index)
         │       └──▶ Kubernetes (Sandbox / SandboxClaim / WarmPool)
         │
         ├── Host: <port>-<sandboxID>.<E2B_DOMAIN>
         │     └── E2B Data Plane Proxy (bypasses OIDC, PicoD JWT rewrite & semaphore)
         │           │  strict port routing, blocks /init, validates 49999 X-Access-Token
         │           ▼
         │     [Sandbox Pod (CodeInterpreter, authMode: none)]
         │       ├── :49983  envd (--isnotfc --no-cgroups: ConnectRPC, /files, /init)
         │       ├── :49999  code-interpreter (Jupyter/FastAPI: /execute, /contexts)
         │       └── :<port> user services exposed via sandbox.get_host(port)
         │
         └── Other Hosts / Native Paths (/v1/namespaces/...)
               └── Existing Router Gin Engine (OIDC + PicoD JWT, completely unchanged)
```

### Supported API Surface (Phase 1)

| Layer | Endpoints | Notes |
| :--- | :--- | :--- |
| **Sandboxes** | `POST /v2/sandboxes` (and `POST /sandboxes`), `GET /v2/sandboxes` & `GET /sandboxes`, `GET /sandboxes/{id}`, `DELETE /sandboxes/{id}`, `POST /sandboxes/{id}/timeout`, `POST /v2/sandboxes/{id}/connect` (and `POST /sandboxes/{id}/connect`) | Scoped to caller API key (`ownerId` + `namespace`). `connect` only extends deadline (`max(currentEndAt, now + timeout)`); `/timeout` resets deadline to `now + timeout`. `autoPause` and network restriction options return `501`. |
| **Templates** | `GET /templates`, `GET /templates/{id}` | Read-only view over opt-in `CodeInterpreter` CRDs in caller's authorized namespace. Build/update/delete endpoints return `501`. |
| **Data Plane** | `<port>-<sandboxID>.<E2B_DOMAIN>/*` | Exact port forwarding for `49983`, `49999`, and user ports across all HTTP methods (`GET/POST/PUT/PATCH/DELETE/OPTIONS`) and WebSocket/ConnectRPC streams. |

## Design Details

### 1. Router Dispatch & Reused Infrastructure

Top-level `http.Handler` in `agentcube-router` dispatches requests cleanly when `--e2b-domain` is configured:

- **No overlap across paths**:
  - `Host == <port>-<sandboxID>.<E2B_DOMAIN>` routes directly to the E2B data-plane reverse proxy—completely bypassing native OIDC middleware, PicoD RS256 JWT injection, and Router's `concurrencyLimitMiddleware` semaphore (so long-lived PTY / HTTP streams never exhaust native invoke slots).
  - `Host == api.<E2B_DOMAIN>` routes to the E2B Platform API router with `X-API-Key` authentication—preventing Platform API headers (e.g., `E2b-Sandbox-Id` sent on `Sandbox.connect`) from ever matching data-plane proxy rules.
  - Everything else falls through untouched to Router's existing Gin engine.
- **Reused components**: E2B handlers reuse Router's existing `store.Store` (`storeClient`), `JWTManager` (`picod-router-identity`), TLS server, and `WorkloadManager` HTTP/2 + mTLS client (`SessionManager`).
- **Default-off RBAC**: When `--e2b-domain` is empty, Router does not load E2B API key Secrets or start a `CodeInterpreter` informer; when enabled, Router only needs read-only Secret access (`e2b-api-keys`) and read-only `CodeInterpreter` CRD `get/list/watch`.

### 2. Identity, Ownership, and WorkloadManager Contract

Because `router` calls WorkloadManager over its shared ServiceAccount + mTLS connection, end-tenant isolation is enforced cleanly across both hops:

- **API Key mapping**: `X-API-Key` hashes (SHA-256 hex stored in a K8s Secret) map deterministically to `(ownerId, namespace)` without keeping plaintext secrets in memory.
- **Identity propagation**: `router` signs short-lived `X-AgentCube-User-Identity` tokens using `JWTManager` (`sub = ownerId`) on every WorkloadManager call.
- **Generic WorkloadManager primitives**:
  - `POST /v1/code-interpreter`: reuses `TTL` (seconds) and adds optional `metadata`, `envVars`, `disableIdleTimeout`, and `envd` init options (`envdAccessToken`, `trafficAccessToken`).
  - `GET /v1/code-interpreter/sessions` & `GET /v1/code-interpreter/sessions/:sessionId`: require verified caller `ownerId` (`401` if missing; never fall back to global listing) and enforce owner + namespace authorization.
  - `PATCH /v1/code-interpreter/sessions/:sessionId/ttl`: validates owner/namespace, rejects non-ready sessions (`409 Conflict` while still `creating` so late create completion cannot overwrite an accepted update), caps expiry at `CreatedAt + MaxSessionDuration`, and delegates atomic `set` / `extend` updates to Store.
  - `DELETE /v1/code-interpreter/sessions/:sessionId`: enforces symmetric owner/namespace/kind authorization before deleting Kubernetes resources and Store state.
- **Session ID reuse**: Reuse AgentCube's existing 36-character UUID `SessionID` directly as `sandboxID` (`<5-digit-port>-<uuid>` is 42 characters, within DNS's 63-character label limit), avoiding dual-ID tables. `POST /v2/sandboxes` returns `domain: <E2B_DOMAIN>`, matching official `sdk.get_host(port)`.

### 3. Store Contract, TTL Authority, and GC

- Apply all store changes consistently to both **Redis** (`pkg/store/store_redis.go`) and **Valkey** (`pkg/store/store_valkey.go`).
- Add Lua-backed `UpdateSandboxExpiry` (atomically updating session JSON `ExpiresAt` and the `session:expiry` sorted set) and per-owner `owner:<ownerID>:sessions` sets for `ListActiveSandboxes`.
- **Explicit idle-GC opt-out & index isolation**: Add `DisableIdleTimeout bool` on `SandboxInfo`. Exclude `DisableIdleTimeout` sessions from the `lastActivity` sorted set and skip them in `GC` idle checks so long-lived idle E2B sessions cannot starve the bounded `ListInactiveSandboxes` scan of native AgentCube sessions.
- **Single deadline authority**: Set direct `Sandbox.Spec.Lifecycle.ShutdownTime` to `MaxSessionDuration` as a hard cluster safety cap, and use Store `ExpiresAt` + WorkloadManager GC as the sole TTL authority (both direct `Sandbox` and WarmPool `SandboxClaim`). GC re-verifies `ExpiresAt <= now` from Store before deleting Pods/Claims to avoid deleting concurrently extended sessions.

### 4. In-Sandbox Runtime & WarmPool `envd /init`

- E2B-compatible `CodeInterpreter` templates set `spec.authMode: none` and run official Apache-2.0 `envd` (`--isnotfc --no-cgroups --port 49983`) alongside `e2b-dev/code-interpreter` (`49999`). Note: inside BestEffort/Burstable Kubernetes Pods without `CAP_SYS_RESOURCE`, unprivileged `envd` child shells cannot lower `/proc/$$/oom_score_adj` from `1000`, so process spawning degrades `oom_score_adj` adjustment to best-effort rather than failing commands.
- **Readiness hook**: Inside `WorkloadManager.createSandbox`, once Pod IP lookup and TCP entrypoint readiness succeed (and before transitioning status to `ready`), WorkloadManager calls `POST http://<podIP>:<port>/init` with redirects disabled (`http.ErrUseLastResponse`) to inject per-sandbox `envVars` and `accessToken`, rolling back Pod/Claim creation if `/init` fails.

### 5. E2B Data Plane Proxy & Security Boundaries

- **Host-only routing & session boundary**: Extract `(port, sandboxID)` strictly from `<port>-<sandboxID>.<E2B_DOMAIN>`. Reject records lacking `EnvdVersion` (`404`) so native non-E2B sessions in the shared Redis/Valkey store cannot be reached via E2B host routing.
- **Port & endpoint protections**:
  - Reject external `/init` requests targeting `49983` (`403 Forbidden`).
  - Enforce `X-Access-Token` on `49999` (`code-interpreter`) using constant-time comparison (`401 Unauthorized` when missing/invalid), while preserving `X-Access-Token` and `Authorization: Basic` upstream on both `49983` and `49999` (never inject PicoD JWTs).
  - Support optional `e2b-traffic-access-token` verification and configurable blocked-port rules.
- **Streaming**: Set `ReverseProxy.FlushInterval = -1`, enable HTTP/2 (`h2c`/TLS) and WebSocket upgrades, and rely on `ReadHeaderTimeout` without short `ReadTimeout` so interactive shells and code executions do not time out.

### 6. Opt-In Template Mapping

- Only `CodeInterpreter` CRDs labeled `e2b.agentcube.io/enabled: "true"` in the caller's authorized namespace are exposed through `/templates`.
- Lookup resolves `templateID` deterministically by CRD name first, then by `e2b.agentcube.io/aliases` (rejecting duplicate aliases in the same namespace with `409 Conflict`), supporting `code-interpreter-v1`.

## Risks and Mitigations

| Risk | Impact | Mitigation |
| :--- | :--- | :--- |
| **Unprotected `envd /init` on warm-pool Pods** | Another Pod could pre-initialize a warm Pod | Restrict Pod ingress on `49983` via NetworkPolicy to WorkloadManager and Router; block `/init` at proxy and disable redirects on WM `/init` client. |
| **Shared Store leakage across E2B & native sessions** | Native session exposed without PicoD auth, or idle GC starvation | Require `EnvdVersion != ""` on E2B proxy, exclude `DisableIdleTimeout` from `lastActivity` index, and enforce `ownerId` + namespace checks in WorkloadManager. |
| **Native Router interference with E2B streams** | PTY / ConnectRPC stalled or auth headers corrupted | Bypass OIDC middleware, PicoD JWT injection, and `concurrencyLimitMiddleware` for `<port>-<sandboxID>.<E2B_DOMAIN>`. |
| **Unenforced security parameters accepted silently** | Caller expects disabled internet (`allowInternetAccess: false`) | Return `501 Not Implemented` explicitly for unsupported `autoPause`, `allowInternetAccess: false`, and `network` policies. |
| **Pod/Claim leak on delete or `/init` failure** | Cluster resource exhaustion | Keep K8s creation/deletion/rollback strictly inside WorkloadManager; never mutate K8s from Router. |

## Test Plan

- **Unit tests**: Host dispatch separation (`api.<domain>` vs `<port>-<id>.<domain>` vs native paths), API key Secret hashing, proxy protections (`/init` 403, `49999` `X-Access-Token` 401, non-E2B session 404), atomic Redis & Valkey Lua expiry/owner/idle-index behavior, WorkloadManager `ownerId` checks (`401`/`403`/`409`), and redirect-disabled `initEnvd`.
- **End-to-End tests (Kind + Docker)**: Run unmodified official `e2b` and `e2b-code-interpreter` SDKs against Router (`--e2b-domain`), WorkloadManager, Redis, and `envd` + `code-interpreter` template Pods covering:
  1. WarmPool (`code-interpreter-v1`) and cold-start (`warmPoolSize: 0`) `Sandbox.create` with `envs`.
  2. `commands.run` with streaming stdout callbacks and exit codes.
  3. `files.write/read/list/remove` and file visibility inside Python `run_code`.
  4. Multi-turn stateful `run_code`, env var access on `49999`, and `create_code_context`/`remove_code_context`.
  5. Custom port HTTP access via `sandbox.get_host(8000)`.
  6. `Sandbox.list`, `get_info`, `set_timeout`, and extend-only `Sandbox.connect`.
  7. Proxy `/init` block (`403`), cross-tenant isolation (`404`), and `kill()` K8s/Redis leak verification.

## Alternatives

1. **Separate `agentcube-e2b-gateway` Component**: Rejected during design refinement because it duplicates Redis/Valkey wiring, WorkloadManager mTLS/SA HTTP client, `JWTManager` Secret coordination (`picod-router-identity`), TLS configuration, and deployment manifests purely to host thin protocol adapters. Keeping E2B opt-in inside `agentcube-router` behind `--e2b-domain` avoids component proliferation while preserving the same clean host-level isolation.
2. **Bundle Kubernetes Writes into Router (PR #260 / PR #275)**: Rejected because Router must remain stateless without Kubernetes write permissions; WorkloadManager remains the single owner of K8s lifecycle and GC.
3. **Implement `envd` Protocol inside PicoD**: Rejected due to ongoing maintenance drift across ConnectRPC, PTY, filesystem watching, and Jupyter kernels when Apache-2.0 `envd` already exists.
4. **Python SDK-Only Wrapper**: Rejected because third-party integrations and JS/TS callers import unmodified official `e2b` packages directly.

## Implementation Plan

- **Phase 1 — Core Lifecycle & Store Primitives**: Atomic Redis/Valkey TTL + owner-index operations, `DisableIdleTimeout` idle-index exclusion, WorkloadManager session query/TTL/ownership checks, and `envd /init` readiness hook.
- **Phase 2 — Router Opt-In E2B Support**: Integrate `--e2b-domain` Host-based dispatch, Platform API handlers, API Key Secret auth, opt-in read-only `CodeInterpreter` informer, and E2B streaming data-plane proxy into `agentcube-router` (plus opt-in Helm values).
- **Phase 3 — Reference Images & Official SDK E2E**: Publish reference `envd` + `code-interpreter` template Dockerfile/manifests and wire official `e2b` + `e2b-code-interpreter` SDK E2E tests into local/CI verification.
