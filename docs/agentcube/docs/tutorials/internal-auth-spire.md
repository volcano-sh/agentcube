# Securing Internal Traffic with SPIRE (mTLS)

This tutorial covers how we use SPIRE to establish zero-trust, mutually authenticated TLS (mTLS) for AgentCube's control plane.

## Why Do We Need This?

By default, internal components trust each other based on network reachability. With our new mTLS implementation, we're locking down the control plane so the Router and WorkloadManager cryptographically verify each other's SPIFFE identities before communicating.

## 1. How the Architecture Works

We implemented a robust mTLS abstraction layer that handles the heavy lifting for the control plane:

- **Strict Identity Enforcement**: The Router and WorkloadManager have hardcoded SPIFFE IDs. 
  - The WorkloadManager accepts any client presenting a valid certificate signed by the trusted CA pool (authorization is handled later at the application layer).
  - However, the Router strictly verifies it's actually talking to the `WorkloadManagerSPIFFEID` before it forwards any traffic, preventing spoofed servers.
- **Zero-Downtime Rotation**: A new `CertWatcher` actively monitors the certificates on disk (using `fsnotify`). When SPIRE rotates the short-lived certs, they are hot-reloaded without dropping any active connections.

### What about Sandboxes?

You might wonder why we don't inject mTLS into the `PicoD` or `AgentRuntime` sandboxes:
- **Startup Latency**: Establishing a new TLS handshake for every short-lived sandbox adds significant latency. We opted to use our existing, blazing-fast JWT-based authentication for the `Router -> Sandbox` path instead.
- **User-Defined Runtimes**: `AgentRuntime` sandboxes are user-defined containers. By avoiding mTLS sidecar injection, we keep them clean and pure without forcing SPIRE dependencies on them.
- **WorkloadManager isolation**: The WorkloadManager never communicates directly with sandboxes over HTTP; it solely manages them via the secure Kubernetes API.

## 2. Enabling mTLS on the Control Plane

To turn on mTLS, you just need to pass the appropriate certificate paths to the binaries. They automatically enable mTLS when the CA bundle is provided alongside the cert and key.

For the **Router**, use the `mtls` prefix:
```bash
--mtls-cert=/path/to/tls.crt
--mtls-key=/path/to/tls.key
--mtls-ca=/path/to/ca.crt
```

For the **WorkloadManager**, use the `tls` prefix:
```bash
--tls-cert=/path/to/tls.crt
--tls-key=/path/to/tls.key
--tls-ca=/path/to/ca.crt
```

When you deploy AgentCube via our Helm charts, you don't have to manually manage these certificates. Instead, the **`spiffe-helper` sidecar** runs alongside the Router and WorkloadManager containers in their respective pods. 

Here is what the `spiffe-helper` sidecar does in the background:
1. It securely authenticates with the local SPIRE Agent.
2. It fetches the short-lived SVIDs (certificates) for the control plane component.
3. It writes the certificates to a shared volume where the component's `CertWatcher` instantly picks them up.
4. It continuously handles rotation before the certificates expire.

## 3. Verifying It Works

Once you've applied the configuration:

1. Check the logs for the Router and WorkloadManager. You'll see the `CertWatcher` output confirming it has successfully loaded the certificates.
2. Try deploying an agent and sending a request. 
3. If everything is wired correctly, the Router will perform the mTLS handshake and verify the WorkloadManager's SPIFFE ID when provisioning the sandbox, and then seamlessly fall back to the low-latency JWT auth when proxying your request directly to the sandbox.

## Next Steps

Now that your control plane communications are locked down, your AgentCube deployment is running a zero-trust architecture. You can safely deploy sensitive agents in multi-tenant environments.