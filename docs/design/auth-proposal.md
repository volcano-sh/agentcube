---
title: AgentCube Authentication and Authorization Design
authors:
  - "@mahil2040"
creation-date: 2025-03-07
---

# AgentCube Authentication and Authorization Design

Author: Mahil Patel

## Motivation

AgentCube currently has partial, ad-hoc authentication between its internal components but lacks a unified security model. The existing mechanisms are:

1. **Workload Manager Auth** (`pkg/workloadmanager/auth.go`): Optional Kubernetes TokenReview-based ServiceAccount token validation, gated behind `config.EnableAuth`, plus per-sandbox ownership checks using the extracted user identity (effectively relying on Kubernetes RBAC when using the user-scoped client).
2. **Router → PicoD Auth** (`PicoD-Plain-Authentication-Design`): A custom RSA key-pair scheme where the Router signs JWTs and PicoD verifies them using a public key exposed via the `PICOD_AUTH_PUBLIC_KEY` environment variable. The key pair (`private.pem`, `public.pem`) is stored in the `picod-router-identity` Secret, and the WorkloadManager reads this Secret to inject the public key into PicoD pods. This works for the Router→PicoD channel but leaves other internal channels unauthenticated.
3. **Router → WorkloadManager**: Optional, one-sided authentication. `pkg/router/session_manager.go` can attach a `Authorization: Bearer <serviceaccount token>` header, and WorkloadManager can validate it when `--enable-auth` is enabled. This is not mutual workload identity or a zero-trust model, and when auth is disabled any pod on the cluster network can call the WorkloadManager API.
4. **External Clients → Router**: No authentication. The `handleInvoke` handler in `pkg/router/handlers.go` processes incoming requests without verifying the caller's identity.

These gaps amount to three distinct problems:

- **Internal (machine-to-machine):** Components trust each other implicitly based on network reachability. A compromised or rogue pod on the same network can impersonate any component.
- **External (user-to-platform):** Anyone who can reach the Router endpoint can invoke sandboxes, with no identity verification and no audit trail.
- **Authorization:** Even where authentication exists, there is no mechanism to control what an authenticated identity is allowed to do. There are no roles, no permission checks, and no namespace-scoped access control.

This proposal addresses all three problems using CNCF industry-standard tooling.

### Goals

- Establish zero-trust, mutually authenticated communication between all AgentCube internal components (Router, WorkloadManager, PicoD) using X.509 mTLS.
- Provide external client/SDK authentication at the Router level via an industry-standard identity provider.
- Implement role-based access control (RBAC) for external users using Keycloak's built-in authorization capabilities.
- Keep all new auth features opt-in behind configuration flags so existing deployments are unaffected.
- Minimize per-request latency overhead from authentication and authorization.
- Supersede the existing PicoD-Plain-Authentication key distribution mechanism with automated certificate lifecycle management.

## Use Cases

1. **Zero-trust internal communication**
   A platform team deploys AgentCube into a shared Kubernetes cluster. They need assurance that only legitimate Router pods can call the WorkloadManager, and only legitimate Router/WorkloadManager pods can communicate with PicoD sandboxes - even if other workloads share the same cluster network.

2. **Authenticated SDK access**
   A development team uses the AgentCube Python SDK to run code interpreters. The Router should verify the developer's identity before creating or routing to sandboxes, and reject unauthenticated or unauthorized requests.

3. **Role-based sandbox access control**
   A platform administrator needs to restrict which users can invoke sandboxes versus which users can create or delete AgentRuntime and CodeInterpreter resources. A developer with the `sandbox:invoke` role should be able to run code but not modify runtime definitions.

4. **Enterprise identity integration**
   An organization already uses AWS IAM / Google Cloud Identity / Azure AD to manage developer identities. They want their developers to authenticate with AgentCube using their existing cloud credentials, without creating a separate set of accounts.

---

## Design Details

The design is structured in four layers, ordered by priority:

| Priority | Layer | Problem | Solution |
|---|---|---|---|
| P1 (Urgent) | Internal workload identity | Machine-to-machine trust between Router, WorkloadManager, PicoD | X.509 mTLS (SPIRE recommended, file-based certs also supported) |
| P2 | External user authentication | Client/SDK identity verification at the Router | Keycloak (OIDC/OAuth2) |
| P3 | Authorization | Role-based access control for external users | Keycloak realm roles (JWT claim checking) |
| P4 (Stretch) | Cloud provider federation | Enterprise SSO via cloud IAM | Keycloak identity brokering |

---

## 1. Internal Workload Authentication (X.509 mTLS)

Internal communication between AgentCube components is secured using mutual TLS (mTLS) with X.509 certificates. The mTLS enforcement layer is **certificate-source agnostic** — it works with any valid X.509 cert/key/CA bundle, regardless of how the certificates are provisioned. Two certificate source modes are supported:

| Mode | Certificate Source | Rotation | Best For |
|---|---|---|---|
| **SPIRE** (recommended) | SPIRE Workload API issues short-lived SVIDs automatically | Automatic (default: 1 hour TTL) | Production deployments needing zero-touch certificate management |
| **File-based** | Certs loaded from disk (provisioned by cert-manager, self-signed CA, Let's Encrypt, corporate PKI, etc.) | Manual or delegated to the provisioning tool | Environments where SPIRE is not available, or operators prefer existing PKI infrastructure |

Configuration flags for each component:

```
--tls-cert-source=spire|file    (default: spire)
--tls-cert-file=<path>          (for file mode)
--tls-key-file=<path>           (for file mode)
--tls-ca-file=<path>            (for file mode)
```

When `--tls-cert-source=file` is used, the cert/key/CA files can be populated by any mechanism - for example, a Kubernetes Secret mounted as a volume (managed by cert-manager), or static files for development. The mTLS enforcement (requiring client certs, verifying peer identity) is identical in both modes.

### 1.1 SPIRE Background

[SPIFFE](https://spiffe.io/) (Secure Production Identity Framework for Everyone) is a CNCF graduated project that provides a standard for service identity. It defines:

- **SPIFFE ID:** A URI-formatted identity, e.g., `spiffe://agentcube.local/ns/agentcube-system/sa/agentcube-router`
- **SVID (SPIFFE Verifiable Identity Document):** An X.509 certificate or JWT that proves a workload holds a given SPIFFE ID.

[SPIRE](https://spiffe.io/docs/latest/spire-about/spire-concepts/) is the production implementation of SPIFFE. It has two components:

- **SPIRE Server:** Central signing authority. Manages registration entries (which selectors map to which SPIFFE IDs) and issues SVIDs to agents.
- **SPIRE Agent:** Runs on every node (DaemonSet). Performs workload attestation, verifying process identity by querying the kernel and kubelet, and delivers SVIDs to local workloads via a Unix domain socket (the Workload API).

SPIRE handles the entire certificate lifecycle (issuance, rotation, revocation) automatically. Workloads receive certificates through the Workload API and they are rotated before they expire.

### 1.2 Why Single-Cluster SPIRE

This design uses a single-cluster SPIRE deployment with one SPIRE Server and a set of SPIRE Agents within a single Kubernetes cluster. Multi-cluster SPIRE federation is not included for the following reasons:

- **AgentCube's deployment model is single-cluster.** All internal components (Router, WorkloadManager, PicoD sandboxes) run within the same Kubernetes cluster. There is no cross-cluster RPC to secure today.
- **Federation introduces significant complexity.** Multi-cluster SPIRE requires configuring separate trust domains per cluster, setting up bundle exchange between SPIRE Servers, and managing cross-cluster network connectivity. This overhead is not justified when all workloads share a single trust boundary.
- **Incremental adoption is safer.** Establishing a solid single-cluster identity foundation first allows the team to gain operational experience with SPIRE before taking on the complexity of federation. If AgentCube later evolves to support distributed routing across clusters, SPIRE federation can be layered in without rewriting the core mTLS integration.

### 1.3 Trust Domain and Identity Assignment

All AgentCube components operate under a single trust domain:

```
spiffe://agentcube.local
```

Each component receives a unique SPIFFE ID following the Istio-convention format `spiffe://<trust-domain>/ns/<namespace>/sa/<service-account>`, which encodes the workload's Kubernetes namespace and service account directly in the URI path.

SPIRE uses **selectors** to verify workload identity during attestation. When a workload requests its certificate, the SPIRE Agent inspects the workload's Kubernetes metadata (namespace, service account, labels) and matches it against registered selectors. A workload only receives its SVID if all registered selectors match.

| Component | SPIFFE ID | Selectors |
|---|---|---|
| Router | `spiffe://agentcube.local/ns/agentcube-system/sa/agentcube-router` | `k8s:ns:agentcube-system`, `k8s:sa:agentcube-router` |
| WorkloadManager | `spiffe://agentcube.local/ns/agentcube-system/sa/agentcube-workload-manager` | `k8s:ns:agentcube-system`, `k8s:sa:agentcube-workload-manager` |
| PicoD (Sandboxes) | `spiffe://agentcube.local/sa/agentcube-sandbox` | `k8s:pod-label:app:picod`, `k8s:sa:agentcube-sandbox` |

> **Note:** PicoD cannot use a namespace selector because sandbox pods are created in the namespace specified by the user's request, not in a fixed namespace. To prevent identity spoofing in multi-tenant clusters, PicoD registration requires both the `app:picod` pod label and a dedicated `agentcube-sandbox` ServiceAccount. WorkloadManager creates this ServiceAccount in the target namespace as part of sandbox provisioning. Using a specific ServiceAccount requires RBAC permission, preventing arbitrary workloads from obtaining the PicoD SPIFFE ID.
>
> **Security Hardening:** For multi-tenant clusters, operators are encouraged to deploy a `ValidatingAdmissionPolicy` (or equivalent policy engine rule) that restricts the `app: picod` label to pods created by the WorkloadManager ServiceAccount. This provides defense-in-depth beyond the ServiceAccount selector.

### 1.4 SPIRE Deployment

#### SPIRE Server

Deployed as a `StatefulSet` in `agentcube-system`. Key configuration:

```hcl
server {
    bind_address = "0.0.0.0"
    bind_port    = "8081"
    trust_domain = "agentcube.local"
    data_dir     = "/run/spire/data"

    ca_ttl                = "24h"
    default_x509_svid_ttl = "1h"
}

plugins {
    DataStore "sql" {
        plugin_data {
            database_type     = "sqlite3"
            connection_string = "/run/spire/data/datastore.sqlite3"
        }
    }

    NodeAttestor "k8s_psat" {
        plugin_data {
            clusters = {
                "agentcube-cluster" = {
                    service_account_allow_list = [
                        "agentcube-system:spire-agent"
                    ]
                }
            }
        }
    }

    KeyManager "disk" {
        plugin_data {
            keys_path = "/run/spire/data/keys.json"
        }
    }
}
```

- **Node Attestor `k8s_psat`:** Uses Kubernetes Projected Service Account Tokens to verify agent node identity. This is the recommended attestor for Kubernetes-native deployments.
- **SVID TTL of 1 hour:** Short-lived certificates limit the blast radius of a compromise. SPIRE renews them transparently.
- **SQLite datastore:** Sufficient for single-server deployment. Can be swapped for PostgreSQL in HA setups.

#### SPIRE Agent

Deployed as a `DaemonSet` - one agent per node. Each agent:

1. Attests to the SPIRE Server using its node's Projected Service Account Token
2. Exposes the Workload API at `/run/spire/sockets/agent.sock`
3. Uses the `k8s` workload attestor to verify workload identity by querying the local kubelet for pod metadata

```yaml
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: spire-agent
  namespace: agentcube-system
spec:
  selector:
    matchLabels:
      app: spire-agent
  template:
    metadata:
      labels:
        app: spire-agent
    spec:
      serviceAccountName: spire-agent
      containers:
        - name: spire-agent
          image: ghcr.io/spiffe/spire-agent:1.12.0
          args: ["-config", "/run/spire/config/agent.conf"]
          volumeMounts:
            - name: spire-agent-socket
              mountPath: /run/spire/sockets
            - name: spire-config
              mountPath: /run/spire/config
      volumes:
        - name: spire-agent-socket
          hostPath:
            path: /run/spire/sockets
            type: DirectoryOrCreate
        - name: spire-config
          configMap:
            name: spire-agent-config
```

#### SPIRE Controller Manager

The [SPIRE Controller Manager](https://github.com/spiffe/spire-controller-manager) is deployed as a `Deployment` alongside the SPIRE Server. It watches for `ClusterSPIFFEID` custom resources and automatically syncs registration entries to the SPIRE Server. This makes workload registration fully declarative and is the recommended approach for production deployments.

### 1.5 Workload Registration

Registration entries tell the SPIRE Server which workloads should receive which SPIFFE IDs. There are two approaches depending on the environment:

#### Production: ClusterSPIFFEID CRDs (Recommended)

With the SPIRE Controller Manager deployed, registration entries are defined as Kubernetes CRDs. These are managed declaratively alongside other AgentCube manifests and are automatically synced to the SPIRE Server:

```yaml
# Router registration
apiVersion: spire.spiffe.io/v1alpha1
kind: ClusterSPIFFEID
metadata:
  name: agentcube-router
spec:
  spiffeIDTemplate: "spiffe://agentcube.local/ns/{{ .PodMeta.Namespace }}/sa/{{ .PodSpec.ServiceAccountName }}"
  podSelector:
    matchLabels:
      app: agentcube-router
  namespaceSelector:
    matchNames:
      - agentcube-system
---
# WorkloadManager registration
apiVersion: spire.spiffe.io/v1alpha1
kind: ClusterSPIFFEID
metadata:
  name: agentcube-workload-manager
spec:
  spiffeIDTemplate: "spiffe://agentcube.local/ns/{{ .PodMeta.Namespace }}/sa/{{ .PodSpec.ServiceAccountName }}"
  podSelector:
    matchLabels:
      app: agentcube-workload-manager
  namespaceSelector:
    matchNames:
      - agentcube-system
---
# PicoD registration (namespace-agnostic)
apiVersion: spire.spiffe.io/v1alpha1
kind: ClusterSPIFFEID
metadata:
  name: agentcube-sandbox
spec:
  spiffeIDTemplate: "spiffe://agentcube.local/sa/{{ .PodSpec.ServiceAccountName }}"
  podSelector:
    matchLabels:
      app: picod
```

#### Development: Manual CLI

For local development (e.g., `kind` clusters), registration entries can be created manually by exec-ing into the SPIRE Server pod:

```bash
kubectl exec -n agentcube-system <spire-server-pod> -- \
  spire-server entry create \
    -spiffeID spiffe://agentcube.local/ns/agentcube-system/sa/agentcube-router \
    -parentID spiffe://agentcube.local/spire-agent \
    -selector k8s:ns:agentcube-system \
    -selector k8s:sa:agentcube-router

kubectl exec -n agentcube-system <spire-server-pod> -- \
  spire-server entry create \
    -spiffeID spiffe://agentcube.local/ns/agentcube-system/sa/agentcube-workload-manager \
    -parentID spiffe://agentcube.local/spire-agent \
    -selector k8s:ns:agentcube-system \
    -selector k8s:sa:agentcube-workload-manager

kubectl exec -n agentcube-system <spire-server-pod> -- \
  spire-server entry create \
    -spiffeID spiffe://agentcube.local/sa/agentcube-sandbox \
    -parentID spiffe://agentcube.local/spire-agent \
    -selector k8s:pod-label:app:picod \
    -selector k8s:sa:agentcube-sandbox
```

> **Note:** Manual CLI entries are stored in the SPIRE Server's datastore (SQLite by default). They survive restarts if the datastore is backed by persistent storage, but are harder to manage and audit compared to the declarative CRD approach.

#### Attestation Flow (Example: Router)

1. Router process connects to the local SPIRE Agent via `/run/spire/sockets/agent.sock`
2. Agent identifies the calling process via PID and queries the kubelet for pod metadata (namespace, service account, labels)
3. Agent matches the discovered selectors against registration entries fetched from the Server
4. Match found → Agent issues an X.509 SVID with `spiffe://agentcube.local/ns/agentcube-system/sa/agentcube-router` as URI SAN
5. Router receives a TLS certificate, private key, and trust bundle - ready to serve and initiate mTLS

### 1.6 mTLS Integration

Each component needs two changes: configure its server to require client certificates, and configure its clients to present its own certificate while verifying the server's. The mTLS implementation uses Go's standard `crypto/tls` package — no external libraries are required.

Regardless of the certificate source (SPIRE or file-based), the cert/key/CA files end up on disk and the Go code is identical. When using SPIRE, the [SPIFFE Helper](https://github.com/spiffe/spiffe-helper) sidecar writes SVIDs to disk as PEM files, making them consumable by standard TLS configuration.

```go
// Load certificate and key (from any source: SPIRE via spiffe-helper, cert-manager, self-signed, etc.)
cert, err := tls.LoadX509KeyPair(certFile, keyFile)

// Load CA bundle for peer verification
caCert, err := os.ReadFile(caFile)
caPool := x509.NewCertPool()
caPool.AppendCertsFromPEM(caCert)
```

#### Server-Side (WorkloadManager, PicoD)

WorkloadManager and PicoD require incoming connections to present a valid client certificate signed by the trusted CA:

```go
serverTLSConfig := &tls.Config{
    Certificates: []tls.Certificate{cert},
    ClientCAs:    caPool,
    ClientAuth:   tls.RequireAndVerifyClientCert,
}

server := &http.Server{
    TLSConfig: serverTLSConfig,
}
```

#### Client-Side (Router)

The Router presents its own certificate when connecting to WorkloadManager and PicoD, and verifies the server's certificate against the trusted CA:

```go
clientTLSConfig := &tls.Config{
    Certificates: []tls.Certificate{cert},
    RootCAs:      caPool,
}

httpClient := &http.Client{
    Transport: &http.Transport{
        TLSClientConfig: clientTLSConfig,
    },
}
```

The cert/key/CA files can be provisioned by any mechanism:
- **SPIRE:** The [SPIFFE Helper](https://github.com/spiffe/spiffe-helper) sidecar fetches SVIDs from the Workload API and writes them to disk as PEM files. Certificates are rotated automatically.
- **cert-manager:** Automatically issues and rotates certificates, stored as Kubernetes Secrets and mounted into pods.
- **Self-signed CA:** Generated manually or via a script for development and testing environments.
- **Let's Encrypt / corporate PKI:** Externally issued certificates placed on disk or in Secrets.

*(Note: WorkloadManager manages PicoD pods via the Kubernetes API, so it does not make direct HTTP requests to PicoD and does not need a PicoD client mTLS configuration.)*

This replaces the existing `PICOD_AUTH_PUBLIC_KEY` environment variable mechanism as the new default. The legacy mechanism is retained behind a flag during transition (see Section 1.9).

### 1.7 Communication Channel Summary

**Before:**

```
SDK → Router:              Plain HTTP, no auth
Router → WorkloadManager:  Plain HTTP/gRPC, no auth
Router → PicoD:            HTTP + custom JWT (PicoD-Plain-Auth)
WorkloadManager → PicoD:   (No direct HTTP calls, managed via K8s API)
```

**After:**

```
SDK → Router:              HTTPS + Keycloak JWT (see Section 2)
Router → WorkloadManager:  mTLS (X.509 certs via SPIRE or file-based)
Router → PicoD:            mTLS (X.509 certs via SPIRE or file-based)
WorkloadManager → PicoD:   (No direct HTTP calls, managed via K8s API)
```

### 1.8 Architecture Overview

```mermaid
graph LR
    subgraph External
        SDK[SDK Client]
    end

    subgraph "agentcube.local cluster"
        subgraph "SPIRE Infrastructure"
            SS[SPIRE Server]
            SA1[SPIRE Agent - Node 1]
            SA2[SPIRE Agent - Node 2]
        end

        subgraph "AgentCube Components"
            R["Router<br/>spiffe://agentcube.local/ns/agentcube-system/sa/agentcube-router"]
            WM["WorkloadManager<br/>spiffe://agentcube.local/ns/agentcube-system/sa/agentcube-workload-manager"]
            P1["PicoD Sandbox 1<br/>spiffe://agentcube.local/sa/agentcube-sandbox"]
            P2["PicoD Sandbox 2<br/>spiffe://agentcube.local/sa/agentcube-sandbox"]
        end
    end

    SS -.->|issues SVIDs| SA1
    SS -.->|issues SVIDs| SA2
    SA1 -.->|Workload API| R
    SA1 -.->|Workload API| WM
    SA2 -.->|Workload API| P1
    SA2 -.->|Workload API| P2

    SDK -->|"HTTPS + JWT"| R
    R <-->|mTLS| WM
    R <-->|mTLS| P1
    R <-->|mTLS| P2
```

> **Note:** The architecture diagram above shows the SPIRE-based deployment. When using file-based certificates (`--tls-cert-source=file`), the SPIRE Infrastructure components are not deployed; certificates are provisioned externally and mounted into pods. The mTLS connections between AgentCube components remain the same.

### 1.9 Impact on Existing PicoD-Plain-Authentication

The X.509 mTLS approach supersedes the PicoD-Plain-Authentication design:

| Current (PicoD-Plain-Auth) | New (X.509 mTLS) |
|---|---|
| Router generates RSA key pair, stores both keys in `picod-router-identity` Secret | SPIRE issues short-lived X.509 SVIDs automatically, or certs are loaded from disk (cert-manager, self-signed, etc.) |
| Public key read from `picod-router-identity` Secret by WorkloadManager | Trust bundle delivered through Workload API socket (SPIRE) or CA file on disk (file-based) |
| Bootstrap phase with optimistic locking race between Router replicas | No bootstrap race - each replica independently fetches its SVID (SPIRE) or loads certs from disk (file-based) |
| `PICOD_AUTH_PUBLIC_KEY` env var injected into PicoD pods | Workload API socket or cert files mounted into PicoD pods |
| Manual key rotation (delete Secret, restart Routers) | Automatic rotation by SPIRE (default: 1 hour TTL) or delegated to external tool (cert-manager) |
| Application-layer JWT verification | Transport-layer mTLS verification |

The existing PicoD-Plain-Auth code path will be kept behind a `--legacy-picod-auth` flag during the transition period and marked as deprecated.

---

## 2. External User Authentication (Keycloak)

### 2.1 Overview

SPIRE solves internal workload identity but does not address external user authentication. When a developer uses the Python SDK to invoke an AgentRuntime, the Router needs to verify the developer's identity.

[Keycloak](https://www.keycloak.org/) is an open-source IAM solution that provides OIDC/OAuth2 token issuance, user management, and built-in federation for external identity providers. Instead of building a custom JWT issuer and API key store, Keycloak handles user identity as a dedicated service.

### 2.2 Workflow

```mermaid
sequenceDiagram
    participant SDK as AgentCube SDK
    participant KC as Keycloak
    participant Router as Router
    participant WM as WorkloadManager
    participant Sandbox as PicoD

    Note over SDK, KC: Authentication (client_credentials grant)
    SDK->>KC: POST /realms/agentcube/protocol/openid-connect/token<br/>grant_type=client_credentials<br/>client_id=agentcube-sdk&client_secret=<secret>
    KC->>KC: Validate client_id and client_secret<br/>against registered client in agentcube realm
    KC-->>SDK: Access Token (JWT with sub, roles, exp)

    Note over Router, KC: JWKS Cache (periodic, e.g. every 5 min)
    Router->>KC: GET /realms/agentcube/protocol/openid-connect/certs
    KC-->>Router: JWKS (public signing keys)
    Router->>Router: Cache JWKS keys locally

    Note over SDK, Sandbox: Invocation
    SDK->>Router: POST /v1/namespaces/.../invocations<br/>Authorization: Bearer jwt
    Router->>Router: Validate JWT signature using cached JWKS from Keycloak
    Router->>Router: Extract claims (sub, roles)
    Router->>Router: Check role authorization

    alt Valid token + authorized role
        Router->>WM: Forward (mTLS)<br/>X-AgentCube-User-ID, X-AgentCube-User-Roles
        WM->>WM: Create sandbox via K8s API
        Router->>Sandbox: Proxy to sandbox (mTLS)
        Sandbox-->>Router: Response
        Router-->>SDK: Response + x-agentcube-session-id
    else Invalid/expired token
        Router-->>SDK: 401 Unauthorized
    else Insufficient permissions
        Router-->>SDK: 403 Forbidden
    end
```

### 2.3 Keycloak Deployment

Keycloak is deployed as a `Deployment` in `agentcube-system`. A dedicated realm called `agentcube` is created during installation containing:

- **Clients:**
  - `agentcube-sdk` - Confidential client for SDK access. Supports `client_credentials` grant for service accounts and `authorization_code` for interactive users.
  - `agentcube-admin` - Client for administrative operations.

- **Roles:**
  - `sandbox:invoke` - Permission to invoke agent runtimes and code interpreters.
  - `sandbox:manage` - Permission to create/delete AgentRuntime and CodeInterpreter CRDs.
  - `admin` - Full administrative access.

### 2.4 Router JWT Validation

The Router validates Keycloak-issued JWTs using standard OIDC token verification:

```go
type KeycloakConfig struct {
    IssuerURL    string        // e.g., "https://keycloak.agentcube-system.svc:8443"
    Realm        string        // "agentcube"
    Audience     string        // expected audience claim
    JWKSCacheTTL time.Duration // how often to refresh JWKS keys
}

func NewKeycloakValidator(cfg KeycloakConfig) (*KeycloakValidator, error) {
    jwksURL := fmt.Sprintf("%s/realms/%s/protocol/openid-connect/certs",
        cfg.IssuerURL, cfg.Realm)

    // JWKS keys are cached locally - no per-request calls to Keycloak
    keySet := jwk.NewCache(context.Background())
    keySet.Register(jwksURL, jwk.WithRefreshInterval(cfg.JWKSCacheTTL))

    return &KeycloakValidator{
        issuer:   fmt.Sprintf("%s/realms/%s", cfg.IssuerURL, cfg.Realm),
        audience: cfg.Audience,
        keySet:   keySet,
    }, nil
}
```

Individual request validation is a local cryptographic operation. The Router reaches Keycloak only periodically (default: every 5 minutes) to refresh the JWKS key set, so Keycloak availability is not in the hot path.

### 2.5 SDK Changes

The Python SDK will support Keycloak-based authentication:

```python
from agentcube import CodeInterpreterClient

# Service account credentials (for automation / CI)
client = CodeInterpreterClient(
    auth=ServiceAccountAuth(
        keycloak_url="https://keycloak.example.com",
        realm="agentcube",
        client_id="agentcube-sdk",
        client_secret="<secret>",
    )
)

# Pre-obtained token
client = CodeInterpreterClient(
    auth=TokenAuth(access_token="<keycloak-jwt>")
)

# Usage unchanged - auth is transparent
result = client.run_code("python", "print('hello')")
```

Token lifecycle is handled automatically by the SDK, but differs by authentication method:

- **`ServiceAccountAuth` (`client_credentials` grant):** No refresh token is issued by Keycloak for this grant type. When the access token expires, the SDK re-authenticates by repeating the client credentials exchange using the configured `client_id` and `client_secret`.
- **`TokenAuth` (pre-obtained token):** The SDK uses the provided token as-is. The caller is responsible for providing a valid, non-expired token. This supports tokens obtained through any flow, including `authorization_code` (e.g., from a web application or CLI tool that performed the interactive login).


### 2.6 Backward Compatibility

External auth is opt-in via `--enable-external-auth` (default: `false`):

- **Disabled:** Router behaves exactly as today - no authentication required.
- **Enabled:** All invocation endpoints require `Authorization: Bearer <token>`. Health checks (`/health/live`, `/health/ready`) remain unauthenticated.

---

## 3. Authorization (Keycloak RBAC)

### 3.1 Overview

Authentication verifies *who* the user is. Authorization determines *what* that user is allowed to do. This proposal uses Keycloak's realm roles for role-based access control. Keycloak embeds the user's assigned roles directly into the JWT access token, and the Router checks these roles locally from the validated token - no additional call to Keycloak is needed at request time.

### 3.2 Role Hierarchy

Keycloak realm roles are organized in a simple hierarchy:

| Role | Permissions | Inherits |
|---|---|---|
| `sandbox:invoke` | Invoke agent runtimes, invoke code interpreters, list sessions | - |
| `sandbox:manage` | Create/delete AgentRuntime and CodeInterpreter CRDs | `sandbox:invoke` |
| `admin` | Full access, user management, view audit logs | `sandbox:manage` |

New users are assigned the `sandbox:invoke` role by default, maintaining backward compatibility with the current behavior where anyone can invoke sandboxes (but now with authentication).

### 3.3 How It Works

Keycloak embeds the user's roles into the JWT access token under the `realm_access.roles` claim. The Router reads these roles directly from the validated token and performs authorization checks locally - no additional call to Keycloak is needed.

Example JWT payload issued by Keycloak:

```json
{
  "sub": "user-123",
  "iss": "https://keycloak.agentcube-system.svc/realms/agentcube",
  "realm_access": {
    "roles": ["sandbox:invoke"]
  },
  "exp": 1893456000
}
```

### 3.4 Router Authorization Middleware

| Endpoint Pattern | Required Role |
|---|---|
| `POST /v1/namespaces/{ns}/agent-runtimes/{name}/invocations/*` | `sandbox:invoke` |
| `POST /v1/namespaces/{ns}/code-interpreters/{name}/invocations/*` | `sandbox:invoke` |
| `GET /health/*` | No auth required |

*(Note: CRD lifecycle operations like creating agent runtimes are handled directly via the Kubernetes API, not the Router's external surface).*

```go
func AuthzMiddleware(requiredRole string) gin.HandlerFunc {
    return func(c *gin.Context) {
        claims, exists := c.Get("jwt_claims")
        if !exists {
            c.AbortWithStatusJSON(401, gin.H{"error": "unauthenticated"})
            return
        }

        userRoles := claims.(*Claims).RealmAccess.Roles
        if !hasRole(userRoles, requiredRole) {
            c.AbortWithStatusJSON(403, gin.H{
                "error": "forbidden",
                "detail": fmt.Sprintf("role '%s' required", requiredRole),
            })
            return
        }

        c.Next()
    }
}
```

### 3.5 Namespace Scoping

For multi-tenant deployments where users should only access sandboxes in specific namespaces, Keycloak's protocol mappers can inject custom claims (e.g., `allowed_namespaces`) into the JWT. The Router would then check the target namespace in the request URL against the user's permitted namespaces from the token claims. This is still a local check from the JWT with no additional calls to Keycloak.

---

## 4. Cloud Provider Identity Federation (Stretch Goal)

This layer is not urgent and will only be pursued after Priorities 1, 2, and 3 are stable.

Keycloak natively supports identity brokering - acting as a proxy to external identity providers. Configuration is done entirely within Keycloak with no AgentCube code changes:

- **AWS IAM → Keycloak:** OIDC federation with AWS IAM Identity Center
- **Google Cloud Identity → Keycloak:** Google as OAuth2 identity provider
- **Azure AD → Keycloak:** SAML or OIDC identity provider

From the Router's perspective, nothing changes. It still validates Keycloak JWTs regardless of how the user originally authenticated.

```mermaid
graph LR
    AWS[AWS IAM] -->|OIDC| KC[Keycloak]
    GCP[Google Cloud Identity] -->|OAuth2| KC
    Azure[Azure AD] -->|SAML/OIDC| KC
    KC -->|JWT| Router[AgentCube Router]
```

---

## Future Enhancements

### OPA for Authorization

The standard across CNCF projects is to strictly separate authentication and authorization. Projects like Volcano, Istio, and ArgoCD follow the pattern of using tools like Keycloak for authentication and Open Policy Agent (OPA) for authorization. If AgentCube's authorization needs grow beyond what Keycloak RBAC offers, OPA could replace Keycloak's authorization role, keeping Keycloak focused purely on identity and token issuance.

The key advantages of OPA over Keycloak-based RBAC:

- **Decoupled policy evaluation:** Like the Keycloak RBAC approach in Section 3, OPA evaluates policies locally without per-request network calls. Its advantage is richer, more expressive policy logic that is decoupled from the identity provider - policies are evaluated independently of how tokens are issued or roles are assigned.
- **Policy as code:** Rego policies are version-controlled, peer-reviewed, and merged through standard PR workflows, giving full audit history over authorization changes.
- **Expressiveness:** OPA supports context-aware policies beyond simple role checks (e.g., time-based access, request payload inspection, cross-namespace constraints).

The approach for integration:

- Ship a set of standard Rego policies baked into AgentCube that cover common access control patterns out of the box.
- Expose a simplified JSON/YAML configuration interface so users can map roles to permissions without writing Rego directly.
- The Router would call OPA locally for policy evaluation, with Keycloak continuing to handle authentication and token issuance only.
