#!/bin/bash
set -euo pipefail

CLUSTER_NAME=${E2E_CLUSTER_NAME:-agentcube-e2b-e2e}
NAMESPACE=${AGENTCUBE_NAMESPACE:-agentcube}
AGENT_SANDBOX_VERSION=${AGENT_SANDBOX_VERSION:-v1.0.9}
REDIS_IMAGE=${REDIS_IMAGE:-redis:7-alpine}
WM_IMAGE=${WORKLOAD_MANAGER_IMAGE:-workloadmanager:e2b-test}
ROUTER_IMAGE=${ROUTER_IMAGE:-agentcube-router:e2b-test}
CI_IMAGE=${E2B_CI_IMAGE:-e2b-code-interpreter:e2b-test}
LOCAL_PORT=${E2E_LOCAL_PORT:-18081}
CLEANUP_CLUSTER=${E2E_CLEANUP_CLUSTER:-true}

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
TMP_DIR="$(mktemp -d)"
E2E_VENV_DIR=${E2E_VENV_DIR:-"${TMP_DIR}/venv"}
CREATED_VENV=false
CREATED_CLUSTER=false
PF_PID=""

step() {
    echo
    echo "==> $1"
}

dump_logs() {
    echo "--- WorkloadManager logs ---"
    kubectl -n "${NAMESPACE}" logs deployment/workloadmanager --tail=100 2>&1 || true
    echo "--- AgentCube Router logs ---"
    kubectl -n "${NAMESPACE}" logs deployment/agentcube-router --tail=100 2>&1 || true
    echo "--- Pods ---"
    kubectl -n "${NAMESPACE}" get pods -o wide 2>&1 || true
    for p in $(kubectl -n "${NAMESPACE}" get pods -o name 2>/dev/null | grep e2b-code-interpreter || true); do
        echo "--- Logs for ${p} ---"
        kubectl -n "${NAMESPACE}" logs "${p}" --tail=150 2>&1 || true
        kubectl -n "${NAMESPACE}" exec "${p}" -- tail -n 50 /var/log/code-interpreter.log /var/log/jupyter.log 2>&1 || true
    done
}

cleanup() {
    local exit_code=$?
    if [ "${exit_code}" -ne 0 ]; then
        dump_logs
    fi
    if [ -n "${PF_PID}" ]; then
        kill "${PF_PID}" 2>/dev/null || true
    fi
    rm -rf "${TMP_DIR}" || true
    if [ "${CREATED_VENV}" = "true" ] && [ -n "${E2E_VENV_DIR}" ]; then
        rm -rf "${E2E_VENV_DIR}" || true
    fi
    if [ "${CLEANUP_CLUSTER}" = "true" ] && [ "${CREATED_CLUSTER}" = "true" ]; then
        kind delete cluster --name "${CLUSTER_NAME}" >/dev/null 2>&1 || true
    fi
}
trap cleanup EXIT

for cmd in docker kind kubectl go python3 openssl; do
    command -v "${cmd}" >/dev/null 2>&1 || {
        echo "Missing required command: ${cmd}" >&2
        exit 1
    }
done

cd "${REPO_ROOT}"

step "Preparing images (${WM_IMAGE}, ${ROUTER_IMAGE}, ${CI_IMAGE})"
docker build -f docker/Dockerfile -t "${WM_IMAGE}" .
docker build -f docker/Dockerfile.router -t "${ROUTER_IMAGE}" .
if ! docker image inspect "${CI_IMAGE}" >/dev/null 2>&1; then
    docker build -f docker/Dockerfile.e2b-code-interpreter -t "${CI_IMAGE}" .
fi

for img in "${REDIS_IMAGE}" "registry.k8s.io/agent-sandbox/agent-sandbox-controller:${AGENT_SANDBOX_VERSION}"; do
    if ! docker image inspect "${img}" >/dev/null 2>&1; then
        docker pull "${img}"
    fi
done

step "Creating Kind cluster ${CLUSTER_NAME}"
if kind get clusters 2>/dev/null | grep -Fxq "${CLUSTER_NAME}"; then
    echo "Kind cluster ${CLUSTER_NAME} already exists; refusing to overwrite (override with E2E_CLUSTER_NAME)." >&2
    exit 1
fi
kind create cluster --name "${CLUSTER_NAME}"
CREATED_CLUSTER=true

HOST_ARCH="$(docker info --format '{{.Architecture}}' | sed 's/x86_64/amd64/; s/aarch64/arm64/')"
HOST_PLATFORM="linux/${HOST_ARCH}"

load_image_into_kind() {
    local img="$1"
    if kind load docker-image "${img}" --name "${CLUSTER_NAME}"; then
        return 0
    fi
    local tar_file
    tar_file="${TMP_DIR}/$(echo "${img}" | tr '/:' '__').tar"
    echo "Fallback: saving single-platform (${HOST_PLATFORM}) archive for ${img}..."
    docker save --platform "${HOST_PLATFORM}" -o "${tar_file}" "${img}"
    if ! kind load image-archive "${tar_file}" --name "${CLUSTER_NAME}"; then
        docker exec -i "${CLUSTER_NAME}-control-plane" \
            ctr --namespace=k8s.io images import --platform "${HOST_PLATFORM}" - < "${tar_file}"
    fi
    rm -f "${tar_file}"
}

step "Loading images into Kind"
for img in \
    "${REDIS_IMAGE}" \
    "registry.k8s.io/agent-sandbox/agent-sandbox-controller:${AGENT_SANDBOX_VERSION}" \
    "${WM_IMAGE}" \
    "${ROUTER_IMAGE}" \
    "${CI_IMAGE}"; do
    load_image_into_kind "${img}"
done

step "Installing agent-sandbox (${AGENT_SANDBOX_VERSION}) and AgentCube CRDs"
kubectl apply --validate=false -f "https://github.com/kubernetes-sigs/agent-sandbox/releases/download/${AGENT_SANDBOX_VERSION}/sandbox-with-extensions.yaml"
kubectl -n agent-sandbox-system rollout status deployment/agent-sandbox-controller --timeout=300s

kubectl create namespace "${NAMESPACE}" --dry-run=client -o yaml | kubectl apply -f -
kubectl apply --server-side --force-conflicts --validate=false -f manifests/charts/base/crds/

step "Deploying Redis"
kubectl -n "${NAMESPACE}" apply -f - <<EOF
apiVersion: apps/v1
kind: Deployment
metadata:
  name: redis
spec:
  replicas: 1
  selector:
    matchLabels:
      app: redis
  template:
    metadata:
      labels:
        app: redis
    spec:
      containers:
        - name: redis
          image: ${REDIS_IMAGE}
          ports:
            - containerPort: 6379
---
apiVersion: v1
kind: Service
metadata:
  name: redis
spec:
  ports:
    - port: 6379
      targetPort: 6379
  selector:
    app: redis
EOF
kubectl -n "${NAMESPACE}" rollout status deployment/redis --timeout=180s

step "Generating self-signed wildcard TLS cert and API keys"
# Prefer *.e2b.localhost when external nip.io is intercepted by fake-ip/proxy DNS;
# LibreSSL rejects single-label wildcards (*.localhost), whereas *.e2b.localhost matches RFC 6125.
E2B_DOMAIN="e2b.localhost:${LOCAL_PORT}"
NIP_IP=$(python3 -c "import socket; print(socket.gethostbyname('api.127.0.0.1.nip.io'))" 2>/dev/null || true)
if [ "${NIP_IP}" = "127.0.0.1" ]; then
    E2B_DOMAIN="127.0.0.1.nip.io:${LOCAL_PORT}"
fi
echo "Using E2B_DOMAIN=${E2B_DOMAIN}"

cat > "${TMP_DIR}/openssl.cnf" <<'EOF'
[req]
distinguished_name = req_distinguished_name
x509_extensions = v3_ext
prompt = no

[req_distinguished_name]
CN = agentcube-router-e2b-test

[v3_ext]
basicConstraints = CA:TRUE
keyUsage = critical, digitalSignature, keyEncipherment, keyCertSign
extendedKeyUsage = serverAuth
subjectAltName = @alt_names

[alt_names]
DNS.1 = localhost
DNS.2 = e2b.localhost
DNS.3 = *.e2b.localhost
DNS.4 = 127.0.0.1.nip.io
DNS.5 = *.127.0.0.1.nip.io
IP.1 = 127.0.0.1
EOF

openssl req -x509 -nodes -newkey rsa:2048 -days 2 \
    -keyout "${TMP_DIR}/tls.key" \
    -out "${TMP_DIR}/tls.crt" \
    -config "${TMP_DIR}/openssl.cnf" >/dev/null 2>&1

API_KEY_A="e2b_test_tenant_a_key_12345"
API_KEY_B="e2b_test_tenant_b_key_67890"
HASH_A=$(printf '%s' "${API_KEY_A}" | openssl dgst -sha256 | awk '{print $NF}')
HASH_B=$(printf '%s' "${API_KEY_B}" | openssl dgst -sha256 | awk '{print $NF}')

kubectl -n "${NAMESPACE}" create secret tls e2b-router-tls \
    --cert="${TMP_DIR}/tls.crt" \
    --key="${TMP_DIR}/tls.key" \
    --dry-run=client -o yaml | kubectl apply -f -

kubectl -n "${NAMESPACE}" create secret generic e2b-api-keys \
    --from-literal="${HASH_A}=tenant-a:${NAMESPACE}:readwrite" \
    --from-literal="${HASH_B}=tenant-b:${NAMESPACE}:readwrite" \
    --dry-run=client -o yaml | kubectl apply -f -

step "Deploying WorkloadManager and AgentCube Router"
kubectl apply -f - <<EOF
apiVersion: v1
kind: ServiceAccount
metadata:
  name: workloadmanager
  namespace: ${NAMESPACE}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: workloadmanager-e2e
rules:
  - apiGroups: ["agents.x-k8s.io"]
    resources: ["sandboxes"]
    verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
  - apiGroups: ["extensions.agents.x-k8s.io"]
    resources: ["sandboxclaims", "sandboxtemplates", "sandboxwarmpools"]
    verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
  - apiGroups: ["extensions.agents.x-k8s.io"]
    resources: ["sandboxwarmpools/status"]
    verbs: ["get", "update", "patch"]
  - apiGroups: ["runtime.agentcube.volcano.sh"]
    resources: ["codeinterpreters"]
    verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
  - apiGroups: ["runtime.agentcube.volcano.sh"]
    resources: ["codeinterpreters/status"]
    verbs: ["get", "update", "patch"]
  - apiGroups: ["runtime.agentcube.volcano.sh"]
    resources: ["codeinterpreters/finalizers"]
    verbs: ["update"]
  - apiGroups: ["runtime.agentcube.volcano.sh"]
    resources: ["agentruntimes"]
    verbs: ["get", "list", "watch"]
  - apiGroups: ["runtime.agentcube.volcano.sh"]
    resources: ["agentruntimes/status"]
    verbs: ["update", "patch"]
  - apiGroups: [""]
    resources: ["pods", "secrets"]
    verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
  - apiGroups: ["authentication.k8s.io"]
    resources: ["tokenreviews"]
    verbs: ["create"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: workloadmanager-e2e
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: workloadmanager-e2e
subjects:
  - kind: ServiceAccount
    name: workloadmanager
    namespace: ${NAMESPACE}
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: agentcube-router
  namespace: ${NAMESPACE}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: agentcube-router-e2e
rules:
  - apiGroups: ["runtime.agentcube.volcano.sh"]
    resources: ["codeinterpreters"]
    verbs: ["get", "list", "watch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: agentcube-router-e2e
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: agentcube-router-e2e
subjects:
  - kind: ServiceAccount
    name: agentcube-router
    namespace: ${NAMESPACE}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: agentcube-router-secrets
  namespace: ${NAMESPACE}
rules:
  - apiGroups: [""]
    resources: ["secrets"]
    verbs: ["get", "create"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: agentcube-router-secrets
  namespace: ${NAMESPACE}
subjects:
  - kind: ServiceAccount
    name: agentcube-router
    namespace: ${NAMESPACE}
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: agentcube-router-secrets
EOF

kubectl -n "${NAMESPACE}" apply -f - <<EOF
apiVersion: apps/v1
kind: Deployment
metadata:
  name: workloadmanager
spec:
  replicas: 1
  selector:
    matchLabels:
      app: workloadmanager
  template:
    metadata:
      labels:
        app: workloadmanager
    spec:
      serviceAccountName: workloadmanager
      containers:
        - name: workloadmanager
          image: ${WM_IMAGE}
          imagePullPolicy: IfNotPresent
          args:
            - --port=8080
            - --runtime-class-name=
          env:
            - name: AGENTCUBE_NAMESPACE
              value: ${NAMESPACE}
            - name: REDIS_ADDR
              value: redis.${NAMESPACE}.svc.cluster.local:6379
            - name: REDIS_PASSWORD_REQUIRED
              value: "false"
          ports:
            - containerPort: 8080
          readinessProbe:
            httpGet:
              path: /health
              port: 8080
            initialDelaySeconds: 2
            periodSeconds: 2
---
apiVersion: v1
kind: Service
metadata:
  name: workloadmanager
spec:
  selector:
    app: workloadmanager
  ports:
    - port: 8080
      targetPort: 8080
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: agentcube-router
spec:
  replicas: 1
  selector:
    matchLabels:
      app: agentcube-router
  template:
    metadata:
      labels:
        app: agentcube-router
    spec:
      serviceAccountName: agentcube-router
      containers:
        - name: router
          image: ${ROUTER_IMAGE}
          imagePullPolicy: IfNotPresent
          args:
            - --port=8081
            - --enable-e2b=true
            - --e2b-domain=${E2B_DOMAIN}
            - --enable-tls=true
            - --tls-cert=/etc/e2b-tls/tls.crt
            - --tls-key=/etc/e2b-tls/tls.key
            - --debug=true
          env:
            - name: AGENTCUBE_NAMESPACE
              value: ${NAMESPACE}
            - name: E2B_DEFAULT_NAMESPACE
              value: ${NAMESPACE}
            - name: E2B_API_KEY_SECRET_NAMESPACE
              value: ${NAMESPACE}
            - name: WORKLOAD_MANAGER_URL
              value: http://workloadmanager.${NAMESPACE}.svc.cluster.local:8080
            - name: REDIS_ADDR
              value: redis.${NAMESPACE}.svc.cluster.local:6379
            - name: REDIS_PASSWORD_REQUIRED
              value: "false"
          ports:
            - containerPort: 8081
          volumeMounts:
            - name: tls
              mountPath: /etc/e2b-tls
              readOnly: true
      volumes:
        - name: tls
          secret:
            secretName: e2b-router-tls
---
apiVersion: v1
kind: Service
metadata:
  name: agentcube-router
spec:
  selector:
    app: agentcube-router
  ports:
    - port: 8081
      targetPort: 8081
EOF

# Wait for Router first so picod-router-identity Secret is guaranteed created, then wait on WorkloadManager.
kubectl -n "${NAMESPACE}" rollout status deployment/agentcube-router --timeout=180s
kubectl -n "${NAMESPACE}" rollout status deployment/workloadmanager --timeout=180s

step "Creating opt-in CodeInterpreter CRDs (WarmPool and Cold-start)"
kubectl -n "${NAMESPACE}" apply -f - <<EOF
apiVersion: runtime.agentcube.volcano.sh/v1alpha1
kind: CodeInterpreter
metadata:
  name: e2b-code-interpreter
  labels:
    e2b.agentcube.io/enabled: "true"
  annotations:
    e2b.agentcube.io/aliases: "code-interpreter-v1"
    e2b.agentcube.io/description: "E2B compatible Python code interpreter (warm pool)"
    e2b.agentcube.io/public: "true"
spec:
  authMode: none
  warmPoolSize: 1
  maxSessionDuration: 1h
  ports:
    - name: envd
      port: 49983
      protocol: HTTP
      pathPrefix: /
    - name: code-interpreter
      port: 49999
      protocol: HTTP
      pathPrefix: /
  template:
    image: ${CI_IMAGE}
    imagePullPolicy: IfNotPresent
---
apiVersion: runtime.agentcube.volcano.sh/v1alpha1
kind: CodeInterpreter
metadata:
  name: e2b-code-interpreter-cold
  labels:
    e2b.agentcube.io/enabled: "true"
  annotations:
    e2b.agentcube.io/description: "E2B compatible Python code interpreter (cold start)"
spec:
  authMode: none
  maxSessionDuration: 1h
  ports:
    - name: envd
      port: 49983
      protocol: HTTP
      pathPrefix: /
    - name: code-interpreter
      port: 49999
      protocol: HTTP
      pathPrefix: /
  template:
    image: ${CI_IMAGE}
    imagePullPolicy: IfNotPresent
EOF

step "Waiting for WarmPool pod to become ready"
for i in $(seq 1 60); do
    ready_replicas=$(kubectl -n "${NAMESPACE}" get sandboxwarmpool e2b-code-interpreter -o jsonpath='{.status.readyReplicas}' 2>/dev/null || echo "")
    if [ "${ready_replicas}" = "1" ]; then
        echo "WarmPool e2b-code-interpreter readyReplicas=1"
        break
    fi
    sleep 2
done
if [ "${ready_replicas:-}" != "1" ]; then
    kubectl -n "${NAMESPACE}" get sandboxwarmpool,pods -o wide
    echo "WarmPool did not become ready in time" >&2
    exit 1
fi

step "Starting port-forward to agentcube-router on port ${LOCAL_PORT}"
kubectl -n "${NAMESPACE}" port-forward --address 127.0.0.1,::1 svc/agentcube-router "${LOCAL_PORT}:8081" \
    >"${TMP_DIR}/pf.log" 2>&1 &
PF_PID=$!

for i in $(seq 1 30); do
    if curl -skf "https://127.0.0.1:${LOCAL_PORT}/health/ready" >/dev/null 2>&1; then
        break
    fi
    sleep 1
done

step "Setting up Python3 venv and installing official e2b + e2b-code-interpreter SDKs"
if [ ! -e "${E2E_VENV_DIR}" ]; then
    CREATED_VENV=true
fi
python3 -m venv "${E2E_VENV_DIR}"
"${E2E_VENV_DIR}/bin/pip" install --upgrade pip >/dev/null
"${E2E_VENV_DIR}/bin/pip" install e2b e2b-code-interpreter httpx >/dev/null

step "Running official E2B SDK E2E test suite"
export E2B_DOMAIN="${E2B_DOMAIN}"
export E2B_API_URL="https://api.${E2B_DOMAIN}"
export E2B_API_KEY="${API_KEY_A}"
export E2B_API_KEY_TENANT_B="${API_KEY_B}"
export E2B_TEST_NAMESPACE="${NAMESPACE}"
export SSL_CERT_FILE="${TMP_DIR}/tls.crt"
export NO_PROXY="localhost,.localhost,127.0.0.1,::1"
export no_proxy="${NO_PROXY}"

"${E2E_VENV_DIR}/bin/python" "${SCRIPT_DIR}/test_e2b.py"
