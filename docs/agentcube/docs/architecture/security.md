# Security Model

Security is a first-class citizen in AgentCube. Since agents often run untrusted user code or handle sensitive data, we employ a multi-layered security strategy.

## 1. Sandbox Isolation

Every agent session runs in its own dedicated sandbox.

- **Resource Hardening**: We use microVMs or hardened containers to ensure that one session cannot break out and access the host or other sessions.
- **Strict Limits**: CPU and Memory limits are enforced at the hardware/cgroup level to prevent "noisy neighbor" or Denial-of-Service attacks.

## 2. Asymmetric Code Signing

AgentCube uses a unique "Split-Key" authentication model to ensure that **only you** can run code in **your** sandbox.

1. **Client-Side Key Generation**: When you start a session using the SDK, a temporary **RSA-2048 key pair** is generated locally on your machine.
2. **Public Key Injection**: Your **Public Key** is sent to the Workload Manager and injected into your specific sandbox during initialization.
3. **Request Signing**: Every command you send (e.g., `execute_command`) is signed by your **Private Key** to create a JWT.
4. **On-Box Verification**: The daemon inside the sandbox (`PicoD`) validates the signature of every incoming request using your Public Key.

:::info
The **Private Key never leaves your client**. Even if the AgentCube Router or Workload Manager were compromised, an attacker could not execute code in your running sandboxes.
:::

## 3. Ephemeral Lifecycles

Resources are only alive as long as they are needed.

- **Automatic Hibernation**: Idle sandboxes are paused to reduce the attack surface.
- **Enforced TTL**: Every sandbox has a maximum "Time To Live" (e.g., 8 hours). Once reached, it is permanently deleted and its memory is sanitized.
- **No Persistence**: By default, the sandbox filesystem is ephemeral. Data is wiped clean between sessions.

## 4. In-Cluster Security

For deployments within Kubernetes:

- **Service Account Integration**: Components use standard Kubernetes RBAC for internal communication.
- **Network Policies**: Tight network policies restrict sandboxes from accessing the Kubernetes API or other internal services.
