# Securing External Access with Keycloak (OIDC)

This task shows you how to add external authentication and authorization to
AgentCube using [Keycloak](https://www.keycloak.org/) as the identity provider.
By the end, every request to the Router API will require a valid JWT token
issued by Keycloak, and access will be controlled by realm roles (RBAC) and
resource ownership (RLAC).

## Before you begin

1. Follow the [Getting Started](../getting-started.md) guide to install
   AgentCube on your cluster and deploy the `sample-agent` runtime. **Do not**
   enable OIDC during the initial installation - this tutorial walks through
   that step explicitly.

2. Make sure you have the following tools installed:
   - [`kubectl`](https://kubernetes.io/docs/tasks/tools/) (v1.25+)
   - [`helm`](https://helm.sh/docs/intro/install/) (v3.12+)
   - [`curl`](https://curl.se/) (any recent version)
   - [`jq`](https://jqlang.github.io/jq/download/) (for parsing JSON
     responses)

3. Confirm AgentCube is running without external auth:

   ```bash
   kubectl get pods -n agentcube
   ```

   You should see the Router and WorkloadManager pods in `Running` state:

```
    NAME                                READY   STATUS    RESTARTS   AGE
    agentcube-router-7d5dccf588-br86j   1/1     Running   0          11s
    workloadmanager-5d55d76645-xqdcl    1/1     Running   0          11s
```

4. Confirm the Router is currently reachable **without** a token.

   Open a **new terminal** and port-forward the Router:

   ```bash
   kubectl port-forward svc/agentcube-router -n agentcube 8081:8080
   ```

   In your **original terminal**, check the health endpoint:

   ```bash
   curl -s http://localhost:8081/health/live
   ```

   Expected output:

    ```json
    {"status":"alive"}
    ```

   Stop the port-forward by pressing `Ctrl+C` in the terminal where it is
   running.

## What gets deployed

When you install the Keycloak addon and enable OIDC, the following resources are
created or modified:

| Resource | Kind | Purpose |
|---|---|---|
| `keycloak` | Deployment (1 replica) | Keycloak identity provider. Imports the `agentcube` realm on first startup. |
| `keycloak` | Service (ClusterIP) | Exposes Keycloak on port 8080 inside the cluster. |
| `keycloak-realm-config` | Secret | Contains the realm JSON with roles, clients, and scope mappings. |
| `keycloak-credentials` | Secret | Stores admin password and client secrets. |

The Keycloak addon also auto-configures the following inside the `agentcube`
realm:

| Item | Details |
|---|---|
| **Roles** | `sandbox:invoke` lets callers invoke agent runtimes and code interpreters, and is assigned by default. `sandbox:manage` inherits invoke and is intended for creating/deleting AgentRuntime and CodeInterpreter resources. `admin` inherits manage and can bypass ownership checks for administrative access. |
| **Clients** | `agentcube-app` (confidential, for SDKs), `agentcube-cli` (public, for browsers/CLI), `agentcube-admin` (confidential, for admin ops) |
| **Audience** | All tokens include `agentcube-api` in the `aud` claim via a protocol mapper |

After enabling OIDC on the Router, **all** requests to `/v1/...` endpoints
require a valid Bearer token. Health endpoints (`/health/live`, `/health/ready`)
remain unauthenticated for Kubernetes probes.

## Step 1 - Deploy the Keycloak addon

Install Keycloak into your AgentCube namespace using the addon Helm chart. You
must provide an admin username, admin password, and client secrets for the three
confidential OAuth2 clients. These client secrets are **plain-text shared
secrets** (similar to API keys). Keycloak uses them to authenticate the caller during
the OAuth2 `client_credentials` grant, the same way any standard OAuth2
provider works.

For this tutorial, we use simple demo values. **For production**, generate
strong random secrets:

```bash
# Generate a strong random secret (repeat for each client)
openssl rand -base64 32
```

Install the Keycloak addon with the client secrets:

```bash
helm upgrade --install keycloak manifests/charts/addons/keycloak \
  --namespace agentcube \
  --set admin.username=admin \
  --set admin.password=admin \
  --set clients.service.secret=my-service-secret \
  --set clients.router.secret=my-router-secret \
  --set clients.admin.secret=my-admin-secret
```

Expected output:

```
  Release "keycloak" does not exist. Installing it now.
  NAME: keycloak
  LAST DEPLOYED: Sat Jun  6 03:03:47 2026
  NAMESPACE: agentcube
  STATUS: deployed
  REVISION: 1
  DESCRIPTION: Install complete
  TEST SUITE: None
```

> **Tip:**
> The addon chart uses `devMode: true` by default, which runs Keycloak with
> an embedded H2 database. This is fine for local development and testing.
> For production deployments, set `devMode=false` and provide an external
> database - see the chart's `values.yaml` for the full set of production
> options.

Wait for Keycloak to be ready. The JVM startup plus realm import typically
takes 60–90 seconds:

```bash
kubectl rollout status deployment/keycloak -n agentcube --timeout=300s
```

Expected output:

```
Waiting for deployment "keycloak" rollout to finish: 0 of 1 updated replicas are available...
deployment "keycloak" successfully rolled out
```

Verify the Keycloak pod is running:

```bash
kubectl get pods -n agentcube -l app=keycloak
```

Expected output:

```
NAME                       READY   STATUS    RESTARTS   AGE
keycloak-f8d7dff7b-b2p24   1/1     Running   0          2m44s
```

## Step 2 - Enable OIDC on the Router

Now configure the Router to validate tokens against Keycloak. This is done by
upgrading the base AgentCube Helm release with OIDC settings. Use
`--reuse-values` so your existing configuration (Redis, images, SPIRE, etc.) is
preserved.

```bash
helm upgrade agentcube manifests/charts/base \
  -n agentcube \
  --reuse-values \
  --set router.jwt.issuerUrl=http://keycloak.agentcube.svc.cluster.local:8080/realms/agentcube \
  --set router.jwt.roleClaim=realm_access.roles \
  --set router.jwt.requiredRole=sandbox:invoke
```

Expected output:

```
Release "agentcube" has been upgraded. Happy Helming!
NAME: agentcube
LAST DEPLOYED: Sat Jun  6 03:07:07 2026
NAMESPACE: agentcube
STATUS: deployed
REVISION: 4
DESCRIPTION: Upgrade complete
TEST SUITE: None
```

This tells the Router:

- **`issuerUrl`** - where to discover OIDC configuration and signing keys
  (JWKS). The Router fetches these once at startup and caches them.
- **`roleClaim`** - the dot-separated path inside the JWT where roles are
  stored. For Keycloak this is `realm_access.roles`.
- **`requiredRole`** - the minimum role required to access the API. Users
  without `sandbox:invoke` will receive a `403 Forbidden`.

Wait for the Router to restart with the new configuration:

```bash
kubectl rollout status deployment/agentcube-router -n agentcube --timeout=120s
```

## Step 3 - Verify that unauthenticated requests are rejected

Open a **new terminal** and port-forward the Router:

```bash
kubectl port-forward svc/agentcube-router -n agentcube 8081:8080
```

In your **original terminal**, try making a request to the `sample-agent` (which you deployed in the Getting Started guide) **without** a token:

```bash
curl -s -w "\nHTTP Status: %{http_code}\n" http://localhost:8081/v1/namespaces/default/agent-runtimes/sample-agent/invocations/
```

Expected output — the Router now rejects unauthenticated requests:

```
{"code":"UNAUTHORIZED","error":"missing authorization header"}
HTTP Status: 401
```

## Step 4 - Obtain a token from Keycloak

Open **another new terminal** and port-forward Keycloak so you can reach it
from your local machine:

```bash
kubectl port-forward svc/keycloak -n agentcube 8082:8080
```

Back in your **original terminal**, proceed with the token requests below.

### Get a service account token

Use the `client_credentials` grant to obtain a token for the
`agentcube-app` client. Note the `Host` header - this is needed because
Keycloak validates the issuer against the original hostname, and we're
connecting through a port-forward:

```bash
KEYCLOAK_TOKEN=$(curl -s -X POST \
  -H "Host: keycloak.agentcube.svc.cluster.local:8080" \
  "http://localhost:8082/realms/agentcube/protocol/openid-connect/token" \
  -d "grant_type=client_credentials" \
  -d "client_id=agentcube-app" \
  -d "client_secret=my-service-secret" | jq -r '.access_token')

echo $KEYCLOAK_TOKEN
```

Expected output - a long base64-encoded JWT string:

### Inspect the token (optional)

You can decode the token to see its claims:

```bash
echo $KEYCLOAK_TOKEN | cut -d'.' -f2 | base64 -d 2>/dev/null | jq .
```

Expected output - you should see `realm_access.roles` containing
`sandbox:invoke`, and `aud` containing `agentcube-api`:

```
{
  "exp": 1780695969,
  "iat": 1780695669,
  "jti": "31ef712f-f62a-4a4e-8714-7ccf710ab476",
  "iss": "http://keycloak.agentcube.svc.cluster.local:8080/realms/agentcube",
  "aud": "agentcube-api",
  "sub": "bb0f4c04-e1d2-4bdb-b7bb-56d5c1cefe50",
  "typ": "Bearer",
  "azp": "agentcube-app",
  "acr": "1",
  "realm_access": {
    "roles": [
      "sandbox:invoke"
    ]
  },
  "scope": "profile email",
  "email_verified": false,
  "clientHost": "127.0.0.1",
  "preferred_username": "service-account-agentcube-app",
  "clientAddress": "127.0.0.1",
  "client_id": "agentcube-app"
}
```

### Get an admin token

The `agentcube-admin` client has the `admin` role, which grants full access
including the ability to bypass resource ownership checks (RLAC):

```bash
ADMIN_TOKEN=$(curl -s -X POST \
  -H "Host: keycloak.agentcube.svc.cluster.local:8080" \
  "http://localhost:8082/realms/agentcube/protocol/openid-connect/token" \
  -d "grant_type=client_credentials" \
  -d "client_id=agentcube-admin" \
  -d "client_secret=my-admin-secret" | jq -r '.access_token')

echo $ADMIN_TOKEN
```

## Step 5 - Make an authenticated request

Now use the token to make an authenticated request through the Router:

```bash
curl -s -o /dev/null -w "HTTP Status: %{http_code}\n" \
  -H "Authorization: Bearer $KEYCLOAK_TOKEN" \
  http://localhost:8081/v1/namespaces/default/agent-runtimes/sample-agent/invocations/
```

Expected output (the response body is discarded to show only the status code):

```
HTTP Status: 200
```

## Step 6 - Verify RBAC enforcement

Try using an **invalid** token to confirm the Router rejects it:

```bash
curl -s -w "\nHTTP Status: %{http_code}\n" \
  -H "Authorization: Bearer invalid-token-here" \
  http://localhost:8081/v1/namespaces/default/agent-runtimes/sample-agent/invocations/
```

Expected output :

```
{"code":"UNAUTHORIZED","error":"invalid or expired token"}
HTTP Status: 401
```

## Step 7 - Use the Python SDK with authentication

The AgentCube Python SDK supports Keycloak authentication via the
`ServiceAccountAuth` provider. This uses the `client_credentials` grant and
automatically refreshes tokens before they expire.

### Install the SDK

```bash
pip install -e sdk-python/
```

### Example usage

Create a Python script (or run in a Python REPL):

```python
from agentcube import ServiceAccountAuth
from agentcube import AgentRuntimeClient

# Initialize authentication with Keycloak
auth = ServiceAccountAuth(
    token_url="http://localhost:8082/realms/agentcube/protocol/openid-connect/token",
    client_id="agentcube-app",
    client_secret="my-service-secret",
    headers={"Host": "keycloak.agentcube.svc.cluster.local:8080"},
)

# Create the client - it will automatically attach Bearer tokens to requests
client = AgentRuntimeClient(
    router_url="http://localhost:8081",
    namespace="default",
    agent_name="sample-agent",
    auth=auth,
)

# Invoke the agent
response = client.invoke(payload={})
print(f"Response: {response}")
```

Expected output:

If you are using the mock `sample-agent` from the Getting Started guide (which runs Python's built-in `http.server`), the script will raise a `501 Server Error: Not Implemented` exception. This is expected because `http.server` only handles `GET`/`HEAD` requests and doesn't implement `POST` handlers. A real agent framework (e.g., FastAPI or Flask) implementing `POST` handlers will return the actual response.

The `ServiceAccountAuth` class handles the full token lifecycle:
- Fetches a new token on the first request
- Caches the token in memory
- Automatically refreshes 30 seconds before expiry

## Understanding what changed

The Helm chart passes four `--jwt-*` flags to the Router when
`router.jwt.issuerUrl` is set. The Router discovers Keycloak's signing keys
once at startup (via OIDC discovery), caches them, and validates every incoming
JWT locally — no per-request calls to Keycloak.

> **Note:** The `--import-realm` flag only applies on Keycloak's **first
> startup**. Changes to the realm Secret after that will **not** take effect
> automatically. Use the Keycloak Admin Console or Admin API to update a live
> realm.

### Realm roles

| Role | Permissions | Assigned to |
|------|-------------|-------------|
| `sandbox:invoke` | Invoke agent runtimes and code interpreters | Default role (all users/clients) |
| `sandbox:manage` | Create/delete AgentRuntime and CodeInterpreter CRDs. Inherits `sandbox:invoke`. | — |
| `admin` | Full administrative access. Bypasses ownership checks. Inherits `sandbox:manage`. | `agentcube-admin` client |

### OAuth2 clients

| Client ID | Type | Grant | Purpose |
|-----------|------|-------|---------|
| `agentcube-app` | Confidential | `client_credentials` | Python SDK, external services (M2M) |
| `agentcube-cli` | Public | Authorization code + PKCE | CLI, browser-based apps (interactive) |
| `agentcube-admin` | Confidential | `client_credentials` | Administrative operations |

## Cleanup

Stop all port-forwards by pressing `Ctrl+C` in each terminal where they are
running.

If you want to **disable** external auth and go back to unauthenticated access,
remove the OIDC configuration from the Router:

```bash
helm upgrade agentcube manifests/charts/base \
  -n agentcube \
  --reuse-values \
  --set router.jwt.issuerUrl="" \
  --set router.jwt.roleClaim="" \
  --set router.jwt.requiredRole=""
```

Wait for the Router to restart:

```bash
kubectl rollout status deployment/agentcube-router -n agentcube --timeout=120s
```

To also remove the Keycloak addon entirely:

```bash
helm uninstall keycloak -n agentcube
```

This removes the Keycloak Deployment, Service, and associated Secrets from the
cluster.
