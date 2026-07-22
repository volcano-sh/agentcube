---
sidebar_position: 6
---

# Troubleshooting

Common problems and how to fix them. If something is broken, start here.

---

## Installation Issues

### Pods are stuck in `Pending` state

**Symptom:** `kubectl get pods -n agentcube` shows Workload Manager or Router pods with status `Pending`.

**Diagnose:**

```bash
kubectl describe pod <pod-name> -n agentcube
```

**Common causes and solutions:**

| Cause                                       | Solution                                                                               |
| ------------------------------------------- | -------------------------------------------------------------------------------------- |
| Insufficient cluster resources (CPU/memory) | Scale up your cluster or reduce resource requests in Helm values                       |
| Missing `agent-sandbox` CRDs                | Run `kubectl get crd \| grep sandbox` to confirm. Re-install agent-sandbox if missing. |
| Image pull error (private registry)         | Set `imagePullSecrets` in Helm values or ensure the cluster has access to `ghcr.io`    |

---

### `kubectl get crd | grep agentcube` returns nothing

**Cause:** The Helm chart installation failed silently, or the CRDs were not applied.

**Solution:**

```bash
# Check Helm release status
helm status agentcube -n agentcube

# Manually apply CRDs
kubectl apply -f manifests/crd/
```

---

### Helm install fails with Redis error

**Symptom:** Helm install fails with an error like `redis: connection refused`.

**Cause:** The Redis deployment isn't running before AgentCube is installed, or the address is wrong.

**Solution:**

```bash
# Check Redis is running
kubectl get pods -n agentcube | grep redis

# Wait for Redis to be ready, then install AgentCube
kubectl -n agentcube rollout status deployment/redis
```

Verify you've set the correct Redis address in `redis.addr`:

```bash
helm install agentcube ./manifests/charts/base \
  --set redis.addr="redis.agentcube.svc.cluster.local:6379"
```

---

## Session and Sandbox Issues

### Session creation fails: `Bad Request` from Router

**Symptom:** The Router returns a `400 Bad Request` when attempting a new invocation.

**Diagnose:**

```bash
kubectl logs -n agentcube deployment/agentcube-router
kubectl logs -n agentcube deployment/workloadmanager
```

**Common causes:**

- The `AgentRuntime` or `CodeInterpreter` resource does not exist in the specified namespace.
- The resource name in the URL does not match.
- The `agent-sandbox` CRDs or controller are not running.

```bash
# Verify the resource exists
kubectl get agentruntime my-agent -n default
kubectl get codeinterpreter my-interpreter -n default
```

---

### Sessions expire unexpectedly

**Symptom:** Sessions time out faster than expected.

**Cause:** Default `sessionTimeout` is `15m`. If your agent is idle for 15 minutes, the sandbox is paused. After another 10 minutes of being paused, it is permanently deleted.

**Solution:** Increase the timeout in your CRD spec:

```yaml
spec:
  sessionTimeout: "60m"
  maxSessionDuration: "24h"
```

---

### Cold starts are too slow (no warm pool)

**Symptom:** New `CodeInterpreter` sessions take 30+ seconds to become ready.

**Solution:** Enable warm pooling:

```yaml
spec:
  warmPoolSize: 3 # Keep 3 pre-warmed sandboxes
```

After updating, verify the warm pool pods are running:

```bash
kubectl get pods -n default -l agentcube.volcano.sh/warmpool=true
```

---

## Authentication Issues

### `401 Unauthorized` from PicoD

**Symptom:** Requests to `/api/execute` or `/api/files` return `401 Unauthorized`.

**Common causes:**

1. **Session JWT is expired**: JWTs are short-lived. The SDK should auto-renew them, but if you're making raw HTTP calls, ensure `exp` is in the future.

2. **Wrong private key**: The JWT must be signed by the private key whose corresponding public key was injected during `/init`. If you've created a new SDK client for an existing session, it won't have the original private key.

3. **Sandbox not initialized**: If you bypassed the Workload Manager and called PicoD directly without `/init`, it will reject all requests.

**Debug:**

```bash
# Decode a JWT to inspect claims (don't send private keys to external services)
echo "<your_jwt>" | cut -d. -f2 | base64 -d 2>/dev/null | python3 -m json.tool
```

---

### External JWT validation fails (`router.jwt` is configured)

**Symptom:** Router returns `401 Unauthorized` even with a valid token.

**Diagnose:**

```bash
kubectl logs -n agentcube deployment/agentcube-router | grep jwt
```

**Checklist:**

- Is `router.jwt.issuerUrl` pointing to the correct OIDC discovery URL?
- Does the token's `aud` claim match `router.jwt.audience`?
- Does the user have the role specified in `router.jwt.requiredRole`?
- Is the OIDC provider reachable from within the cluster?

```bash
# Test OIDC discovery from inside the cluster
kubectl -n agentcube exec deployment/agentcube-router -- \
  curl -s <issuerUrl>/.well-known/openid-configuration | python3 -m json.tool
```

---

## Python SDK Issues

### `ConnectionRefusedError` when running the SDK

**Symptom:** The Python SDK raises `ConnectionRefusedError` on the first call.

**Cause:** The Workload Manager or Router isn't accessible from your local machine.

**Solution:** Set up port-forwarding in separate terminal windows:

```bash
# Terminal 1
kubectl port-forward -n agentcube svc/workloadmanager 8080:8080

# Terminal 2
kubectl port-forward -n agentcube svc/agentcube-router 8081:8080
```

Then set environment variables:

```bash
export WORKLOAD_MANAGER_URL="http://localhost:8080"
export ROUTER_URL="http://localhost:8081"
```

---

### `WORKLOAD_MANAGER_URL` not set error

**Symptom:** SDK raises `ValueError: WORKLOAD_MANAGER_URL is not set`.

**Solution:** Set the environment variable or pass the URL directly:

```python
from agentcube import CodeInterpreterClient

client = CodeInterpreterClient(
    workload_manager_url="http://localhost:8080",
    router_url="http://localhost:8081"
)
```

---

### Code execution times out

**Symptom:** `run_code()` raises a `TimeoutError`.

**Cause:** The default execution timeout is 30 seconds. Long-running computations will exceed this.

**Solution:** Pass a longer timeout:

```python
result = client.run_code("python", long_script, timeout=300)  # 5 minutes
```

---

## SPIRE / mTLS Issues

### SPIRE agent `CrashLoopBackOff`

**Symptom:** `kubectl get pods -n agentcube | grep spire` shows SPIRE agent pods crashing.

**Cause:** For local development clusters (Kind/Minikube), the SPIRE agent cannot verify the kubelet certificate by default.

**Solution:**

```bash
helm upgrade agentcube ./manifests/charts/base \
  --set spire.agent.insecureBootstrap=true \
  --set spire.agent.skipKubeletVerification=true
```

---

## Diagnostic Commands

Use these commands to quickly gather diagnostic information:

```bash
# Overall component health
kubectl get pods -n agentcube
kubectl get pods -n agent-sandbox-system

# Logs from all components
kubectl logs -n agentcube deployment/agentcube-router --tail=100
kubectl logs -n agentcube deployment/workloadmanager --tail=100

# Check events for issues
kubectl get events -n agentcube --sort-by='.lastTimestamp'

# Describe a specific problematic pod
kubectl describe pod <pod-name> -n agentcube

# Check CRDs
kubectl get crd | grep agentcube
kubectl get agentruntime -A
kubectl get codeinterpreter -A

# Check Redis connectivity
kubectl -n agentcube exec deployment/workloadmanager -- \
  redis-cli -h redis.agentcube.svc.cluster.local ping
```

---

## Getting Help

If you cannot resolve an issue with the steps above:

1. **Search existing issues**: [GitHub Issues](https://github.com/volcano-sh/agentcube/issues)
2. **Open a new issue**: Include the output of diagnostic commands above, your Helm values, and a description of the expected vs. actual behavior.
3. **Security vulnerabilities**: Email `volcano-security@googlegroups.com` instead of filing a public issue.
