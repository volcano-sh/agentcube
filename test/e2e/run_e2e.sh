#!/bin/bash
set -euo pipefail
IFS=$'\n\t'

# Configuration
E2E_CLUSTER_NAME=${E2E_CLUSTER_NAME:-agentcube-e2e}
E2E_CLEAN_CLUSTER=${E2E_CLEAN_CLUSTER:-true}
E2E_SKIP_SETUP=${E2E_SKIP_SETUP:-false}
AGENTCUBE_NAMESPACE=${AGENTCUBE_NAMESPACE:-agentcube}
WORKLOAD_NAMESPACE=${WORKLOAD_NAMESPACE:-agentcube}

detect_mtls_enabled() {
    if [ -n "${MTLS_ENABLED+x}" ]; then
        return 0
    fi

    if kubectl get deployment workloadmanager -n "${AGENTCUBE_NAMESPACE}" >/dev/null 2>&1; then
        if kubectl -n "${AGENTCUBE_NAMESPACE}" get deployment workloadmanager \
            -o jsonpath='{.spec.template.spec.containers[?(@.name=="workloadmanager")].args[*]}' 2>/dev/null \
            | grep -q -- '--tls-cert='; then
            MTLS_ENABLED=true
        else
            MTLS_ENABLED=false
        fi
    elif [ "${E2E_SKIP_SETUP}" = "true" ]; then
        MTLS_ENABLED=false
    else
        MTLS_ENABLED=true
    fi
}

if [ -z "${MTLS_ENABLED+x}" ]; then
    detect_mtls_enabled
fi
AGENT_SANDBOX_VERSION=${AGENT_SANDBOX_VERSION:-v0.5.2}
E2E_REQUIRE_CODEINTERPRETER=${E2E_REQUIRE_CODEINTERPRETER:-false}
WORKLOAD_MANAGER_IMAGE=${WORKLOAD_MANAGER_IMAGE:-workloadmanager:latest}
ROUTER_IMAGE=${ROUTER_IMAGE:-agentcube-router:latest}
PICOD_IMAGE=${PICOD_IMAGE:-picod:latest}
REDIS_IMAGE=${REDIS_IMAGE:-redis:7-alpine}
E2E_VENV_DIR=${E2E_VENV_DIR:-/tmp/agentcube-e2e-venv}
MCP_K8S_LOCAL_PORT=${MCP_K8S_LOCAL_PORT:-19446}
KEYCLOAK_ENABLED=${KEYCLOAK_ENABLED:-false}
KEYCLOAK_IMAGE=${KEYCLOAK_IMAGE:-quay.io/keycloak/keycloak:26.0}

_SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" && pwd)"
REPO_ROOT="$(cd "$_SCRIPT_DIR/../.." && pwd)"

# Images that need to be pre-pulled and loaded into kind cluster
# Based on agent-sandbox manifest analysis, only these images are needed:
# - agent-sandbox-controller (used in both agentsandbox manifest.yaml and extensions.yaml)
# - python:3.9-slim (used by echo-agent)
PRE_PULL_IMAGES=(
    "registry.k8s.io/agent-sandbox/agent-sandbox-controller:${AGENT_SANDBOX_VERSION}"
    "python:3.9-slim"
)

WORKLOAD_MANAGER_LOCAL_PORT=${WORKLOAD_MANAGER_LOCAL_PORT:-8080}
ROUTER_LOCAL_PORT=${ROUTER_LOCAL_PORT:-8081}

# Artifacts path for collecting logs on test failure
ARTIFACTS_PATH=${ARTIFACTS_PATH:-"${PWD}/e2e-logs"}

# Function to clean up
cleanup() {
    echo "Cleaning up..."

    # Kill port-forward processes by PID
    if [ -n "${WORKLOAD_PID:-}" ]; then
        echo "Stopping workload manager port forward (PID: $WORKLOAD_PID)..."
        kill "$WORKLOAD_PID" 2>/dev/null || true
    fi
    if [ -n "${ROUTER_PID:-}" ]; then
        echo "Stopping router port forward (PID: $ROUTER_PID)..."
        kill "$ROUTER_PID" 2>/dev/null || true
    fi
    if [ -n "${MCP_K8S_PF_PID:-}" ]; then
        echo "Stopping MCP in-cluster port forward (PID: $MCP_K8S_PF_PID)..."
        kill "$MCP_K8S_PF_PID" 2>/dev/null || true
    fi
    if [ -n "${KEYCLOAK_PID:-}" ]; then
        echo "Stopping Keycloak port forward (PID: $KEYCLOAK_PID)..."
        kill "$KEYCLOAK_PID" 2>/dev/null || true
    fi

    # Best-effort: remove MCP Deployment so the next run starts clean
    kubectl delete deployment agentcube-code-interpreter-mcp -n "${AGENTCUBE_NAMESPACE:-agentcube}" --ignore-not-found=true 2>/dev/null || true

    # Kill any remaining kubectl port-forward processes
    echo "Killing any remaining kubectl port-forward processes..."
    pkill -f "kubectl port-forward" 2>/dev/null || true

    # Wait a moment for processes to terminate
    sleep 2

    # Force kill any remaining processes on our ports
    echo "Force killing any processes using ports 8080-8081 and MCP_K8S_LOCAL_PORT..."
    for port in 8080 8081 "${MCP_K8S_LOCAL_PORT:-19446}"; do
        # Try lsof first (most Linux systems)
        if command -v lsof >/dev/null 2>&1 && lsof -i :$port >/dev/null 2>&1; then
            echo "Port $port is still in use, force killing with lsof..."
            lsof -ti :$port | xargs kill -9 2>/dev/null || true
        # Fallback to netstat if lsof not available
        elif command -v netstat >/dev/null 2>&1 && netstat -tulpn 2>/dev/null | grep ":$port " >/dev/null; then
            echo "Port $port is still in use, force killing with netstat..."
            netstat -tulpn 2>/dev/null | grep ":$port " | awk '{print $7}' | cut -d'/' -f1 | xargs kill -9 2>/dev/null || true
        fi
    done

    # Clean up virtual environment
    if [ -d "${E2E_VENV_DIR:-}" ]; then
        echo "Removing Python virtual environment..."
        rm -rf "$E2E_VENV_DIR" || true
    fi

    # Clean up temp files
    rm -f /tmp/workload_port_forward.log /tmp/router_port_forward.log 2>/dev/null || true
}

# Register cleanup on exit
trap cleanup EXIT

require_cmd() {
    command -v "$1" >/dev/null 2>&1 || {
        echo "Missing required command: $1" >&2
        exit 1
    }
}

require_python() {
    # Check if agentcube package is available in the virtual environment
    "$E2E_VENV_DIR/bin/python" -c "import agentcube" 2>/dev/null || {
        echo "Python package 'agentcube' not found in virtual environment. Please ensure sdk-python is properly installed." >&2
        exit 1
    }
}

apply_workload_fixture() {
    local source=$1
    local rendered
    rendered=$(mktemp)
    sed -E "s/^([[:space:]]*)namespace:[[:space:]]*.*/\1namespace: ${WORKLOAD_NAMESPACE}/" "$source" > "$rendered"
    if ! kubectl apply --validate=false -f "$rendered"; then
        rm -f "$rendered"
        return 1
    fi
    rm -f "$rendered"
}

tcp_port_open() {
    local port=$1
    : 2>/dev/null </dev/tcp/127.0.0.1/"${port}"
}

step() {
    echo
    echo "==> $1"
}

# Helper function to collect logs for pods by label selector
# Note: script uses IFS=$'\n\t', so jsonpath space-separated names must be split explicitly
collect_pod_logs() {
    local label_selector=$1
    local component_name=$2
    local artifacts_dir=$3

    echo "Collecting ${component_name} logs..."
    local pods=$(kubectl -n "${AGENTCUBE_NAMESPACE}" get pods -l "${label_selector}" \
        -o jsonpath='{.items[*].metadata.name}' 2>/dev/null || echo "")

    if [ -n "$pods" ]; then
        for pod in $(echo "$pods" | tr ' ' '\n' | grep -v '^$'); do
            echo "  Collecting logs from pod: $pod"
            kubectl -n "${AGENTCUBE_NAMESPACE}" logs "$pod" --all-containers=true \
                > "${artifacts_dir}/${component_name}-${pod}.log" 2>&1 || true
            kubectl -n "${AGENTCUBE_NAMESPACE}" describe pod "$pod" \
                > "${artifacts_dir}/${component_name}-${pod}-describe.txt" 2>&1 || true
        done
    else
        echo "  No ${component_name} pods found with label selector: ${label_selector}"
        # List all pods for debugging
        kubectl -n "${AGENTCUBE_NAMESPACE}" get pods -o wide > "${artifacts_dir}/${component_name}-all-pods.txt" 2>&1 || true
    fi
}

# Function to collect logs from all E2E test components
collect_component_logs() {
    local artifacts_dir="${ARTIFACTS_PATH}"
    echo "Collecting component logs to ${artifacts_dir}..."
    mkdir -p "${artifacts_dir}"

    # 1. Collect workloadmanager logs
    collect_pod_logs "app=workloadmanager" "workloadmanager" "${artifacts_dir}"
    
    # 2. Collect router logs
    collect_pod_logs "app=agentcube-router" "router" "${artifacts_dir}"

    # 2b. MCP server (in-cluster E2E)
    collect_pod_logs "app=agentcube-code-interpreter-mcp" "code-interpreter-mcp" "${artifacts_dir}"
    
    # 3. Collect Sandbox Pods logs (per-container: picod, user agent containers, etc.)
    echo "Collecting sandbox pods logs (picod/user containers per container)..."
    local sandbox_pods=$(kubectl -n "${AGENTCUBE_NAMESPACE}" get pods \
        -l runtime.agentcube.io/sandbox-name \
        -o jsonpath='{.items[*].metadata.name}' 2>/dev/null || echo "")
    for pod in $(echo "$sandbox_pods" | tr ' ' '\n' | grep -v '^$'); do
        kubectl -n "${AGENTCUBE_NAMESPACE}" describe pod "$pod" \
            > "${artifacts_dir}/sandbox-${pod}-describe.txt" 2>&1 || true
        local containers=$(kubectl -n "${AGENTCUBE_NAMESPACE}" get pod "$pod" \
            -o jsonpath='{.spec.containers[*].name}' 2>/dev/null || echo "")
        for c in $(echo "$containers" | tr ' ' '\n' | grep -v '^$'); do
            kubectl -n "${AGENTCUBE_NAMESPACE}" logs "$pod" -c "$c" --tail=10000 \
                > "${artifacts_dir}/sandbox-${pod}-${c}.log" 2>&1 || true
            [ -s "${artifacts_dir}/sandbox-${pod}-${c}.log" ] || \
                kubectl -n "${AGENTCUBE_NAMESPACE}" logs "$pod" -c "$c" --previous --tail=10000 \
                    > "${artifacts_dir}/sandbox-${pod}-${c}.log" 2>/dev/null || true
        done
    done

    echo "Component logs collected to ${artifacts_dir}"
    ls -lah "${artifacts_dir}" || true
}

pre_pull_images() {
    echo "Pre-pulling required images..."

    for image in "${PRE_PULL_IMAGES[@]}"; do
        echo "Pulling image: ${image}"
        if ! docker pull "${image}"; then
            echo "Warning: Failed to pull ${image}, will continue without it"
        fi
    done
}

ensure_kind_cluster() {
    step "Kind cluster: ${E2E_CLUSTER_NAME}"

    if kind get clusters | grep -q "^${E2E_CLUSTER_NAME}$"; then
        if [ "${E2E_CLEAN_CLUSTER}" = "true" ]; then
            echo "Kind cluster '${E2E_CLUSTER_NAME}' already exists, deleting it for a clean E2E run..."
            kind delete cluster --name "${E2E_CLUSTER_NAME}" || true
            echo "Recreating Kind cluster '${E2E_CLUSTER_NAME}'..."
            kind create cluster --name "${E2E_CLUSTER_NAME}"
        else
            echo "Kind cluster '${E2E_CLUSTER_NAME}' already exists, skipping deletion/creation (E2E_CLEAN_CLUSTER=false)..."
        fi
    else
        echo "Creating Kind cluster '${E2E_CLUSTER_NAME}'..."
        kind create cluster --name "${E2E_CLUSTER_NAME}"
    fi

    echo "Kind cluster created successfully"
}

ensure_namespace() {
    local ns="$1"
    kubectl get ns "${ns}" >/dev/null 2>&1 || kubectl create ns "${ns}"
}

docker_pull_if_missing() {
    local image="$1"
    if docker image inspect "${image}" > /dev/null 2>&1; then
        echo "Image '${image}' already exists locally, skipping pull..."
    else
        echo "Pulling image '${image}'..."
        docker pull "${image}"
    fi
}

kind_load_image() {
    local image="$1"
    # Note: Docker Desktop 29.x with containerd image store can fail to load multi-platform
    # images. We allow failures here and let Kind nodes pull from registry instead.
    if ! kind load docker-image "${image}" --name "${E2E_CLUSTER_NAME}"; then
        echo "Warning: Failed to load image ${image} into Kind. Will attempt to pull from registry." >&2
        return 0
    fi
}

curl_download() {
    local url="$1"
    local out="$2"
    # Retry a few times to reduce flakiness in CI/WSL networks.
    curl -fsSL --retry 5 --retry-delay 2 --retry-all-errors "${url}" -o "${out}"
}

kubectl_apply_url() {
    local url="$1"
    local tmp
    tmp="$(mktemp)"
    echo "Downloading: ${url}"
    curl_download "${url}" "${tmp}"
    kubectl apply --validate=false -f "${tmp}"
    rm -f "${tmp}"
}

verify_agent_sandbox_controller() {
    local expected_image="registry.k8s.io/agent-sandbox/agent-sandbox-controller:${AGENT_SANDBOX_VERSION}"
    local actual_image

    kubectl -n agent-sandbox-system rollout status deployment/agent-sandbox-controller --timeout=300s
    actual_image=$(kubectl -n agent-sandbox-system get deployment agent-sandbox-controller \
        -o jsonpath='{.spec.template.spec.containers[?(@.name=="agent-sandbox-controller")].image}')
    if [ "${actual_image}" != "${expected_image}" ]; then
        echo "agent-sandbox controller version mismatch: expected ${expected_image}, got ${actual_image}" >&2
        exit 1
    fi
    echo "Verified agent-sandbox controller image: ${actual_image}"
}

deploy_redis() {
    step "Deploying Redis (${REDIS_IMAGE})"
    ensure_namespace "${AGENTCUBE_NAMESPACE}"

    # Ensure redis image is available to kind nodes (avoid node pull/proxy issues).
    docker_pull_if_missing "${REDIS_IMAGE}"
    kind_load_image "${REDIS_IMAGE}"

    # Use a simple Deployment+Service for idempotency.
    kubectl -n "${AGENTCUBE_NAMESPACE}" create deployment redis \
        --image="${REDIS_IMAGE}" \
        --port=6379 \
        --dry-run=client -o yaml | kubectl apply --validate=false -f -

    kubectl -n "${AGENTCUBE_NAMESPACE}" expose deployment redis \
        --port=6379 \
        --target-port=6379 \
        --name=redis \
        --dry-run=client -o yaml | kubectl apply --validate=false -f -

    kubectl -n "${AGENTCUBE_NAMESPACE}" rollout status deployment/redis --timeout=180s
}

run_setup() {
    require_cmd kind
    require_cmd kubectl
    require_cmd docker
    require_cmd curl
    require_cmd helm

    ensure_kind_cluster

    step "Pre-pulling required images..."
    pre_pull_images

    step "Loading pre-pulled images into Kind..."
    for image in "${PRE_PULL_IMAGES[@]}"; do
        echo "Loading image into kind: ${image}"
        kind load docker-image "${image}" --name "${E2E_CLUSTER_NAME}" || echo "Warning: Failed to load ${image}"
    done

    step "Installing agent-sandbox (${AGENT_SANDBOX_VERSION})..."
    # Download then apply to avoid URL parsing issues / improve debuggability.
    kubectl_apply_url "https://github.com/kubernetes-sigs/agent-sandbox/releases/download/${AGENT_SANDBOX_VERSION}/manifest.yaml"
    kubectl_apply_url "https://github.com/kubernetes-sigs/agent-sandbox/releases/download/${AGENT_SANDBOX_VERSION}/extensions.yaml"
    verify_agent_sandbox_controller

    step "Building images..."
    # Change to project root for make commands
    cd "$REPO_ROOT"
    make docker-build
    make docker-build-router
    make docker-build-picod

    step "Loading images into Kind..."
    kind load docker-image "${WORKLOAD_MANAGER_IMAGE}" --name "${E2E_CLUSTER_NAME}"
    kind load docker-image "${ROUTER_IMAGE}" --name "${E2E_CLUSTER_NAME}"
    kind load docker-image "${PICOD_IMAGE}" --name "${E2E_CLUSTER_NAME}"

    deploy_redis

    # Wait for Redis to be fully ready before deploying dependent services
    step "Waiting for Redis to be ready..."
    kubectl -n "${AGENTCUBE_NAMESPACE}" rollout status deployment/redis --timeout=120s

    # Additional Redis readiness check - ensure Redis is actually responding
    REDIS_READY=false
    for i in {1..30}; do
        if kubectl exec -n "${AGENTCUBE_NAMESPACE}" deployment/redis -- redis-cli ping 2>/dev/null | grep -q "PONG"; then
            echo "Redis is responding to ping"
            REDIS_READY=true
            break
        fi
        echo "Waiting for Redis to be ready (attempt $i/30)..."
        sleep 2
    done

    if [ "$REDIS_READY" != "true" ]; then
        echo "Redis failed to become ready"
        exit 1
    fi

    step "Deploying AgentCube via Helm (using native parameters)..."
    # Prepare extra environment variables as JSON for Helm
    WM_EXTRA_ENV=$(printf '[{"name":"REDIS_PASSWORD_REQUIRED","value":"false"},{"name":"JWT_KEY_SECRET_NAMESPACE","value":"%s"}]' "${AGENTCUBE_NAMESPACE}")
    ROUTER_EXTRA_ENV='[{"name":"REDIS_PASSWORD_REQUIRED","value":"false"}]'

    if [ "${MTLS_ENABLED}" = "true" ]; then
        # Install SPIRE CRDs before installing the chart with spire.enabled=true.
        step "Installing SPIRE CRDs..."
        kubectl apply -k "https://github.com/spiffe/spire-controller-manager/config/crd?ref=v0.6.4"
    fi

    # Install using Helm directly from the source chart
    # We use --set-json to pass the extra environment variables and enable RBAC/SA for the router
    helm upgrade --install agentcube manifests/charts/base \
        --namespace "${AGENTCUBE_NAMESPACE}" \
        --create-namespace \
        --set redis.addr="redis.${AGENTCUBE_NAMESPACE}.svc.cluster.local:6379" \
        --set redis.password="" \
        --set workloadmanager.image.repository="workloadmanager" \
        --set workloadmanager.image.tag="latest" \
        --set-json "workloadmanager.extraEnv=${WM_EXTRA_ENV}" \
        --set router.image.repository="agentcube-router" \
        --set router.image.tag="latest" \
        --set router.rbac.create=true \
        --set router.serviceAccountName="agentcube-router" \
        --set-json "router.extraEnv=${ROUTER_EXTRA_ENV}" \
        --set spire.enabled="${MTLS_ENABLED}" \
        --set spire.agent.insecureBootstrap=true \
        --set spire.agent.skipKubeletVerification=true \
        --wait --timeout=10m

    if [ "${MTLS_ENABLED}" = "true" ]; then
        step "Waiting for SPIRE infrastructure..."
        kubectl -n "${AGENTCUBE_NAMESPACE}" rollout status statefulset/spire-server --timeout=300s
        kubectl -n "${AGENTCUBE_NAMESPACE}" rollout status daemonset/spire-agent --timeout=300s
    fi

    step "Waiting for deployments..."
    kubectl -n "${AGENTCUBE_NAMESPACE}" rollout status deployment/workloadmanager --timeout=300s
    kubectl -n "${AGENTCUBE_NAMESPACE}" rollout status deployment/agentcube-router --timeout=300s

    step "Creating ServiceAccount and Token..."
    kubectl create serviceaccount e2e-test -n "${AGENTCUBE_NAMESPACE}" || true
    kubectl create clusterrolebinding e2e-test-binding --clusterrole=workloadmanager --serviceaccount="${AGENTCUBE_NAMESPACE}:e2e-test" || true

    step "Creating test AgentRuntimes..."
    kubectl create namespace "${WORKLOAD_NAMESPACE}" --dry-run=client -o yaml | kubectl apply -f -
    # Create normal echo-agent
    apply_workload_fixture test/e2e/echo_agent.yaml
    # Create echo-agent-short-ttl with short sessionTimeout for TTL testing
    tmp_ttl_agent=$(mktemp)
    sed 's/name: echo-agent/name: echo-agent-short-ttl/; s/app: echo-agent/app: echo-agent-short-ttl/; s/sessionTimeout: "15m"/sessionTimeout: "30s"/' test/e2e/echo_agent.yaml > "$tmp_ttl_agent"
    apply_workload_fixture "$tmp_ttl_agent"
    rm -f "$tmp_ttl_agent"

    step "Creating test CodeInterpreter..."
    # Create e2e-code-interpreter CodeInterpreter
    apply_workload_fixture test/e2e/e2e_code_interpreter.yaml

    step "Waiting for AgentRuntimes to be ready..."
    kubectl get agentruntime echo-agent -n "${WORKLOAD_NAMESPACE}" -o jsonpath='{.metadata.name}{"\n"}' || echo "echo-agent may still be starting..."
    kubectl get agentruntime echo-agent-short-ttl -n "${WORKLOAD_NAMESPACE}" -o jsonpath='{.metadata.name}{"\n"}' || echo "echo-agent-short-ttl may still be starting..."
    echo "AgentRuntimes created, waiting for pods to be ready..."
    sleep 10

    # Deploy Keycloak when enabled
    if [ "${KEYCLOAK_ENABLED}" = "true" ]; then
        step "Deploying Keycloak addon..."
        docker_pull_if_missing "${KEYCLOAK_IMAGE}"
        kind_load_image "${KEYCLOAK_IMAGE}"

        helm upgrade --install keycloak manifests/charts/addons/keycloak \
            --namespace "${AGENTCUBE_NAMESPACE}" \
            --set admin.username=admin --set admin.password=admin \
            --set clients.app.secret=e2e-app-secret \
            --set clients.router.secret=e2e-router-secret \
            --set clients.admin.secret=e2e-admin-secret \
            --wait --timeout=5m

        step "Waiting for Keycloak to be ready..."
        kubectl -n "${AGENTCUBE_NAMESPACE}" rollout status deployment/keycloak --timeout=300s

        # Configure OIDC Helm args for Router
        OIDC_HELM_ARGS=(
            --set "router.jwt.issuerUrl=http://keycloak.${AGENTCUBE_NAMESPACE}.svc.cluster.local:8080/realms/agentcube"
            --set "router.jwt.roleClaim=realm_access.roles"
            --set "router.jwt.requiredRole=sandbox:invoke"
        )

        # Reconfigure Router with OIDC flags
        step "Reconfiguring Router with OIDC flags..."
        helm upgrade agentcube manifests/charts/base \
            --namespace "${AGENTCUBE_NAMESPACE}" \
            --reuse-values \
            "${OIDC_HELM_ARGS[@]}" \
            --wait --timeout=5m

        kubectl -n "${AGENTCUBE_NAMESPACE}" rollout status deployment/agentcube-router --timeout=300s
    fi
}

echo "Starting E2E tests..."

if [ "${E2E_SKIP_SETUP}" = "true" ]; then
    if kubectl get svc workloadmanager -n "${AGENTCUBE_NAMESPACE}" >/dev/null 2>&1 && \
       kubectl get svc agentcube-router -n "${AGENTCUBE_NAMESPACE}" >/dev/null 2>&1; then
        echo "Skipping setup phase (E2E_SKIP_SETUP=true)"
        echo "Assuming cluster '${E2E_CLUSTER_NAME}' is already running with deployed services..."
        echo "Using namespace: ${AGENTCUBE_NAMESPACE}"
    else
        echo "Skipping setup was requested, but required services are missing; provisioning them now..."
        run_setup
    fi
else
    run_setup
fi

step "Pre-cleanup"
# Clean up any leftover processes before starting
echo "Performing pre-run cleanup..."
pkill -f "kubectl port-forward" 2>/dev/null || true
for port in 8080 8081 "${MCP_K8S_LOCAL_PORT:-19446}" 19245; do
    if command -v lsof > /dev/null 2>&1 && lsof -i :$port > /dev/null 2>&1; then
        lsof -ti :$port | xargs kill -9 2>/dev/null || true
    elif command -v netstat > /dev/null 2>&1 && netstat -tulpn 2>/dev/null | grep ":$port " > /dev/null; then
        netstat -tulpn 2>/dev/null | grep ":$port " | awk '{print $7}' | cut -d'/' -f1 | xargs kill -9 2>/dev/null || true
    fi
done
rm -f /tmp/workload_port_forward.log /tmp/router_port_forward.log 2>/dev/null || true
sleep 2

step "Preparing test namespaces and service account..."
ensure_namespace "${AGENTCUBE_NAMESPACE}"
ensure_namespace "${WORKLOAD_NAMESPACE}"
kubectl create serviceaccount e2e-test -n "${AGENTCUBE_NAMESPACE}" --dry-run=client -o yaml | kubectl apply -f -
kubectl create clusterrolebinding e2e-test-binding --clusterrole=workloadmanager --serviceaccount="${AGENTCUBE_NAMESPACE}:e2e-test" --dry-run=client -o yaml | kubectl apply -f -

step "Running tests..."
# Create token
API_TOKEN=$(kubectl create token e2e-test -n "${AGENTCUBE_NAMESPACE}" --duration=24h)
echo "Token created"

# Obtain Keycloak tokens when OIDC is enabled
if [ "${KEYCLOAK_ENABLED}" = "true" ]; then
    step "Obtaining Keycloak access tokens..."
    kubectl port-forward svc/keycloak -n "${AGENTCUBE_NAMESPACE}" 8082:8080 > /tmp/keycloak_port_forward.log 2>&1 &
    KEYCLOAK_PID=$!
    sleep 3

    KEYCLOAK_TOKEN=$(curl -s -X POST \
        -H "Host: keycloak.${AGENTCUBE_NAMESPACE}.svc.cluster.local:8080" \
        "http://localhost:8082/realms/agentcube/protocol/openid-connect/token" \
        -d "grant_type=client_credentials" \
        -d "client_id=agentcube-app" \
        -d "client_secret=e2e-app-secret" | jq -r '.access_token')

    ADMIN_TOKEN=$(curl -s -X POST \
        -H "Host: keycloak.${AGENTCUBE_NAMESPACE}.svc.cluster.local:8080" \
        "http://localhost:8082/realms/agentcube/protocol/openid-connect/token" \
        -d "grant_type=client_credentials" \
        -d "client_id=agentcube-admin" \
        -d "client_secret=e2e-admin-secret" | jq -r '.access_token')

    # Override the K8s SA token with the Keycloak token
    export API_TOKEN="${KEYCLOAK_TOKEN}"
    export ADMIN_TOKEN="${ADMIN_TOKEN}"
    export OIDC_ENABLED="true"
    export KEYCLOAK_TOKEN_URL="http://localhost:8082/realms/agentcube/protocol/openid-connect/token"
    echo "Keycloak tokens acquired"
fi

# Port forward workload manager in background
echo "Starting workload manager port-forward..."
kubectl port-forward svc/workloadmanager -n "${AGENTCUBE_NAMESPACE}" "${WORKLOAD_MANAGER_LOCAL_PORT}:8080" > /tmp/workload_port_forward.log 2>&1 &
WORKLOAD_PID=$!
sleep 1
if ! kill -0 $WORKLOAD_PID 2>/dev/null; then
    echo "Failed to start workload manager port-forward. Check /tmp/workload_port_forward.log"
    cat /tmp/workload_port_forward.log
    exit 1
fi
echo "Workload manager port forward started with PID $WORKLOAD_PID"

# Port forward router in background
echo "Starting router port-forward..."
kubectl port-forward svc/agentcube-router -n "${AGENTCUBE_NAMESPACE}" "${ROUTER_LOCAL_PORT}:8080" > /tmp/router_port_forward.log 2>&1 &
ROUTER_PID=$!
sleep 1
if ! kill -0 $ROUTER_PID 2>/dev/null; then
    echo "Failed to start router port-forward. Check /tmp/router_port_forward.log"
    cat /tmp/router_port_forward.log
    exit 1
fi
echo "Router port forward started with PID $ROUTER_PID"

# Wait for port-forwards to be ready
echo "Waiting for port-forwards..."
for i in $(seq 1 30); do
    # WorkloadManager uses mTLS, so an unauthenticated HTTP health check cannot complete.
    # Router exposes a non-mTLS health endpoint and should be verified at HTTP level.
    wm_ok=false
    router_ok=false
    if [ "${MTLS_ENABLED}" = "true" ]; then
        tcp_port_open "${WORKLOAD_MANAGER_LOCAL_PORT}" && wm_ok=true
    else
        curl -fsS "http://localhost:${WORKLOAD_MANAGER_LOCAL_PORT}/health" >/dev/null 2>&1 && wm_ok=true
    fi
    curl -fsS "http://localhost:${ROUTER_LOCAL_PORT}/health/live" >/dev/null 2>&1 && router_ok=true
    if $wm_ok && $router_ok; then
        echo "Port-forwards are ready."
        break
    fi
    if [ $i -eq 30 ]; then
        echo "Timed out waiting for port-forwards (wm_ready=$wm_ok router_ready=$router_ok)." >&2
        cat /tmp/workload_port_forward.log
        cat /tmp/router_port_forward.log
        exit 1
    fi
    sleep 2
done

# Setup Python virtual environment for testing
if [ ! -d "$E2E_VENV_DIR" ]; then
    echo "Creating Python virtual environment..."
    python3 -m venv "$E2E_VENV_DIR"
fi

echo "Activating virtual environment and installing dependencies..."
source "$E2E_VENV_DIR/bin/activate"
pip install --upgrade pip

# Install agentcube SDK in development mode
# Use absolute paths to support running from any directory
pip install -e "$REPO_ROOT/sdk-python"
pip install -e "$REPO_ROOT/integrations/code-interpreter-mcp"
pip install -e "$REPO_ROOT/integrations/langchain-agentcube"

# Check if agentcube package is available after installation
require_python

# Run tests with error handling to collect logs on failure
TEST_FAILED=0

echo "Running Go tests..."
# When SPIRE/mTLS is active, direct-WM tests skip because the test client has no client cert.
cd "$REPO_ROOT"
if ! WORKLOAD_MANAGER_URL="http://localhost:${WORKLOAD_MANAGER_LOCAL_PORT}" \
   ROUTER_URL="http://localhost:${ROUTER_LOCAL_PORT}" \
   MTLS_ENABLED="${MTLS_ENABLED}" \
   WORKLOAD_NAMESPACE="${WORKLOAD_NAMESPACE}" \
   OIDC_ENABLED="${OIDC_ENABLED:-false}" \
   ADMIN_TOKEN="${ADMIN_TOKEN:-}" \
   API_TOKEN=$API_TOKEN \
   go test -v ./test/e2e/...; then
    TEST_FAILED=1
fi

echo "Running Python CodeInterpreter tests..."
cd "$_SCRIPT_DIR"

if ! WORKLOAD_MANAGER_URL="http://localhost:${WORKLOAD_MANAGER_LOCAL_PORT}" \
   ROUTER_URL="http://localhost:${ROUTER_LOCAL_PORT}" \
   MTLS_ENABLED="${MTLS_ENABLED}" \
   OIDC_ENABLED="${OIDC_ENABLED:-false}" \
   KEYCLOAK_TOKEN_URL="${KEYCLOAK_TOKEN_URL:-}" \
   API_TOKEN=$API_TOKEN \
   AGENTCUBE_NAMESPACE="${WORKLOAD_NAMESPACE}" \
   "$E2E_VENV_DIR/bin/python" test_codeinterpreter.py; then
    echo "ERROR: Python CodeInterpreter tests failed with exit code $?"
    TEST_FAILED=1
else
    echo "✓ Python CodeInterpreter tests passed"
fi

if [ "${KEYCLOAK_ENABLED}" = "true" ]; then
    echo "Running Python OIDC auth tests..."
    if ! WORKLOAD_MANAGER_URL="http://localhost:${WORKLOAD_MANAGER_LOCAL_PORT}" \
       ROUTER_URL="http://localhost:${ROUTER_LOCAL_PORT}" \
       OIDC_ENABLED="true" \
       KEYCLOAK_TOKEN_URL="${KEYCLOAK_TOKEN_URL}" \
       AGENTCUBE_SYSTEM_NAMESPACE="${AGENTCUBE_NAMESPACE}" \
       API_TOKEN=$API_TOKEN \
       AGENTCUBE_NAMESPACE="${WORKLOAD_NAMESPACE}" \
       "$E2E_VENV_DIR/bin/python" test_oidc_auth.py; then
        TEST_FAILED=1
    fi
fi

echo "Running LangChain AgentcubeSandbox E2E..."
WORKLOAD_MANAGER_URL="http://localhost:${WORKLOAD_MANAGER_LOCAL_PORT}" ROUTER_URL="http://localhost:${ROUTER_LOCAL_PORT}" MTLS_ENABLED="${MTLS_ENABLED}" API_TOKEN=$API_TOKEN AGENTCUBE_NAMESPACE="${AGENTCUBE_NAMESPACE}" "$E2E_VENV_DIR/bin/python" test_langchain_agentcube_sandbox.py
EXIT_CODE=$?

if [ $EXIT_CODE -ne 0 ]; then
    echo "ERROR: LangChain tests failed with exit code $EXIT_CODE"
    TEST_FAILED=1
else
    echo "✓ LangChain tests passed"
fi

echo "Running Python Code Interpreter MCP tests (streamable-http, local subprocess)..."
if ! WORKLOAD_MANAGER_URL="http://localhost:${WORKLOAD_MANAGER_LOCAL_PORT}" ROUTER_URL="http://localhost:${ROUTER_LOCAL_PORT}" MTLS_ENABLED="${MTLS_ENABLED}" API_TOKEN=$API_TOKEN AGENTCUBE_NAMESPACE="${WORKLOAD_NAMESPACE}" "$E2E_VENV_DIR/bin/python" test_mcp_code_interpreter.py; then
    echo "ERROR: MCP code-interpreter tests failed with exit code $?"
    TEST_FAILED=1
else
    echo "✓ MCP code-interpreter tests passed"
fi

echo "Running Python Code Interpreter MCP stdio tests..."
if ! WORKLOAD_MANAGER_URL="http://localhost:${WORKLOAD_MANAGER_LOCAL_PORT}" ROUTER_URL="http://localhost:${ROUTER_LOCAL_PORT}" MTLS_ENABLED="${MTLS_ENABLED}" API_TOKEN=$API_TOKEN AGENTCUBE_NAMESPACE="${WORKLOAD_NAMESPACE}" "$E2E_VENV_DIR/bin/python" test_mcp_code_interpreter_stdio.py; then
    echo "ERROR: MCP stdio tests failed with exit code $?"
    TEST_FAILED=1
else
    echo "✓ MCP stdio tests passed"
fi

if [ "${E2E_SKIP_SETUP}" = "true" ]; then
    echo "Skipping MCP in-cluster Deployment E2E (E2E_SKIP_SETUP=true — image may not be loaded in Kind)."
else
    step "Building and deploying MCP server into Kind for in-cluster E2E..."
    cd "$REPO_ROOT"
    docker build -f integrations/code-interpreter-mcp/Dockerfile -t agentcube-code-interpreter-mcp:latest .
    kind load docker-image agentcube-code-interpreter-mcp:latest --name "${E2E_CLUSTER_NAME}"
    # The deployment manifest hardcodes namespace "agentcube". Render it so the
    # pod and service URLs target AGENTCUBE_NAMESPACE (system), while the
    # AGENTCUBE_NAMESPACE env var inside the pod points to WORKLOAD_NAMESPACE
    # (where CodeInterpreters are deployed).
    mcp_rendered=""
    mcp_rendered="$(mktemp)"
    sed -e "s|namespace: agentcube|namespace: ${AGENTCUBE_NAMESPACE}|g" \
        -e "s|\.agentcube\.svc\.cluster\.local|.${AGENTCUBE_NAMESPACE}.svc.cluster.local|g" \
        -e "s|value: \"agentcube\"|value: \"${WORKLOAD_NAMESPACE}\"|g" \
        integrations/code-interpreter-mcp/deployment.yaml > "${mcp_rendered}"
    kubectl apply --validate=false -f "${mcp_rendered}"
    rm -f "${mcp_rendered}"
    kubectl -n "${AGENTCUBE_NAMESPACE}" rollout status deployment/agentcube-code-interpreter-mcp --timeout=300s
    kubectl -n "${AGENTCUBE_NAMESPACE}" set env deployment/agentcube-code-interpreter-mcp "API_TOKEN=${API_TOKEN}"
    kubectl -n "${AGENTCUBE_NAMESPACE}" rollout status deployment/agentcube-code-interpreter-mcp --timeout=300s

    echo "Starting MCP in-cluster port-forward (localhost:${MCP_K8S_LOCAL_PORT} -> svc/agentcube-code-interpreter-mcp)..."
    kubectl port-forward -n "${AGENTCUBE_NAMESPACE}" "svc/agentcube-code-interpreter-mcp" "${MCP_K8S_LOCAL_PORT}:8000" >/tmp/mcp_k8s_port_forward.log 2>&1 &
    MCP_K8S_PF_PID=$!
    sleep 2
    if ! kill -0 "$MCP_K8S_PF_PID" 2>/dev/null; then
        echo "Failed to start MCP port-forward. Check /tmp/mcp_k8s_port_forward.log" >&2
        cat /tmp/mcp_k8s_port_forward.log >&2 || true
        TEST_FAILED=1
    else
        echo "Running Python Code Interpreter MCP in-cluster (K8s Pod) tests..."
        cd "$_SCRIPT_DIR"
        export MCP_K8S_MCP_URL="http://127.0.0.1:${MCP_K8S_LOCAL_PORT}/mcp"
        if ! MTLS_ENABLED="${MTLS_ENABLED}" "$E2E_VENV_DIR/bin/python" test_mcp_code_interpreter_k8s.py; then
            echo "ERROR: MCP K8s tests failed with exit code $?"
            TEST_FAILED=1
        else
            echo "✓ MCP K8s tests passed"
        fi
    fi
fi

# Agent-Sandbox Upgrade Test (Advanced / Optional)
# Note: Upgrade test is an exploratory advanced scenario. If it fails, core E2E tests may still pass.
# Core functionality validation (invocations, TTL, port-forwarding) takes precedence.
if [ $TEST_FAILED -eq 0 ] && [ "${E2E_RUN_AGENT_SANDBOX_UPGRADE_TEST:-false}" = "true" ]; then
    cd "$REPO_ROOT"
    echo "Running Agent-Sandbox Upgrade Test (v0.4.6 -> v0.5.2, optional/exploratory)..."
    require_cmd jq
    require_cmd curl
    UPGRADE_FAILED=0
    NEW_VERSION="v0.5.2"
    MIGRATE_TMP="$(mktemp)"

    echo "Downloading agent-sandbox migration helper for ${NEW_VERSION}..."
    MIGRATE_DOWNLOADED=0
    MIGRATE_URL="https://raw.githubusercontent.com/kubernetes-sigs/agent-sandbox/${NEW_VERSION}/dev/tools/migrate.sh"

    if curl_download "${MIGRATE_URL}" "${MIGRATE_TMP}"; then
        MIGRATE_DOWNLOADED=1
    fi

    if [ $MIGRATE_DOWNLOADED -eq 0 ]; then
        echo "Error: failed to download migrate.sh"
        UPGRADE_FAILED=1
    fi

    if [ $UPGRADE_FAILED -eq 0 ]; then
        echo "Pre-pulling v0.5.2 agent-sandbox controller image to avoid slow/failed pulls during upgrade..."
        UPGRADE_CONTROLLER_IMAGE="registry.k8s.io/agent-sandbox/agent-sandbox-controller:${NEW_VERSION}"
        if ! docker pull "${UPGRADE_CONTROLLER_IMAGE}"; then
            echo "Warning: Failed to pre-pull ${UPGRADE_CONTROLLER_IMAGE}; upgrade may fall back to registry pull (could be slow/fail)"
        else
            echo "Pre-loading v0.5.2 controller image into Kind..."
            if ! kind load docker-image "${UPGRADE_CONTROLLER_IMAGE}" --name "${E2E_CLUSTER_NAME}"; then
                echo "Warning: Failed to load ${UPGRADE_CONTROLLER_IMAGE} into Kind; nodes will attempt registry pull"
            fi
        fi
    fi

    if [ $UPGRADE_FAILED -eq 0 ]; then
        chmod +x "${MIGRATE_TMP}"
        echo "1. Running pre-upgrade bootstrap..."
        if ! "${MIGRATE_TMP}" --phase=bootstrap; then
            echo "Error: migrate.sh bootstrap phase failed."
            UPGRADE_FAILED=1
        fi
    fi

    # Ensure idempotency if a previous run left resources behind.
    kubectl delete codeinterpreter upgrade-ci-1 upgrade-ci-2 -n "${WORKLOAD_NAMESPACE}" --ignore-not-found=true || true
    if [ $UPGRADE_FAILED -eq 0 ]; then
        echo "2. Creating upgrade-ci-1 CodeInterpreter with warm pool under v0.4.6..."
        cat <<EOF | kubectl apply -n "${WORKLOAD_NAMESPACE}" -f - || UPGRADE_FAILED=1
apiVersion: runtime.agentcube.volcano.sh/v1alpha1
kind: CodeInterpreter
metadata:
  name: upgrade-ci-1
spec:
  warmPoolSize: 1
  ports:
    - pathPrefix: "/"
      port: 8080
      protocol: "HTTP"
  template:
    image: picod:latest
    imagePullPolicy: IfNotPresent
    args:
      - --workspace=/root
    labels:
      app: upgrade-ci-1
      test: e2e-upgrade
    resources:
      limits:
        cpu: "500m"
        memory: "512Mi"
      requests:
        cpu: "100m"
        memory: "128Mi"
EOF

    echo "  Waiting for upgrade-ci-1's SandboxClaim to become Bound (up to 60s)..."
    SB_CLAIM_NAME=""
    for i in {1..30}; do
        SB_CLAIM_NAME=$(kubectl get sandboxclaim -n "${WORKLOAD_NAMESPACE}" -o json \
            | jq -r '.items[] | select(any(.metadata.ownerReferences[]?; .name=="upgrade-ci-1")) | .metadata.name' \
            2>/dev/null | head -n 1 || true)
        if [ -n "$SB_CLAIM_NAME" ] && kubectl get sandboxclaim "$SB_CLAIM_NAME" -n "${WORKLOAD_NAMESPACE}" 2>/dev/null | grep -qw Bound; then
            echo "  SandboxClaim ${SB_CLAIM_NAME} is Bound under v0.4.6."
            break
        fi
        sleep 2
    done

    if [ -z "$SB_CLAIM_NAME" ] || ! kubectl get sandboxclaim "$SB_CLAIM_NAME" -n "${WORKLOAD_NAMESPACE}" 2>/dev/null | grep -qw Bound; then
        echo "Error: upgrade-ci-1's SandboxClaim did not become Bound under v0.4.6."
        UPGRADE_FAILED=1
    fi
fi

    if [ $UPGRADE_FAILED -eq 0 ]; then
        echo "3. Capturing sandbox name for post-upgrade GC verification..."
        SB_NAME=$(kubectl get sandboxclaim "${SB_CLAIM_NAME}" -n "${WORKLOAD_NAMESPACE}" \
            -o jsonpath='{.status.sandboxName}' 2>/dev/null || echo "")
        echo "  Pre-upgrade sandbox name: ${SB_NAME:-<unknown>}"
    fi

    if [ $UPGRADE_FAILED -eq 0 ]; then
        echo "4. Upgrading agent-sandbox to ${NEW_VERSION} via server-side apply..."
        kubectl apply --server-side \
            -f "https://github.com/kubernetes-sigs/agent-sandbox/releases/download/${NEW_VERSION}/sandbox-with-extensions.yaml" \
            || UPGRADE_FAILED=1
    fi

    if [ $UPGRADE_FAILED -eq 0 ]; then
        echo "5. Waiting for new v0.5.2 controller to become ready (up to 180s)..."
        kubectl rollout status deployment/agent-sandbox-controller \
            -n agent-sandbox-system --timeout=180s || UPGRADE_FAILED=1
    fi

    if [ $UPGRADE_FAILED -eq 0 ]; then
        echo "6. Running post-upgrade migrate..."
        if ! "${MIGRATE_TMP}" --phase=migrate; then
            echo "Error: migrate.sh migrate phase failed."
            UPGRADE_FAILED=1
        fi
    fi

    if [ $UPGRADE_FAILED -eq 0 ]; then
        echo "7. Verifying controller is running the v0.5.2 image..."
        ACTUAL_IMAGE=$(kubectl get deployment agent-sandbox-controller -n agent-sandbox-system \
            -o jsonpath='{.spec.template.spec.containers[?(@.name=="agent-sandbox-controller")].image}' \
            2>/dev/null || echo "")
        EXPECTED_IMAGE="registry.k8s.io/agent-sandbox/agent-sandbox-controller:${NEW_VERSION}"
        if [ "${ACTUAL_IMAGE}" != "${EXPECTED_IMAGE}" ]; then
            echo "Error: controller image mismatch: expected ${EXPECTED_IMAGE}, got ${ACTUAL_IMAGE}"
            UPGRADE_FAILED=1
        else
            echo "  Controller image verified: ${ACTUAL_IMAGE}"
        fi
    fi

    if [ $UPGRADE_FAILED -eq 0 ]; then
        echo "8. Creating upgrade-ci-2 to verify new claims bind under v0.5.2..."
        cat <<EOF | kubectl apply -n "${WORKLOAD_NAMESPACE}" -f - || UPGRADE_FAILED=1
apiVersion: runtime.agentcube.volcano.sh/v1alpha1
kind: CodeInterpreter
metadata:
  name: upgrade-ci-2
spec:
  warmPoolSize: 1
  ports:
    - pathPrefix: "/"
      port: 8080
      protocol: "HTTP"
  template:
    image: picod:latest
    imagePullPolicy: IfNotPresent
    args:
      - --workspace=/root
    labels:
      app: upgrade-ci-2
      test: e2e-upgrade
    resources:
      limits:
        cpu: "500m"
        memory: "512Mi"
      requests:
        cpu: "100m"
        memory: "128Mi"
EOF
        echo "  Waiting for upgrade-ci-2's SandboxClaim to become Bound under v0.5.2 (up to 120s)..."
        SB_CLAIM_2_NAME=""
        for i in {1..60}; do
            SB_CLAIM_2_NAME=$(kubectl get sandboxclaim -n "${WORKLOAD_NAMESPACE}" -o json \
                | jq -r '.items[] | select(any(.metadata.ownerReferences[]?; .name=="upgrade-ci-2")) | .metadata.name' \
                2>/dev/null | head -n 1 || true)
            if [ -n "$SB_CLAIM_2_NAME" ] && kubectl get sandboxclaim "$SB_CLAIM_2_NAME" -n "${WORKLOAD_NAMESPACE}" 2>/dev/null | grep -qw Bound; then
                echo "  SandboxClaim ${SB_CLAIM_2_NAME} is Bound under v0.5.2."
                break
            fi
            sleep 2
        done
        if [ -z "$SB_CLAIM_2_NAME" ] || ! kubectl get sandboxclaim "$SB_CLAIM_2_NAME" -n "${WORKLOAD_NAMESPACE}" 2>/dev/null | grep -qw Bound; then
            echo "Error: upgrade-ci-2's SandboxClaim did not become Bound after upgrade to v0.5.2."
            kubectl get sandboxclaim -n "${WORKLOAD_NAMESPACE}" -o wide 2>/dev/null || true
            UPGRADE_FAILED=1
        fi
    fi

    if [ $UPGRADE_FAILED -eq 0 ]; then
        echo "9. Verifying GC: deleting upgrade-ci-1 and waiting for its Sandbox to disappear..."
        kubectl delete codeinterpreter upgrade-ci-1 -n "${WORKLOAD_NAMESPACE}" --ignore-not-found=true || UPGRADE_FAILED=1
        if [ $UPGRADE_FAILED -eq 0 ] && [ -n "${SB_NAME:-}" ]; then
            GC_FAILED=0
            for i in {1..30}; do
                if ! kubectl get sandbox "${SB_NAME}" -n "${WORKLOAD_NAMESPACE}" 2>/dev/null | grep -q "${SB_NAME}"; then
                    echo "  Sandbox ${SB_NAME} was garbage collected."
                    GC_FAILED=0
                    break
                fi
                GC_FAILED=1
                sleep 2
            done
            if [ $GC_FAILED -eq 1 ]; then
                echo "Error: Sandbox ${SB_NAME} was not garbage collected after deleting upgrade-ci-1."
                UPGRADE_FAILED=1
            fi
        fi
    fi

    echo "10. Cleaning up upgrade test resources..."
    kubectl delete codeinterpreter upgrade-ci-1 upgrade-ci-2 -n "${WORKLOAD_NAMESPACE}" --ignore-not-found=true || true
    rm -f "${MIGRATE_TMP:-}"

    if [ $UPGRADE_FAILED -eq 0 ]; then
        echo "Upgrade test (v0.4.6 -> v0.5.2) completed successfully!"
    else
        echo "ERROR: Upgrade test (v0.4.6 -> v0.5.2) failed"
        TEST_FAILED=1
    fi
fi

# Collect logs if core tests failed
echo "[DEBUG] TEST_FAILED value: $TEST_FAILED"
if [ $TEST_FAILED -eq 1 ]; then
    echo "Core tests failed, collecting component logs..."
    collect_component_logs
    exit 1
fi

echo "All core E2E tests passed!"
