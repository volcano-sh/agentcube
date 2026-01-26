# Picod Plain Authentication Design

Author: Layne Peng

##### 2. Provisioning Phase

- The **Router** sends a sandbox allocation request to the **WorkloadManager**. Crucially, this request **does not** contain key data.
- The **WorkloadManager** constructs the Pod specification. It defines an environment variable `AGENTCUBE_ROUTER_PUBLIC_KEY` that is populated with the Router's public key.
  - **Preferred Method**: If the `AGENTCUBE_ROUTER_PUBLIC_KEY` environment variable is set in the WorkloadManager deployment, this value is used directly.
  - **Fallback Method**: Otherwise, the key is sourced from the `picod-router-identity` Secret.
- **PicoD** starts, reads the key from the **environment**, and initializes its JWT verifier.

Currently, AgentCube’s `picod` daemon enforces authentication and authorization using a client self-signed key-pair mechanism. This design binds a specific client (agent) directly to a `picod` instance, ensuring a secure, one-to-one relationship. Details of this existing implementation can be found in the [PicoD Authentication & Authorization Design](https://github.com/volcano-sh/agentcube/blob/main/docs/design/picod-proposal.md#3-authentication--authorization).

However, emerging use cases require a more flexible architecture where the client's authentication and authorization are offloaded to an upstream Router or Gateway. In this scenario, the Router is responsible for:
*   Allocating the appropriate built-in service (PicoD) to the user.
*   Managing the security of connections between clients (agents) and the service.

The existing self-signed key-pair model is incompatible with this centralized management flow, as it bypasses the Router's ability to mediate access. To address this, we propose a new **Plain Authentication** mechanism for `picod`. This design enables the Router/Gateway to manage credentials and connection security, simplifying the client-side workflow while maintaining robust access control.

## Use Cases

### Gateway-Managed Sandbox Access

A user requests a sandbox environment using the AgentCube Python SDK. The Router receives this request and validates the user's identity (authorization logic is handled upstream). Upon successful validation, the Router selects and allocates an available PicoD instance. It records the mapping between the specific Session ID and the requesting client. The Router then returns the connection credentials to the client, enabling the Python SDK to establish a direct connection with the PicoD instance using the plain authentication mechanism.

## Design Details

### Architecture Overview

To ensure **High Availability (HA)** across multiple Router replicas and enforce the principle of **Least Privilege**, this design implements a **Shared Identity Model** backed by Kubernetes primitives.

1. **Shared Authority (Router)**:
    
    - All Router replicas share a single cryptographic identity to function as a unified Token Issuer.
    - **Private Key Storage**: Stored in a Kubernetes Secret (picod-router-identity). The Private Key is accessible only by the Router component.
    - **Public Key Distribution**: Published to a Kubernetes ConfigMap (picod-router-public-key). This is accessible by the WorkloadManager and PicoD instances.
        
2. **Decoupled Provisioning (WorkloadManager)**:
    
    - It provisions sandboxes by injecting the Public Key into the picod container as an Environment Variable (AGENTCUBE_ROUTER_PUBLIC_KEY).
    - **Preferred Method**: The public key can be provided via the `AGENTCUBE_ROUTER_PUBLIC_KEY` environment variable during WorkloadManager deployment, avoiding the need to read from Kubernetes Secrets.
    - **Fallback Method**: If the environment variable is not set, WorkloadManager will read the public key from the Kubernetes Secret (`picod-router-identity`) for backward compatibility.
        
3. **Local Verification (PicoD)**:
    
    - `picod` instances trust the Router by loading the Public Key directly from the environment variable at startup.
        

### Workflow Description

The authentication lifecycle is divided into three phases: **Bootstrap**, **Provisioning**, and **Runtime Access**.

#### 1. Bootstrap Phase (Concurrency Control)

Upon startup, every Router replica executes an **Atomic Initialization Routine** to resolve the shared identity. This logic handles race conditions using Kubernetes' optimistic locking capabilities:

1. **Identity Acquisition**: The Router attempts to retrieve the picod-router-identity Secret.
    
    - If Missing: The Router generates a new RSA/ECDSA key pair in memory and attempts to **CREATE** the Secret.
    - Concurrency Handling: If the creation fails with 409 Conflict (implying another replica initialized it simultaneously), the Router discards its generated key and fetches the existing Secret created by the peer.
        
2. **Public Key Publication**: Once the Private Key is successfully loaded, the Router reconciles the picod-router-public-key ConfigMap. It ensures the Public Key in the ConfigMap matches the Private Key in memory.

#### 2. Provisioning Phase

- The **Router** sends a sandbox allocation request to the **WorkloadManager**. Crucially, this request **does not** contain key data.
- The **WorkloadManager** constructs the Pod specification. It defines an environment variable `PICOD_AUTH_PUBLIC_KEY` that sources its value from the `picod-router-public-key` ConfigMap (using `valueFrom: configMapKeyRef`).
- **PicoD** starts, reads the key from the **environment**, and initializes its JWT verifier.

#### 3. Runtime Access Phase

- The **SDK** sends a request to the Router.
- The **Router** validates the request and signs a JSON Web Token (JWT) using the shared Private Key.
- The request is forwarded to **PicoD** with the JWT in the Authorization header.
- **PicoD** verifies the signature using the locally mounted Public Key and processes the request.

### Sequence Diagram

The following diagram illustrates the initialization race condition handling and the resulting authentication flow.

```mermaid
sequenceDiagram
    autonumber
    participant SDK as SDK-Python
    participant Router as Router (Replica)
    participant K8s as K8s API (Secret/CM)
    participant WM as WorkloadManager
    participant PicoD as PicoD

    Note over Router, K8s: 1. Bootstrap (Identity Reconciliation)
    Router->>K8s: GET Secret "picod-router-identity"
    
    alt Secret Missing (First Initialization)
        Router->>Router: Generate New Key Pair (Priv/Pub)
        Router->>K8s: CREATE Secret (Private Key)
        
        alt Success
            Note right of Router: First to create (Created Key)
        else Failure (409 Conflict)
            Note right of Router: Others (Peer created Key)
            Router->>K8s: GET Secret (Retry)
        end
    else Secret Exists
        Note right of Router: Load existing Identity
    end

    Router->>Router: Load Private Key into Memory
    Router->>K8s: APPLY ConfigMap "picod-router-public-key"
    Note right of Router: Publishes Public Key for consumption

    Note over Router, PicoD: 2. Provisioning
    Router->>WM: Request Sandbox (No Key Payload)
    WM->>K8s: Create Pod (Env: AGENTCUBE_ROUTER_PUBLIC_KEY)
    K8s-->>PicoD: Start Container (Env: AGENTCUBE_ROUTER_PUBLIC_KEY)
    PicoD->>PicoD: Load Key from Env

    Note over SDK, PicoD: 3. Runtime Access
    SDK->>Router: Request (No Auth)
    Router->>Router: Sign JWT (w/ Private Key)
    Router->>PicoD: Forward Request (Header: Authorization Bearer <JWT>)
    PicoD->>PicoD: Verify Signature (w/ Public Key)
    PicoD-->>Router: Response (200 OK)
    Router-->>SDK: Response
```

## Data Structures & Configuration

### 1. Kubernetes Resources

The Router manages two primary resources in the installation namespace (e.g., agentcube-system).

**A. Identity Secret (Private)**

- **Name**: picod-router-identity
- **Type**: Opaque
- **Purpose**: Stores the private key shared among Router replicas.


```yaml
apiVersion: v1
kind: Secret
metadata:
  name: picod-router-identity
  namespace: agentcube-system
data:
  # Base64 encoded PKCS#8 Private Key (RSA or ECDSA)
  private.pem: <base64-encoded-key>
```

**B. Identity ConfigMap (Public)**

- **Name**: picod-router-public-key
- **Purpose**: Stores the public key mounted into PicoD instances.

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: picod-router-public-key
  namespace: agentcube-system
data:
  # Plain text Public Key (PEM format)
  public.pem: |
    -----BEGIN PUBLIC KEY-----
    ...
    -----END PUBLIC KEY-----
```

### 2. WorkloadManager Pod Spec

When creating the picod Pod, the WorkloadManager injects the key as follows:

**Preferred Method (Environment Variable):**

If `AGENTCUBE_ROUTER_PUBLIC_KEY` is set in the WorkloadManager deployment:

```yaml
apiVersion: v1
kind: Pod
spec:
  containers:
    - name: picod
      env:
        - name: AGENTCUBE_ROUTER_PUBLIC_KEY
          value: <router-public-key-from-env>
```

**Fallback Method (Programmatic Injection):**

If the environment variable is not set, the WorkloadManager reads the key from the `picod-router-identity` Secret and injects it directly:

```yaml
apiVersion: v1
kind: Pod
spec:
  containers:
    - name: picod
      env:
        - name: AGENTCUBE_ROUTER_PUBLIC_KEY
          valueFrom:
            secretKeyRef:
              name: picod-router-identity
              key: public.pem
```
  
### 3. PicoD Configuration
The existing CLI flags for authentication are deprecated.

* Environment Variable: picod requires `AGENTCUBE_ROUTER_PUBLIC_KEY` to be set.
* Behavior: If the environment variable is present, `picod` initializes the Plain Auth provider. If missing, it fails to start (or falls back to legacy mode if we decide to keep it for a transition period).

### 4. JWT Token Spec

The Router signs tokens using the standard JWT (RFC 7519) format.

**Header:**
- alg: RS256 (or ES256)
- typ: JWT

**Payload (Claims):**

```json
{
  "iss": "agentcube-router",        // Issuer: Fixed identifier for the Router
  "iat": 1716239000,                // Issued At: Unix timestamp
  "exp": 1716242600,                // Expiration: e.g., +1 hour
  "sub": "client-session-id",       // Subject: Identifies the client/session
  "aud": "picod-service"            // Audience: Intended recipient
}
```

## Scope & Constraints

### In Scope

1. **Router Identity Management**: Implementation of the "Bootstrap" logic to atomically create/load keys from Kubernetes.
2. **JWT Implementation**: Signing logic in Router and verification logic in PicoD.
3. **WorkloadManager Updates**: Modifying the Pod Spec generation to include the `AGENTCUBE_ROUTER_PUBLIC_KEY` environment variable.

### Out of Scope

1. **Key Rotation**: For this initial version, the key pair is static. If rotation is needed, an administrator must manually delete the Secret/ConfigMap and restart the Routers. Automatic key rotation is deferred to a future release.
2. **Token Revocation**: There is no mechanism to revoke a specific JWT before it expires. Security relies on short expiration intervals.
3. **Fine-Grained RBAC**: Authorization is binary (valid/invalid). Granular permissions within the sandbox (e.g., "read-only access") are not part of this proposal.
