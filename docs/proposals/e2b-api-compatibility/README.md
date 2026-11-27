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

1. **Default-Enabled, Fail-Closed E2B Module inside `agentcube-router`**: E2B support is enabled by default (`--enable-e2b=true`, with `--enable-e2b=false` to disable completely) and fails closed until configured. Top-level HTTP `Host` dispatch activates when `--e2b-domain` is set: `api.<E2B_DOMAIN>` serves the E2B Platform API, `<port>-<sandboxID>.<E2B_DOMAIN>` serves a dedicated streaming data-plane proxy, and native AgentCube paths continue through the existing Router Gin engine (with E2B sessions rejected on native invoke paths)—reusing Router's Redis/Valkey store, TLS/Ingress entrypoint, `JWTManager` identity Secret, and WorkloadManager HTTP/2+mTLS client.
2. **WorkloadManager as Single Lifecycle Owner**: Extend WorkloadManager and the Redis/Valkey stores with generic session listing, ownership/kind enforcement, atomic TTL updates & GC claim locking, explicit idle-GC opt-out (`DisableIdleTimeout`), and an `envd /init` readiness hook. Router keeps **zero Kubernetes workload/sandbox write permissions**.
3. **Reuse Open-Source `envd` + `code-interpreter` Inside Opt-In Templates**: Run Apache-2.0 `envd` (port `49983`) and compatible `code-interpreter` (port `49999`) in opt-in `CodeInterpreter` templates (`authMode: none`), keeping PicoD focused on AgentCube's native lightweight workflow.

## Motivation

E2B is a widely adopted sandbox protocol across AI frameworks (LangChain, AutoGen, Vercel AI SDK, etc.). Supporting unmodified official E2B SDKs out of the box lets users adopt self-hosted, Kubernetes-native AgentCube as an E2B-compatible runtime simply by configuring `E2B_DOMAIN` (or `--e2b-domain`), TLS ingress, API keys, and opt-in templates.

Deploying a separate stateless `e2b-gateway` service would duplicate `agentcube-router`'s core plumbing (Redis/Valkey store connection, WorkloadManager HTTP/2 + mTLS + ServiceAccount client, `picod-router-identity` JWT signing/Secret management, TLS serving, Deployment/Service/Helm packaging) only to translate REST requests. Making E2B a built-in, default-enabled capability of `agentcube-router`—with strict `Host`-level isolation and fail-closed defaults so native non-E2B traffic sees zero impact—minimizes operational overhead while preserving the control-plane/lifecycle boundary in #467 and #430.

### Goals

- Run unmodified official `e2b` (Python/JS) and `e2b-code-interpreter` SDKs against AgentCube for sandbox lifecycle, command execution, file operations, stateful code execution, and custom port exposure (`get_host(port)`).
- Keep the existing two-component topology (`workloadmanager` + `router`) with E2B enabled by default (`--enable-e2b=true`) and strictly fail-closed when `--e2b-domain` or API keys are absent.
- Keep Router free of Kubernetes workload/CRD write permissions; delegate all sandbox mutations and Pod `/init` exclusively to WorkloadManager.
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
  │  (HTTPS via Router --enable-tls or wildcard TLS Ingress preserving Host)
  └──▶ [agentcube-router] (--enable-e2b=true by default, fail-closed)
         │
         ├── Host: api.<E2B_DOMAIN>  (X-API-Key auth)
         │     └── E2B Control Plane Handler (read-only CodeInterpreter lookup)
         │           │  reused HTTP/2+mTLS + signed X-AgentCube-User-Identity
         │           ▼
         │     [WorkloadManager] (single owner of K8s lifecycle, GC, envd /init)
         │       ├──▶ Session Store (Redis & Valkey: atomic Lua TTL/claim, owner index)
         │       └──▶ Kubernetes (Sandbox / SandboxClaim / WarmPool)
         │
         ├── Host: <port>-<sandboxID>.<E2B_DOMAIN>
         │     └── E2B Data Plane Proxy (bypasses OIDC, PicoD JWT rewrite & semaphore)
         │           │  strict port routing, requires EnvdVersion, blocks /init, validates 49999 X-Access-Token
         │           ▼
         │     [Sandbox Pod (CodeInterpreter, authMode: none)]
         │       ├── :49983  envd (--isnotfc --no-cgroups: ConnectRPC, /files, /init)
         │       ├── :49999  code-interpreter (Jupyter/FastAPI: /execute, /contexts)
         │       └── :<port> user services exposed via sandbox.get_host(port)
         │
         └── Other Hosts / Native Paths (/v1/namespaces/...)
               └── Existing Router Gin Engine (OIDC + PicoD JWT; rejects E2B sessions)
```

### Supported API Surface (Phase 1)

| Layer | Endpoints | Notes |
| :--- | :--- | :--- |
| **Sandboxes** | `POST /v2/sandboxes` (and `POST /sandboxes`), `GET /v2/sandboxes` & `GET /sandboxes`, `GET /sandboxes/{id}`, `DELETE /sandboxes/{id}`, `POST /sandboxes/{id}/timeout`, `POST /v2/sandboxes/{id}/connect` (and `POST /sandboxes/{id}/connect`) | Scoped to caller API key (`ownerId` + `namespace`). Returns `templateID`, `sandboxID`, `clientID`, `envdVersion`, `envdAccessToken`, and `domain`. Omitted `timeout` defaults to `300s`. `connect` returns `201` with sandbox payload and only extends deadline (`max(currentEndAt, now + timeout)`); `/timeout` resets deadline to `now + timeout`. `GET /v2/sandboxes` supports `limit` and `X-Next-Token` cursor pagination. `autoPause` and network restriction options return `501`. |
| **Templates** | `GET /templates`, `GET /templates/{id}` | Read-only view over opt-in `CodeInterpreter` CRDs in caller's authorized namespace. Build/update/delete endpoints return `501`. |
| **Data Plane** | `<port>-<sandboxID>.<E2B_DOMAIN>/*` | Exact port forwarding for `49983`, `49999`, and user ports across all HTTP methods (`GET/POST/PUT/PATCH/DELETE/OPTIONS`) and WebSocket/ConnectRPC streams. |

## Design Details

### 1. Router Dispatch, Default-On Safety, TLS, and RBAC

Top-level `http.Handler` in `agentcube-router` keeps E2B enabled by default (`--enable-e2b=true`, with `--enable-e2b=false` to disable completely) and enforces strict isolation boundaries:

- **Fail-closed out of the box**:
  - If `--e2b-domain` is unset, Router starts normally and intercepts no HTTP `Host` (native AgentCube traffic sees zero change).
  - If the E2B API key Secret (`e2b-api-keys`) is missing or empty, Router starts normally and rejects all E2B Platform API calls with `401 Unauthorized`.
  - E2B template lookup only exposes `CodeInterpreter` CRDs carrying `e2b.agentcube.io/enabled: "true"`, and E2B proxy routing rejects non-E2B sessions (`EnvdVersion == ""`).
- **Two-way path isolation**:
  - `Host == <port>-<sandboxID>.<E2B_DOMAIN>` routes directly to the E2B data-plane reverse proxy—completely bypassing native OIDC middleware, PicoD RS256 JWT injection, and Router's `concurrencyLimitMiddleware` semaphore (so long-lived PTY / HTTP streams never exhaust native invoke slots).
  - `Host == api.<E2B_DOMAIN>` routes to the E2B Platform API router with `X-API-Key` authentication—preventing Platform API headers (e.g., `E2b-Sandbox-Id` sent on `Sandbox.connect`) from ever matching data-plane proxy rules.
  - **Native path guard**: Native Router invocation (`/v1/namespaces/.../invocations/*`) validates that a session resolved via `x-agentcube-session-id` matches the requested namespace/name/kind and is **not** an E2B session (`EnvdVersion == ""`), preventing native paths (especially when OIDC is disabled) from bypassing E2B proxy `/init` blocks or `49999` token enforcement.
- **Reused infrastructure & TLS**:
  - E2B handlers reuse Router's existing `store.Store` (`storeClient`), `JWTManager` (`picod-router-identity`), and `WorkloadManager` HTTP/2 + mTLS client.
  - Because official E2B SDKs default to `https://api.<domain>` and `https://<port>-<id>.<domain>` while Helm Router TLS defaults off, Helm/deployment docs must specify either enabling Router `--enable-tls` with a wildcard certificate for `api.<domain>` + `*.<domain>` or terminating wildcard TLS at Ingress/Gateway while preserving the original `Host`, HTTP/2 streams, and WebSocket upgrades.
- **RBAC & safe upgrades**:
  - Preserve Router's existing release-namespace `secrets` `["get", "create"]` permission required by `JWTManager` (`picod-router-identity` bootstrap) alongside `e2b-api-keys` `get`.
  - Grant read-only `get/list/watch` on `codeinterpreters` (release-namespace `Role` by default, or `ClusterRole` when templates span caller namespaces) with zero workload/sandbox/CRD write permissions. If Router is upgraded ahead of RBAC updates and lacks `CodeInterpreter` watch permission, log a warning and fail closed on E2B endpoints rather than failing native Router startup.

### 2. Identity, Ownership, and WorkloadManager Contract

Because `router` calls WorkloadManager over its shared ServiceAccount + mTLS connection, end-tenant isolation is enforced cleanly across both hops:

- **API Key mapping**: `X-API-Key` hashes (SHA-256 hex stored in `e2b-api-keys` Secret) map deterministically to `(ownerId, namespace)` without keeping plaintext secrets in memory.
- **Identity & workload lifecycle enforcement**:
  - `router` signs short-lived `X-AgentCube-User-Identity` tokens using `JWTManager` (`sub = ownerId`) on every WorkloadManager call.
  - When WorkloadManager `--enable-auth` is active, E2B-delegated calls authenticate the Router ServiceAccount + signed user identity and execute Kubernetes operations with WorkloadManager's own Kubernetes client (instead of delegating K8s write RBAC to Router).
  - `POST /v1/code-interpreter`: applies `TTL` (with Router passing `300s` default when E2B client omits `timeout`) and optional `metadata`, `envVars`, `disableIdleTimeout`, and `envd` init options (`envdAccessToken`, `trafficAccessToken`). Requires a verified `ownerId` (`401` if missing) whenever `InitEnvd` / E2B options are requested.
  - `GET /v1/code-interpreter/sessions` & `GET /v1/code-interpreter/sessions/:sessionId`: require verified caller `ownerId` (`401` if missing; never fall back to global listing) and enforce owner + namespace authorization.
  - `PATCH /v1/code-interpreter/sessions/:sessionId/ttl`: validates owner/namespace, rejects non-ready sessions (`409 Conflict` while still `creating`, and `404`/`409` if claimed for deletion), caps expiry at `CreatedAt + MaxSessionDuration`, and delegates atomic `set` / `extend` updates to Store.
  - `DELETE /v1/code-interpreter/sessions/:sessionId`: enforces symmetric owner/namespace and resource-kind verification across both `/v1/agent-runtime` and `/v1/code-interpreter` delete routes before deleting Kubernetes resources and Store state.
- **Session ID reuse**: Reuse AgentCube's existing 36-character UUID `SessionID` directly as `sandboxID` (`<5-digit-port>-<uuid>` is 42 characters, within DNS's 63-character label limit). `POST /v2/sandboxes` returns `domain: <E2B_DOMAIN>`, matching `sdk.get_host(port)`.

### 3. Store Contract, TTL Authority, and GC

- Apply all store changes consistently to both **Redis** (`pkg/store/store_redis.go`) and **Valkey** (`pkg/store/store_valkey.go`).
- Store the requested `ExpiresAt` (written to `session:expiry`) and `TemplateName` separately from Kubernetes `ShutdownTime` and GC cleanup target `(Kind, Name)`.
- Add Lua-backed `UpdateSandboxExpiry` (atomically updating session JSON `ExpiresAt` and `session:expiry`) and per-owner `owner:<ownerID>:sessions` sets for `ListActiveSandboxes`.
- **Explicit idle-GC opt-out & index isolation**: Add `DisableIdleTimeout bool` on `SandboxInfo`. Exclude `DisableIdleTimeout` sessions from the `lastActivity` sorted set and skip them in `GC` idle checks so long-lived idle E2B sessions cannot starve the bounded `ListInactiveSandboxes` scan of native AgentCube sessions.
- **Single deadline authority & atomic GC claim**: Set direct `Sandbox.Spec.Lifecycle.ShutdownTime` to `MaxSessionDuration` as a hard cluster safety cap, and use Store `ExpiresAt` + WorkloadManager GC as the sole TTL authority (both direct `Sandbox` and WarmPool `SandboxClaim`). To eliminate the race between GC deletion and concurrent `/connect` or `/timeout` calls, GC atomically claims expired sessions (`ExpiresAt <= now`, marking `deleting`) in Store before deleting Pods/Claims, while TTL updates and proxy forwarding reject `"deleting"` or expired records.

### 4. In-Sandbox Runtime & WarmPool `envd /init`

- E2B-compatible `CodeInterpreter` templates set `spec.authMode: none` and run official Apache-2.0 `envd` (`--isnotfc --no-cgroups --port 49983`) alongside `e2b-dev/code-interpreter` (`49999`). Note: inside BestEffort/Burstable Kubernetes Pods without `CAP_SYS_RESOURCE`, unprivileged `envd` child shells cannot lower `/proc/$$/oom_score_adj` from `1000`, so process spawning degrades `oom_score_adj` adjustment to best-effort rather than failing commands.
- **Readiness hook**: Inside `WorkloadManager.createSandbox`, once Pod IP lookup and TCP entrypoint readiness succeed (and before transitioning status to `ready`), WorkloadManager calls `POST http://<podIP>:<port>/init` with redirects disabled (`http.ErrUseLastResponse`) to inject per-sandbox `envVars` and `accessToken`, rolling back Pod/Claim creation if `/init` fails.
- **Network isolation prerequisite**: E2B-enabled `CodeInterpreter` templates must pair with Pod-level NetworkPolicy restricting ingress on **both `49983` and `49999`**: port `49983` allows only WorkloadManager (for `/init`) and Router, and port `49999` allows only Router (preventing unauthenticated peer pods from bypassing Router's `X-Access-Token` validation).

### 5. E2B Data Plane Proxy & Security Boundaries

- **Host-only routing & session boundary**: Extract `(port, sandboxID)` strictly from `<port>-<sandboxID>.<E2B_DOMAIN>`. Require `Status == "ready"`, unexpired `ExpiresAt`, and `EnvdVersion != ""` (`404` otherwise) so native non-E2B or deleting sessions cannot be reached.
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
| **Default-on E2B exposed before cluster configuration** | Accidental exposure or startup failure on non-E2B clusters | Fail closed by default (`--e2b-domain` required for Host dispatch, missing Secret returns `401`, opt-in template label required, graceful informer RBAC fallback, `--enable-e2b=false` opt-out). |
| **Direct Pod access to `49983 /init` or unauthenticated `49999`** | Peer Pod pre-initializes warm Pod or runs code without token | Require Pod-level NetworkPolicy restricting ingress on `49983` (WM + Router) and `49999` (Router only); block `/init` at proxy and disable redirects on WM `/init`. |
| **Native vs E2B cross-path leakage & GC race** | Native path bypasses E2B auth, GC starves, or deletes extended Pod | Reject E2B sessions on native invoke paths; require `EnvdVersion` on E2B proxy; exclude `DisableIdleTimeout` from `lastActivity` index; use atomic GC claim before K8s delete. |
| **Native Router interference with E2B streams** | PTY / ConnectRPC stalled or auth headers corrupted | Bypass OIDC middleware, PicoD JWT injection, and `concurrencyLimitMiddleware` for `<port>-<sandboxID>.<E2B_DOMAIN>`. |
| **Unenforced security parameters accepted silently** | Caller expects disabled internet (`allowInternetAccess: false`) | Return `501 Not Implemented` explicitly for unsupported `autoPause`, `allowInternetAccess: false`, and `network` policies. |
| **Pod/Claim leak on delete or `/init` failure** | Cluster resource exhaustion | Keep K8s creation/deletion/rollback strictly inside WorkloadManager; never mutate K8s from Router. |

## Test Plan

- **Unit tests**: Host dispatch separation (`api.<domain>` vs `<port>-<id>.<domain>` vs native paths), native rejection of E2B sessions, API key Secret hashing, proxy protections (`/init` 403, `49999` `X-Access-Token` 401, non-E2B/deleting/expired session 404), atomic Redis & Valkey Lua expiry/claim/owner/idle-index behavior, WorkloadManager `ownerId`/kind/namespace checks (`401`/`403`/`409`), and redirect-disabled `initEnvd`.
- **End-to-End tests (Kind + Docker)**: Run unmodified official `e2b` and `e2b-code-interpreter` SDKs against Router (`--e2b-domain` + TLS), WorkloadManager, Redis, and `envd` + `code-interpreter` template Pods covering:
  1. WarmPool (`code-interpreter-v1`) and cold-start (`warmPoolSize: 0`) `Sandbox.create` with omitted timeout (`300s` default) and `envs`.
  2. `commands.run` with streaming stdout callbacks and exit codes.
  3. `files.write/read/list/remove` and file visibility inside Python `run_code`.
  4. Multi-turn stateful `run_code`, env var access on `49999`, and `create_code_context`/`remove_code_context`.
  5. Custom port HTTP access via `sandbox.get_host(8000)`.
  6. `Sandbox.list`, `get_info`, `set_timeout`, and extend-only `Sandbox.connect`.
  7. Proxy `/init` block (`403`), cross-tenant isolation (`404`), and `kill()` K8s/Redis leak verification.

## Alternatives

1. **Separate `agentcube-e2b-gateway` Component**: Rejected during design refinement because it duplicates Redis/Valkey wiring, WorkloadManager mTLS/SA HTTP client, `JWTManager` Secret coordination (`picod-router-identity`), TLS configuration, and deployment manifests purely to host thin protocol adapters. Keeping E2B built into `agentcube-router` (`--enable-e2b=true` by default, fail-closed) aligns with the project's evolution toward native E2B support while preserving host-level isolation.
2. **Bundle Kubernetes Writes into Router (PR #260 / PR #275)**: Rejected because Router must remain free of Kubernetes workload/CRD write permissions; WorkloadManager remains the single owner of K8s lifecycle and GC.
3. **Implement `envd` Protocol inside PicoD**: Rejected due to ongoing maintenance drift across ConnectRPC, PTY, filesystem watching, and Jupyter kernels when Apache-2.0 `envd` already exists.
4. **Python SDK-Only Wrapper**: Rejected because third-party integrations and JS/TS callers import unmodified official `e2b` packages directly.

## Implementation Plan

- **Phase 1 — Core Lifecycle & Store Primitives**: Atomic Redis/Valkey TTL + GC claim + owner-index operations, `DisableIdleTimeout` idle-index exclusion, WorkloadManager session query/TTL/ownership/kind checks (`--enable-auth` Router SA support), and `envd /init` readiness hook.
- **Phase 2 — Built-in Router E2B Support**: Integrate `--enable-e2b` (default `true`, fail-closed) / `--e2b-domain` Host-based dispatch, native E2B-session rejection, Platform API handlers, API Key Secret auth, read-only `CodeInterpreter` informer with graceful RBAC fallback, and E2B streaming data-plane proxy into `agentcube-router` alongside default Helm RBAC & TLS ingress docs.
- **Phase 3 — Reference Images & Official SDK E2E**: Publish reference `envd` + `code-interpreter` template Dockerfile/manifests (with NetworkPolicy covering `49983` and `49999`) and wire official `e2b` + `e2b-code-interpreter` SDK E2E tests into local/CI verification.
