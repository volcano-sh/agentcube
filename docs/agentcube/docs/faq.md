---
sidebar_position: 7
---

# FAQ

Frequently asked questions about AgentCube.

---

## General

### What is AgentCube?

AgentCube is a specialized subproject within the [Volcano](https://volcano.sh/) community that provides a Kubernetes-native platform for running AI Agent and code interpreter workloads. It treats agents and interpreters as first-class citizens — with dedicated scheduling, lifecycle management, and secure sandbox isolation.

---

### Is AgentCube production-ready?

AgentCube is currently in its **Proposal and Early Design phase**. It is under active development and is suitable for experimentation and pilot deployments. Specific APIs and feature sets are subject to change based on community consensus. Follow the [GitHub repository](https://github.com/volcano-sh/agentcube) for updates.

---

### Do I need Volcano installed to use AgentCube?

**No, Volcano is not strictly required.** The Volcano agent scheduler (`vc-agent-scheduler`) is an optional component, disabled by default in the Helm chart (`volcano.scheduler.enabled: false`). You can run AgentCube with standard Kubernetes scheduling.

That said, enabling the Volcano scheduler provides advanced bin-packing, priority-based placement, and GPU-aware scheduling that significantly improves performance at scale.

---

### What is the relationship between AgentCube and Volcano?

AgentCube is a **subproject of Volcano**. It extends Volcano's strengths in managing compute-intensive batch workloads to cover the unique patterns of AI Agent workloads: interactive, stateful, intermittently active, and latency-sensitive. Volcano provides the scheduling infrastructure; AgentCube provides the agent-specific lifecycle management on top of it.

---

### What languages does the AgentCube codebase use?

- **Control plane** (Workload Manager, Router, PicoD daemon): **Go**
- **Client SDK**: **Python**
- **Website/docs**: **TypeScript** (Docusaurus)

---

## Architecture

### What's the difference between `AgentRuntime` and `CodeInterpreter`?

| | AgentRuntime | CodeInterpreter |
|-|--------------|-----------------|
| **Use case** | Long-running, conversational agents | Short-lived code execution sessions |
| **Security** | Standard Kubernetes Pod spec | Stricter defaults (no hostPath, restricted capabilities) |
| **Warm pool** | Not supported | Supported (`warmPoolSize`) |
| **Authentication** | Configurable | PicoD asymmetric RSA by default |
| **Typical user** | Multi-turn chat agents, tool-using agents | REPL sessions, "run this code" actions |

Both share the same sandbox infrastructure and session lifecycle model.

---

### What is PicoD?

**PicoD** (Pico Daemon) is a lightweight RESTful HTTP daemon that runs inside every `CodeInterpreter` sandbox. It replaces traditional SSH-based access with a simpler, more secure model:

- Exposes `/api/execute` for command/code execution.
- Exposes `/api/files` for file upload/download.
- Exposes `/health` for readiness checks.
- Uses RSA JWT-based authentication (the private key never leaves the client).

PicoD is what the AgentCube Python SDK communicates with — you don't interact with it directly.

---

### Is there a 1:1 mapping between sessions and sandboxes?

**Yes.** AgentCube maintains a strict **1:1 mapping between a session and a sandbox**. This ensures:
- Complete isolation — one user's code cannot access another user's data.
- Consistent context — state is preserved across multiple turns within a session.
- Clean cleanup — when a session ends, the entire sandbox (filesystem, memory, network identity) is deleted.

---

### Does filesystem state persist across `run_code` calls?

**Yes, within the same session.** Files written to the sandbox filesystem in one `run_code` call are visible in subsequent calls within the same session.

However, **Python variables do NOT persist** between `run_code` calls, because each call spawns a new process. Use files to pass state between calls:

```python
# Call 1: Write a result to a file
client.run_code("python", "open('/tmp/result.txt', 'w').write('42')")

# Call 2: Read it back
result = client.run_code("python", "print(open('/tmp/result.txt').read())")
```

---

### What happens to my sandbox when I disconnect?

The sandbox continues running until `sessionTimeout` expires (default: 15 minutes of inactivity). After that, it is **paused** (hibernated) to free resources. If no new traffic arrives within an additional 10 minutes, it is permanently **deleted**.

You can configure these durations per-resource:
```yaml
spec:
  sessionTimeout: "60m"     # hibernate after 1 hour idle
  maxSessionDuration: "24h" # always delete after 24 hours
```

---

## Security

### Is my code safe from other users?

**Yes.** Each session runs in its own isolated sandbox. AgentCube's security model includes:

1. **Process isolation**: Each sandbox is a separate Kubernetes Pod (or microVM with `runtimeClassName`).
2. **Resource limits**: CPU and memory are capped to prevent noisy-neighbor issues.
3. **Asymmetric authentication**: Requests must be signed by a private key that is only ever on the client's machine. Not even the AgentCube Router can forge your requests.
4. **Ephemeral filesystems**: By default, all sandbox data is wiped when the session ends.

---

### Can I use hardware-level (microVM) isolation?

**Yes.** Set `runtimeClassName` in the `CodeInterpreterSandboxTemplate` to use Kata Containers, Kuasar, or another microVM runtime:

```yaml
spec:
  template:
    runtimeClassName: kata-qemu
```

This requires the corresponding RuntimeClass to be installed and configured on your Kubernetes cluster nodes.

---

### What authentication modes are available?

AgentCube supports three authentication approaches:

| Mode | Description | Configure Via |
|------|-------------|---------------|
| **PicoD (default)** | RSA-2048 asymmetric key signing (client-side key pair) | `CodeInterpreter.spec.authMode: "picod"` |
| **External JWT/OIDC** | Validates Bearer tokens from Keycloak, Okta, Auth0, etc. | `router.jwt.*` Helm values |
| **Internal mTLS (SPIRE)** | Automatic workload identity and mTLS between all components | `spire.enabled: true` Helm value |

These modes are complementary — you can run external JWT validation at the Router level and PicoD auth at the sandbox level simultaneously.

---

## Performance

### How fast are cold starts?

Without a warm pool, a new session requires pulling a container image and starting a pod, which can take **5–30 seconds** depending on image size and cluster speed.

With `warmPoolSize > 0`, an already-running pod is adopted, reducing cold-start time to **under 1 second** for the session setup.

---

### How many sessions can I run concurrently?

This is limited only by your cluster's resources. Each sandbox has configurable CPU and memory requests/limits. AgentCube's Workload Manager efficiently packs sandboxes using Volcano's bin-packing algorithm.

---

### Can AgentCube scale to zero?

**Yes.** Sandboxes are created lazily (on first request) and are automatically deleted after `sessionTimeout` + 10 minutes of inactivity. This means you only consume resources when sessions are actually active.

---

## Development

### How do I build AgentCube locally?

See the [Local Development Guide](./developer-guide/local-development.md) for a full walkthrough. The quick version:

```bash
git clone https://github.com/volcano-sh/agentcube.git
cd agentcube
make build-all  # Builds all binaries to ./bin/
```

---

### How do I run the tests?

```bash
make test          # Unit tests
make e2e           # End-to-end tests (requires a cluster)
make lint          # Linting
```

See the [Testing Guide](./developer-guide/testing.md) for details.

---

### How do I contribute?

See the [Contributing Guide](./developer-guide/contributing.md). We welcome all contributions — bug reports, documentation improvements, feature proposals, and code changes.
