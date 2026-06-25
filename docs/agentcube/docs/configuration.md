---
sidebar_position: 4
---

# Configuration Reference

This page provides a complete reference for all configurable options in AgentCube, including Helm chart values, Custom Resource Definition (CRD) fields, and environment variables.

---

## Helm Chart Values

Install or upgrade AgentCube using the Helm chart in `manifests/charts/base`. Pass overrides with `--set key=value` or via a custom `values.yaml`.

### Redis

AgentCube requires a Redis (or Valkey) instance for session state synchronization.

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `redis.addr` | `string` | `""` | **Required.** Redis server address in `host:port` format (e.g., `redis.agentcube.svc.cluster.local:6379`). |
| `redis.password` | `string` | `""` | Redis password. Creates a chart-managed Kubernetes Secret. Do **not** set together with `redis.secretName`. |
| `redis.secretName` | `string` | `""` | Name of an existing Kubernetes Secret containing the Redis password. **Recommended for production.** |
| `redis.secretKey` | `string` | `"password"` | The key inside the Secret (chart-managed or external) that holds the password. |

**Example (production with existing Secret):**
```bash
kubectl -n agentcube create secret generic agentcube-redis \
    --from-literal=password='YOUR_SECURE_PASSWORD'

helm install agentcube ./manifests/charts/base \
  --set redis.addr="redis.agentcube.svc.cluster.local:6379" \
  --set redis.secretName="agentcube-redis"
```

---

### AgentCube Router

The Router is the data plane component that handles all incoming invocation requests.

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `router.replicas` | `int` | `1` | Number of Router pod replicas. Increase for high availability. |
| `router.image.repository` | `string` | `ghcr.io/volcano-sh/agentcube-router` | Container image repository. |
| `router.image.tag` | `string` | `"latest"` | Container image tag. Pin this to a specific version in production. |
| `router.image.pullPolicy` | `string` | `IfNotPresent` | Kubernetes image pull policy (`Always`, `Never`, `IfNotPresent`). |
| `router.service.type` | `string` | `ClusterIP` | Kubernetes Service type (`ClusterIP`, `NodePort`, `LoadBalancer`). |
| `router.service.port` | `int` | `8080` | Port exposed by the Service. |
| `router.service.targetPort` | `int` | `8080` | Port the container listens on. |
| `router.resources.limits.cpu` | `string` | `500m` | CPU limit for the Router container. |
| `router.resources.limits.memory` | `string` | `512Mi` | Memory limit for the Router container. |
| `router.resources.requests.cpu` | `string` | `100m` | CPU request for the Router container. |
| `router.resources.requests.memory` | `string` | `128Mi` | Memory request for the Router container. |
| `router.serviceAccountName` | `string` | `"agentcube-router"` | Kubernetes ServiceAccount name for the Router. |
| `router.extraEnv` | `list` | `[]` | Additional environment variables to inject into the Router pods. |
| `router.config` | `object` | `{}` | Extra configuration map entries for the Router. |

#### Router JWT / OIDC Authentication

When `router.jwt.issuerUrl` is set, the Router validates all incoming API requests against an external OIDC identity provider (Keycloak, Okta, Auth0, Dex, etc.).

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `router.jwt.issuerUrl` | `string` | `""` | OIDC issuer URL. When set, enables external JWT validation. |
| `router.jwt.audience` | `string` | `"agentcube-api"` | Expected `aud` claim value in access tokens. |
| `router.jwt.roleClaim` | `string` | `""` | **Required if `issuerUrl` is set.** JSON path to the roles array in the JWT (e.g., `"realm_access.roles"` for Keycloak, `"groups"` for Okta). |
| `router.jwt.requiredRole` | `string` | `""` | **Required if `issuerUrl` is set.** The role required to access API endpoints (e.g., `"sandbox:invoke"`). |

**Example (Keycloak):**
```yaml
router:
  jwt:
    issuerUrl: "http://keycloak.agentcube-system.svc:8080/realms/agentcube"
    audience: "agentcube-api"
    roleClaim: "realm_access.roles"
    requiredRole: "sandbox:invoke"
```

---

### Workload Manager

The Workload Manager is the control plane component that manages sandbox lifecycle.

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `workloadmanager.replicas` | `int` | `1` | Number of Workload Manager pod replicas. |
| `workloadmanager.image.repository` | `string` | `ghcr.io/volcano-sh/workloadmanager` | Container image repository. |
| `workloadmanager.image.tag` | `string` | `"latest"` | Container image tag. |
| `workloadmanager.image.pullPolicy` | `string` | `IfNotPresent` | Kubernetes image pull policy. |
| `workloadmanager.service.type` | `string` | `ClusterIP` | Kubernetes Service type. |
| `workloadmanager.service.port` | `int` | `8080` | Service port. |
| `workloadmanager.resources.limits.cpu` | `string` | `500m` | CPU limit. |
| `workloadmanager.resources.limits.memory` | `string` | `512Mi` | Memory limit. |
| `workloadmanager.resources.requests.cpu` | `string` | `100m` | CPU request. |
| `workloadmanager.resources.requests.memory` | `string` | `128Mi` | Memory request. |
| `workloadmanager.extraEnv` | `list` | `[]` | Additional environment variables. |

---

### Volcano Scheduler (Optional)

AgentCube can leverage the Volcano agent scheduler for advanced AI workload placement. Disabled by default.

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `volcano.scheduler.enabled` | `bool` | `false` | Whether to deploy the Volcano agent scheduler. |
| `volcano.scheduler.replicas` | `int` | `1` | Number of scheduler pod replicas. |
| `volcano.scheduler.image.repository` | `string` | `ghcr.io/volcano-sh/vc-agent-scheduler` | Scheduler image repository. |
| `volcano.scheduler.image.tag` | `string` | `"latest"` | Scheduler image tag. |

---

### SPIRE (Internal mTLS Identity) — Optional

AgentCube supports [SPIRE](https://spiffe.io/) for zero-trust, mTLS-based communication between all internal components.

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `spire.enabled` | `bool` | `false` | Whether to enable SPIRE for internal workload identity. |
| `spire.trustDomain` | `string` | `"cluster.local"` | SPIFFE trust domain for the cluster. |
| `spire.clusterName` | `string` | `"agentcube-cluster"` | SPIRE cluster name identifier. |
| `spire.server.ca.ttl` | `string` | `"24h"` | Upstream CA certificate TTL. |
| `spire.server.ca.x509SvidDefaultTtl` | `string` | `"1h"` | Default X.509 SVID TTL. |
| `spire.agent.insecureBootstrap` | `bool` | `false` | For local dev clusters (kind/minikube). Set to `true` to skip attestation verification. |
| `spire.agent.skipKubeletVerification` | `bool` | `false` | For local dev clusters. Skip kubelet certificate verification. |

**Example (local dev cluster):**
```bash
helm install agentcube ./manifests/charts/base \
  --set spire.enabled=true \
  --set spire.agent.insecureBootstrap=true \
  --set spire.agent.skipKubeletVerification=true
```

---

## CRD Reference: AgentRuntime

The `AgentRuntime` CRD defines a template for long-running, conversational agent sandboxes.

**API Group**: `runtime.agentcube.volcano.sh/v1alpha1`

### Spec Fields

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `targetPort` | `[]TargetPort` | Yes | — | List of ports/paths the agent runtime exposes. |
| `podTemplate` | `SandboxTemplate` | Yes | — | Template for the sandbox Pod. |
| `sessionTimeout` | `Duration` | No | `15m` | Inactivity duration after which a session is paused. |
| `maxSessionDuration` | `Duration` | No | `8h` | Maximum lifetime of a session, regardless of activity. |

### TargetPort Fields

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `pathPrefix` | `string` | No | — | URL path prefix routed to this port (e.g., `/api`). |
| `name` | `string` | No | — | Optional human-readable name for this port. |
| `port` | `uint32` | Yes | — | Port number the agent container listens on. |
| `protocol` | `string` | Yes | `HTTP` | Protocol type. Allowed values: `HTTP`, `HTTPS`. |

### SandboxTemplate (AgentRuntime)

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `labels` | `map[string]string` | No | Labels to apply to the sandbox Pod. |
| `annotations` | `map[string]string` | No | Annotations to apply to the sandbox Pod. |
| `spec` | `corev1.PodSpec` | Yes | Full Kubernetes Pod specification for the sandbox container. |

**Example:**
```yaml
apiVersion: runtime.agentcube.volcano.sh/v1alpha1
kind: AgentRuntime
metadata:
  name: my-agent
  namespace: default
spec:
  targetPort:
    - pathPrefix: "/"
      port: 8080
      protocol: "HTTP"
  podTemplate:
    spec:
      containers:
        - name: agent
          image: my-registry/my-agent:latest
          resources:
            limits:
              cpu: "1"
              memory: "1Gi"
  sessionTimeout: "30m"
  maxSessionDuration: "4h"
```

---

## CRD Reference: CodeInterpreter

The `CodeInterpreter` CRD is optimized for short-lived, secure code execution sessions.

**API Group**: `runtime.agentcube.volcano.sh/v1alpha1`

### Spec Fields

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `ports` | `[]TargetPort` | No | — | Ports the code interpreter exposes (e.g., `/execute`, `/files`). Defaults to using AgentCube's PicoD. |
| `template` | `CodeInterpreterSandboxTemplate` | Yes | — | Sandbox configuration for the code interpreter. |
| `sessionTimeout` | `Duration` | No | `15m` | Duration of inactivity before the session is eligible for cleanup. |
| `maxSessionDuration` | `Duration` | No | `8h` | Maximum session lifetime. |
| `warmPoolSize` | `int32` | No | — | Number of pre-warmed sandbox Pods to maintain. Reduces cold-start latency. |
| `authMode` | `string` | No | `picod` | Authentication mode. `picod` injects RSA public key auth; `none` disables auth injection for custom images. |

### CodeInterpreterSandboxTemplate Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `image` | `string` | Yes | Container image for the code interpreter (e.g., `ghcr.io/volcano-sh/picod:latest`). |
| `imagePullPolicy` | `string` | No | Image pull policy (`Always`, `Never`, `IfNotPresent`). |
| `imagePullSecrets` | `[]LocalObjectReference` | No | Secrets for pulling private images. |
| `runtimeClassName` | `string` | No | Kubernetes RuntimeClass for hardware-level isolation (e.g., `kata-qemu`). |
| `command` | `[]string` | No | Override the container's entrypoint command. |
| `args` | `[]string` | No | Arguments passed to the entrypoint. |
| `environment` | `[]corev1.EnvVar` | No | Environment variables to set in the sandbox. |
| `resources` | `corev1.ResourceRequirements` | No | CPU and memory limits and requests. |
| `labels` | `map[string]string` | No | Labels to apply to the sandbox Pod. |
| `annotations` | `map[string]string` | No | Annotations to apply to the sandbox Pod. |

**Example:**
```yaml
apiVersion: runtime.agentcube.volcano.sh/v1alpha1
kind: CodeInterpreter
metadata:
  name: python-interpreter
  namespace: default
spec:
  warmPoolSize: 2
  authMode: "picod"
  template:
    image: ghcr.io/volcano-sh/picod:latest
    args:
      - --workspace=/root
    resources:
      limits:
        cpu: "500m"
        memory: "512Mi"
      requests:
        cpu: "100m"
        memory: "128Mi"
  sessionTimeout: "15m"
  maxSessionDuration: "8h"
```

---

## Python SDK Environment Variables

When using the `agentcube` Python SDK, these environment variables configure the connection to AgentCube services:

| Variable | Required | Description |
|----------|----------|-------------|
| `WORKLOAD_MANAGER_URL` | Yes (if not passed directly) | URL of the Workload Manager service (e.g., `http://localhost:8080`). |
| `ROUTER_URL` | Yes (if not passed directly) | URL of the AgentCube Router service (e.g., `http://localhost:8081`). |

Set them before running your Python code:

```bash
export WORKLOAD_MANAGER_URL="http://localhost:8080"
export ROUTER_URL="http://localhost:8081"
```

Or pass them directly when constructing the client:

```python
from agentcube import CodeInterpreterClient

client = CodeInterpreterClient(
    name="my-interpreter",
    workload_manager_url="http://localhost:8080",
    router_url="http://localhost:8081"
)
```
