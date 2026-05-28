# Securing Internal Traffic with SPIRE (mTLS)

This task shows you how to enable mutual TLS (mTLS) between AgentCube's
control-plane components using [SPIRE](https://spiffe.io/docs/latest/spire-about/spire-concepts/).
By the end, every request between the Router and WorkloadManager will be
cryptographically authenticated using short-lived X.509 certificates that rotate
automatically.

## Before you begin

1. Follow the [Getting Started](../getting-started.md) guide to install
   AgentCube on your cluster. **Do not** enable SPIRE during the initial
   installation - this tutorial walks through that step explicitly.

2. Make sure you have the following tools installed:
   - [`kubectl`](https://kubernetes.io/docs/tasks/tools/) (v1.25+)
   - [`helm`](https://helm.sh/docs/intro/install/) (v3.12+)

3. Confirm AgentCube is running without SPIRE:

   ```bash
   kubectl get pods -n agentcube-system
   ```

   You should see the Router and WorkloadManager pods in `Running` state, each
   showing `1/1` containers ready (no sidecar yet):

   ```
   NAME                                READY   STATUS    RESTARTS   AGE
   agentcube-router-5d8f9b7c4-xxxxx    1/1     Running   0          5m
   workloadmanager-6b6bb75d98-xxxxx    1/1     Running   0          5m
   ```

> **Tip :**
> If you are running on a local [Kind](https://kind.sigs.k8s.io/) or
[Minikube](https://minikube.sigs.k8s.io/) cluster, you will need to pass two
extra overrides in the Helm upgrade command shown below. These are already
included in the instructions, so just keep them in..


## What gets deployed

When you enable SPIRE, the Helm chart creates the following additional resources
inside your cluster:

| Resource | Kind | Purpose |
|---|---|---|
| `spire-server` | StatefulSet (1 replica) | Central certificate authority. Runs the SPIRE Controller Manager as a sidecar. |
| `spire-agent` | DaemonSet | Runs on every node. Attests workloads and delivers certificates. |
| `ClusterSPIFFEID` (×2) | CRD | Declarative identity registration for the Router and WorkloadManager. |
| `spiffe-helper` sidecar | Container (injected) | Fetches and rotates certificates inside the Router and WorkloadManager pods. |

The Router and WorkloadManager pods will each go from `1/1` to `2/2` containers
(the main process + the `spiffe-helper` sidecar).

## Step 1 - Install the SPIRE Controller Manager CRDs

The SPIRE Controller Manager watches `ClusterSPIFFEID` custom resources. These
CRDs must be present in the cluster **before** the Helm upgrade, otherwise the
chart will fail to create them.

```bash
kubectl apply -k "https://github.com/spiffe/spire-controller-manager/config/crd?ref=v0.6.4"
```

Verify the CRD was installed:

```bash
kubectl get crd clusterspiffeids.spire.spiffe.io
```

Expected output:

```
NAME                                  CREATED AT
clusterspiffeids.spire.spiffe.io      2025-XX-XXTXX:XX:XXZ
```

## Step 2 - Upgrade the Helm release with SPIRE enabled

Run the Helm upgrade with `spire.enabled=true`. The extra `--set` flags for
`insecureBootstrap` and `skipKubeletVerification` are needed for local
development clusters (Kind / Minikube). On a production cluster with proper
kubelet certificates, you can omit them.

```bash
helm upgrade agentcube manifests/charts/base \
  -n agentcube-system \
  --set spire.enabled=true \
  --set spire.agent.insecureBootstrap=true \
  --set spire.agent.skipKubeletVerification=true
```

This single command deploys the full SPIRE infrastructure **and** injects the
`spiffe-helper` sidecar into the Router and WorkloadManager pods.

Wait for everything to become ready:

```bash
kubectl rollout status statefulset/spire-server -n agentcube-system --timeout=120s
kubectl rollout status daemonset/spire-agent -n agentcube-system --timeout=120s
kubectl rollout status deployment/agentcube-router -n agentcube-system --timeout=120s
kubectl rollout status deployment/workloadmanager -n agentcube-system --timeout=120s
```

## Step 3 - Verify SPIRE is healthy

Check that the SPIRE Server is up and has registered agents:

```bash
kubectl exec -n agentcube-system statefulset/spire-server -c spire-server -- \
  /opt/spire/bin/spire-server agent list
```

You should see at least one agent entry (one per cluster node):

```
Found X attested agent(s):

SPIFFE ID         : spiffe://cluster.local/spire/agent/k8s_psat/agentcube-cluster/...
Attestation type  : k8s_psat
...
```

Next, confirm the identity registrations were picked up from the
`ClusterSPIFFEID` resources:

```bash
kubectl exec -n agentcube-system statefulset/spire-server -c spire-server -- \
  /opt/spire/bin/spire-server entry show
```

You should see entries for both the Router and WorkloadManager, with SPIFFE IDs
following the format
`spiffe://cluster.local/ns/agentcube-system/sa/<service-account>`:

```
Entry ID         : ...
SPIFFE ID        : spiffe://cluster.local/ns/agentcube-system/sa/agentcube-router
Parent ID        : ...
Revision         : 0

Entry ID         : ...
SPIFFE ID        : spiffe://cluster.local/ns/agentcube-system/sa/workloadmanager
Parent ID        : ...
Revision         : 0
```

## Step 4 - Verify the sidecar and certificates

Confirm that both the Router and WorkloadManager pods now show `2/2` containers
(the main container + the `spiffe-helper` sidecar):

```bash
kubectl get pods -n agentcube-system
```

Expected output:

```
NAME                                READY   STATUS    RESTARTS   AGE
agentcube-router-7f8d4b9c6-xxxxx    2/2     Running   0          2m
workloadmanager-8c7dd85f9-xxxxx     2/2     Running   0          2m
spire-agent-xxxxx                   1/1     Running   0          2m
spire-server-0                      2/2     Running   0          2m
```

Check the Router logs to confirm mTLS is active. You should see a log line
indicating it is waiting for, and then successfully loading, the certificates:

```bash
kubectl logs -n agentcube-system deployment/agentcube-router -c agentcube-router | grep -i mtls
```

Expected output:

```
Waiting for Router mTLS cert/key/CA files
All mTLS cert/key/CA files are present
Router→WorkloadManager mTLS enabled: expecting server SPIFFE ID spiffe://cluster.local/ns/agentcube-system/sa/workloadmanager
```

Do the same for the WorkloadManager:

```bash
kubectl logs -n agentcube-system deployment/workloadmanager -c workloadmanager | grep -i mtls
```

Expected output:

```
Waiting for WorkloadManager mTLS cert/key/CA files
All mTLS cert/key/CA files are present
WorkloadManager mTLS enabled: accepting clients with valid SPIRE-provisioned certificates
```

## Step 5 - Test it end-to-end

Deploy a simple agent and invoke it through the Router to confirm the full
mTLS-secured path works:

```bash
kubectl apply -f - <<EOF
apiVersion: runtime.agentcube.volcano.sh/v1alpha1
kind: AgentRuntime
metadata:
  name: mtls-test
  namespace: default
spec:
  targetPort:
    - pathPrefix: "/"
      port: 8000
      protocol: "HTTP"
  podTemplate:
    spec:
      containers:
        - name: agent
          image: python:3.11-slim
          command: ["python3", "-m", "http.server", "8000"]
          resources:
            requests:
              cpu: "100m"
              memory: "128Mi"
            limits:
              cpu: "500m"
              memory: "512Mi"
EOF
```

Port-forward the Router and send a request:

```bash
kubectl port-forward -n agentcube-system svc/agentcube-router 8080:8080 &
curl -s -o /dev/null -w "%{http_code}" \
  http://localhost:8080/v1/namespaces/default/agent-runtimes/mtls-test/invocations/test/
```

If the mTLS handshake between Router and WorkloadManager succeeds, you will see
a `200` (or `502` while the sandbox is still booting - retry after a few
seconds). A TLS-related error in the Router logs would indicate a
misconfiguration.

## Understanding what changed

Here is how each component is configured behind the scenes. You do **not** need
to set any of these flags manually - the Helm chart handles it when
`spire.enabled=true`.

### Router (mTLS client)

The Helm chart passes these flags to the Router binary:

```
--mtls-cert=/run/spire/certs/svid.pem
--mtls-key=/run/spire/certs/svid_key.pem
--mtls-ca=/run/spire/certs/svid_bundle.pem
```

When all three are present, the Router creates a dedicated HTTPS transport for
its WorkloadManager connection. It verifies that the WorkloadManager's
certificate contains the expected SPIFFE ID
(`spiffe://cluster.local/ns/agentcube-system/sa/workloadmanager`).

### WorkloadManager (mTLS server)

The Helm chart passes these flags to the WorkloadManager binary:

```
--tls-cert=/run/spire/certs/svid.pem
--tls-key=/run/spire/certs/svid_key.pem
--tls-ca=/run/spire/certs/svid_bundle.pem
```

When the CA file is present, the WorkloadManager starts its HTTP server with
mTLS enabled. It requires every connecting client to present a valid certificate
signed by the trusted CA. Authorization is handled at the application layer, not
at the TLS level.

### Certificate rotation

The `spiffe-helper` sidecar continuously fetches fresh SVIDs from the local
SPIRE Agent and writes them to a shared volume at `/run/spire/certs/`. A
`CertWatcher` inside each component watches that directory using `fsnotify` and
hot-reloads the certificates without dropping any active connections. The default
SVID TTL is **1 hour**.

### What about sandboxes?

mTLS is only used for the control-plane path (Router ↔ WorkloadManager).
The Router→Sandbox connection continues to use the existing JWT-based
authentication. This keeps sandbox startup latency low and avoids injecting
SPIRE dependencies into user-defined runtime containers.

## Cleanup

Remove the test agent:

```bash
kubectl delete agentruntime mtls-test -n default
```

If you want to **disable** SPIRE and go back to plain HTTP between the control
plane components, run the Helm upgrade again with `spire.enabled=false`:

```bash
helm upgrade agentcube manifests/charts/base \
  -n agentcube-system \
  --set spire.enabled=false
```

This removes all SPIRE resources (Server, Agent, CRDs, sidecars) and the
Router/WorkloadManager pods will restart with `1/1` containers.

To also remove the SPIRE Controller Manager CRDs:

```bash
kubectl delete -k "https://github.com/spiffe/spire-controller-manager/config/crd?ref=v0.6.4"
```