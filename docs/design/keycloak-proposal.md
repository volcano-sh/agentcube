# Keycloak Integration Design

Author: Mahil Patel

## Motivation

AgentCube currently has no mechanism to authenticate external callers, anyone who can reach the Router endpoint can invoke agent runtimes and code interpreters without proving their identity. This proposal adds external authentication and authorization using Keycloak as the identity provider, covering OIDC token validation at the Router, role-based access control (RBAC), resource-level access control (RLAC), identity forwarding to downstream services, and Python SDK auth support. Everything is gated behind `keycloak.enabled` in Helm values, so existing deployments are unaffected.

## Architecture

### Auth layers

| Layer | Purpose | Mechanism | Status |
|-------|---------|-----------|--------|
| Internal (transport) | Machine identity between components | mTLS with SPIRE/file-based certificates | Existing |
| Internal (application) | Bind Router to PicoD sessions | Router-signed JWT with session claims | Existing |
| **External (authentication)** | **Prove human/SDK caller identity** | **Keycloak OIDC JWT validation** | **This proposal** |
| **External (authorization)** | **Enforce access rules** | **RBAC (realm roles) + RLAC (owner labels)** | **This proposal** |

### End-to-end request flow

```mermaid
sequenceDiagram
    actor SDK as SDK / Client
    participant KC as Keycloak
    participant Router as Router
    participant WM as WorkloadManager
    participant PicoD as PicoD (Sandbox)

    Note over SDK, KC: 1. Token Acquisition
    SDK->>KC: POST /realms/agentcube/protocol/openid-connect/token<br/>(client_credentials: agentcube-service + client_secret)
    KC-->>SDK: Access Token (JWT)

    Note over SDK, PicoD: 2. Authenticated Invocation
    SDK->>Router: POST /v1/namespaces/.../invocations/...<br/>Authorization: Bearer <keycloak_jwt>

    Note over Router: 3. Edge Authentication
    Router->>Router: Validate JWT signature (cached JWKS)
    Router->>Router: Check expiry, issuer, audience
    Router->>Router: Extract realm_access.roles

    Note over Router: 4. RBAC Check
    Router->>Router: Require "sandbox:invoke" role

    Note over Router, WM: 5. Session Creation (if new)
    Router->>Router: Sign identity JWT (sub, iss, aud, exp)
    Router->>WM: POST /v1/agent-runtime<br/>Authorization: Bearer <k8s_sa_token><br/>X-AgentCube-User-Identity: <router_signed_jwt>
    WM->>WM: Verify identity JWT (Router public key)<br/>Record ownership (annotation + hashed label)
    WM-->>Router: Sandbox info (sessionId, endpoints, ownerId)

    Note over Router: 6. RLAC Check (if existing session)
    Router->>Router: Verify sandbox owner matches JWT sub

    Note over Router, PicoD: 7. Proxy to Sandbox
    Router->>Router: Sign internal JWT (session_id + user_sub)
    Router->>PicoD: Forward request<br/>Authorization: Bearer <internal_jwt>
    PicoD->>PicoD: Verify internal JWT (existing flow)
    PicoD-->>Router: Response
    Router-->>SDK: Response
```

### Key decisions

**JWKS-based offline validation.** The Router fetches Keycloak's public signing keys via OIDC discovery at startup. The `go-oidc` library caches and auto-rotates these keys - no per-request call to Keycloak.

**Forwarding user identity via signed tokens, not plain headers.** The Router embeds the user's `sub` claim into existing channels:
- For PicoD: added as a `user_sub` claim in the Router-signed internal JWT
- For WorkloadManager: sent as a short-lived Router-signed identity JWT in the `X-AgentCube-User-Identity` header

Downstream services trust the identity because it is cryptographically signed by the Router's private key (the same key used for PicoD auth). WM verifies this JWT using the Router's public key from the `picod-router-public-key` ConfigMap — no dependency on mTLS for identity trust.

## Detailed Design

### 1. Keycloak Helm Deployment

Keycloak runs as a single-replica Deployment inside the AgentCube namespace, gated behind `{{- if .Values.keycloak.enabled }}`. The Helm chart **bootstraps** the initial realm, clients, and roles — it is not a declarative management tool. After first startup, realm changes must be made via the Keycloak Admin API or Admin Console.

The Deployment runs `quay.io/keycloak/keycloak:26.0.8` and supports two modes:

- **Dev mode** (`keycloak.devMode: true`, default): Runs `start-dev` with an embedded H2 database. Suitable for local development and testing.
- **Production mode** (`keycloak.devMode: false`): Runs `start` and requires an external database, an external Secret for credentials, and a public hostname. For production HA, we recommend using an external Keycloak instance or the [Keycloak Operator](https://www.keycloak.org/operator/installation) rather than the built-in single-replica chart.

The Deployment uses `--import-realm` to load the realm configuration from a mounted Secret (`keycloak-realm-config`) on first startup. The import uses `IGNORE_EXISTING` strategy — subsequent restarts do not overwrite the realm, so Helm value changes to the realm JSON will not take effect on an existing database. The pod runs as non-root with all capabilities dropped.

The Service exposes Keycloak on port 8080 (configurable) as a ClusterIP service.

#### Realm Configuration

The realm JSON defines a role hierarchy where each higher role inherits the ones below:

```
admin
  └── sandbox:manage       (create/delete AgentRuntime and CodeInterpreter CRDs)
        └── sandbox:invoke  (invoke agent runtimes and code interpreters)
```

`sandbox:invoke` is assigned to the default realm role, so every new user gets it automatically.

**OAuth2 Clients:**

| Client ID | Type | Purpose |
|-----------|------|---------|
| `agentcube-service` | Confidential (`client_credentials`) | Server-side / automation authentication |
| `agentcube-sdk` | Public (`authorization_code` + PKCE) | Interactive SDK and CLI authentication |
| `agentcube-router` | Confidential (`client_credentials`) | Internal Router service identity |
| `agentcube-admin` | Confidential (`client_credentials`) | Administrative operations |

Confidential client secrets are enforced via Helm's `required` function — the chart fails to render if they're missing. The `agentcube-sdk` client is a **public client** (no secret) that uses authorization code with PKCE for interactive flows, following RFC 8252 (OAuth 2.0 for Native Apps). The `agentcube-service` client is confidential and used for server-side `client_credentials` flows where a secret can be stored securely. Both clients have `sandbox:invoke` mapped via `scopeMappings`.

Both clients include a **hardcoded audience protocol mapper** (`oidc-audience-mapper`) that injects `agentcube-api` into the access token's `aud` claim. The Router validates `aud = "agentcube-api"`. This follows OAuth2 convention — the audience identifies the resource server (the Router API), not the client that requested the token.

For production mode, the chart includes validation guards that fail the render if required values like `existingSecret`, `database.vendor`, or `proxy.hostname` are missing.

### 2. OIDC Token Validation (Router)

The Router uses the `coreos/go-oidc` library to validate incoming JWTs. This is the standard OIDC library in the Go ecosystem - Kubernetes itself uses it.

**New file: `pkg/router/oidc.go`**

```go
type OIDCConfig struct {
    IssuerURL string   // e.g. "http://keycloak.agentcube-system.svc:8080/realms/agentcube"
    Audience  string   // expected "aud" claim, e.g. "agentcube-api"
}

type Claims struct {
    Subject     string      `json:"sub"`
    Email       string      `json:"email"`
    RealmAccess RealmAccess `json:"realm_access"`
}

type RealmAccess struct {
    Roles []string `json:"roles"`
}
```

The `OIDCValidator` uses `go-oidc` for JWKS discovery and key caching (`oidc.NewProvider()`), but validates the token as an **OAuth2 access token**, not an ID token. Keycloak's `client_credentials` grant returns an access token, and while Keycloak issues these as signed JWTs using the same keys, the audience semantics differ from ID tokens. `ValidateToken` verifies the JWT signature against cached JWKS keys, then explicitly checks `iss`, `exp`, `nbf`, `aud`, and extracts `realm_access.roles` — all locally, no per-request call to Keycloak.

### 3. Authentication Middleware (Router)

**New file: `pkg/router/auth.go`**

Two gin middleware functions :

- **`oidcAuthMiddleware()`** - extracts the Bearer token, validates it via the OIDC validator, stores Claims in context. No-op when auth is disabled.
- **`requireRole(role)`** - checks `realm_access.roles` for the required role. Returns 403 if missing.

Applied to the `/v1` route group:

```go
v1 := s.engine.Group("/v1")
v1.Use(s.oidcAuthMiddleware())          // validate JWT
if s.oidcValidator != nil {
    v1.Use(requireRole("sandbox:invoke"))  // check role
}
v1.Use(s.concurrencyLimitMiddleware())  // existing
```

Health endpoints skip authentication - they must remain accessible for Kubernetes probes.

### 4. Identity Forwarding

#### Router → PicoD

The Router already signs an internal JWT for each proxied request. When external auth is enabled, the caller's `sub` claim is embedded in this JWT:

```go
claims := map[string]interface{}{
    "session_id": sandbox.SessionID,
}
if oidcClaims := extractClaims(c); oidcClaims != nil {
    claims["user_sub"] = oidcClaims.Subject
}
```

PicoD doesn't need any changes - the extra claim is simply available if it ever needs to read it.

#### Router → WorkloadManager

The `createSandbox` method in `session_manager.go` signs a short-lived identity JWT using the Router's existing private key (the same `picod-router-identity` key from the PicoD auth design) and sends it as a header:

```go
identityClaims := map[string]interface{}{
    "sub": claims.Subject,
    "iss": "agentcube-router",
    "aud": "workloadmanager",
    "exp": time.Now().Add(30 * time.Second).Unix(),
}
identityToken, _ := s.jwtManager.GenerateToken(identityClaims)
req.Header.Set("X-AgentCube-User-Identity", identityToken)
```

WM verifies this JWT using the Router's public key from the `picod-router-public-key` ConfigMap (already mounted for PicoD auth). This provides cryptographic proof of the user identity without depending on mTLS — the identity is trustworthy regardless of transport security configuration. WM continues to authenticate the Router itself via the existing K8s SA token.

### 5. RLAC - Resource-Level Access Control

RLAC ensures users can only interact with sandboxes they created.

**Ownership tagging (WorkloadManager):** When WM creates a sandbox, it verifies the `X-AgentCube-User-Identity` JWT, extracts the `sub` claim, and records ownership in two ways:

```go
Annotations: map[string]string{
    "agentcube.io/owner": userID,  // raw sub from verified identity JWT
}
Labels: map[string]string{
    "agentcube.io/owner-hash": sha256Short(userID),  // first 63 chars of hex SHA-256
}
```

The raw `sub` is stored in an annotation (no length/charset restrictions) and in Redis (`SandboxInfo.OwnerID`) for authoritative ownership checks. The hashed label is used only for Kubernetes label-based selection if needed. Keycloak UUIDs (36 chars) would fit as labels directly, but federated or pairwise subject identifiers can exceed the 63-character Kubernetes label limit, so we hash defensively.

The owner ID is returned in the create sandbox response so the Router can persist it in its Redis cache.

**Ownership verification (Router):** Before proxying to an existing sandbox, the Router checks if the caller owns it. When auth is enabled, the check is **fail-closed** — if the owner is missing (legacy sandbox, cache issue, WM bug), access is denied rather than silently allowed:

```go
if s.oidcValidator != nil {
    claims := extractClaims(c)
    if claims != nil {
        if sandbox.OwnerID == "" {
            c.JSON(http.StatusForbidden, gin.H{"error": "sandbox has no owner record"})
            return
        }
        if sandbox.OwnerID != claims.Subject {
            c.JSON(http.StatusForbidden, gin.H{"error": "you do not own this sandbox"})
            return
        }
    }
}
```

This check is skipped when external auth is disabled. Sandboxes created before auth was enabled will be inaccessible once auth is turned on, which is the intended behavior.

### 6. Python SDK Auth

The Python SDK currently supports an explicit `auth_token` string or reading a K8s service account token from a file. Neither supports `client_credentials` flow or token refresh.

We add a pluggable auth provider pattern:

**New file: `sdk-python/agentcube/auth.py`**

```python
@runtime_checkable
class AuthProvider(Protocol):
    def get_token(self) -> str: ...

class ServiceAccountAuth:
    """Authenticates using OAuth2 client_credentials grant against Keycloak."""
    def __init__(self, token_url: str, client_id: str, client_secret: str): ...
    def get_token(self) -> str:
        # Returns cached token, refreshes 30s before expiry
        ...

class TokenAuth:
    """Wraps a pre-obtained token. No refresh support."""
    def __init__(self, token: str): ...
    def get_token(self) -> str: ...
```

The existing clients (`ControlPlaneClient`, Data Plane clients, high-level clients) are updated to accept an `auth` parameter. The `auth_token` string parameter is kept for backward compatibility. Each request calls `self._auth.get_token()` to get a fresh token. `ServiceAccountAuth` is used with the `agentcube-service` client (confidential, `client_credentials`). For interactive CLI flows, a separate `DeviceCodeAuth` or browser-based flow using the public `agentcube-sdk` client can be added later.

### 7. Helm Wiring and CLI Flags

When `keycloak.enabled` is true, the Router Deployment template passes additional args:

```yaml
{{- if .Values.keycloak.enabled }}
- --enable-external-auth
- --oidc-issuer-url=http://keycloak.{{ .Release.Namespace }}.svc.cluster.local:{{ .Values.keycloak.service.port }}/realms/{{ .Values.keycloak.realm }}
- --oidc-audience=agentcube-api
{{- end }}
```

Three new flags in `cmd/router/main.go`:

| Flag | Default | Description |
|------|---------|-------------|
| `--enable-external-auth` | `false` | Enable Keycloak OIDC authentication |
| `--oidc-issuer-url` | `""` | Keycloak realm issuer URL |
| `--oidc-audience` | `"agentcube-api"` | Expected JWT audience claim |

When `--enable-external-auth` is set, `--oidc-issuer-url` is required. The Router will fail to start if it is missing.

## Testing

**Unit Tests:**
- `pkg/router/oidc_test.go` - OIDC validator tests using `httptest` as a fake JWKS server.
- `pkg/router/auth_test.go` - middleware tests: missing header, invalid token, valid token, role checks.
- WorkloadManager owner label tests - verify labels are applied during sandbox creation.
- Python SDK auth tests - `ServiceAccountAuth` token refresh, `TokenAuth` static token, backward compatibility.

**E2E Tests:**

The E2E suite (`test/e2e/`) will be extended to deploy Keycloak in the Kind cluster:

1. Preload the Keycloak image into Kind
2. Deploy with `keycloak.enabled=true` and test client secrets
3. Obtain a real token via `client_credentials` grant
4. Test cases: no token → 401, invalid token → 401, valid token → success, RLAC ownership → 403
