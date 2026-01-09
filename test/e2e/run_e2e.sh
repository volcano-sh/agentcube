#!/bin/bash
set -euo pipefail
IFS=$'\n\t'

# Configuration
E2E_CLUSTER_NAME=${E2E_CLUSTER_NAME:-agentcube-e2e}
E2E_CLEAN_CLUSTER=${E2E_CLEAN_CLUSTER:-true}
AGENT_SANDBOX_VERSION=${AGENT_SANDBOX_VERSION:-v0.1.0}
WORKLOAD_MANAGER_IMAGE=${WORKLOAD_MANAGER_IMAGE:-workloadmanager:latest}
ROUTER_IMAGE=${ROUTER_IMAGE:-agentcube-router:latest}
PICOD_IMAGE=${PICOD_IMAGE:-picod:latest}
REDIS_IMAGE=${REDIS_IMAGE:-redis:7-alpine}
AGENTCUBE_NAMESPACE=${AGENTCUBE_NAMESPACE:-agentcube}
E2E_VENV_DIR=${E2E_VENV_DIR:-/tmp/agentcube-e2e-venv}

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

    # Kill any remaining kubectl port-forward processes
    echo "Killing any remaining kubectl port-forward processes..."
    pkill -f "kubectl port-forward" 2>/dev/null || true

    # Wait a moment for processes to terminate
    sleep 2

    # Force kill any remaining processes on our ports
    echo "Force killing any processes using ports 8080-8081..."
    for port in 8080 8081; do
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

step() {
    echo
    echo "==> $1"
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
    kind load docker-image "${image}" --name "${E2E_CLUSTER_NAME}"
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

echo "Starting E2E tests..."

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

step "Building images..."
# We assume we are in the project root
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
WM_EXTRA_ENV='[{"name":"REDIS_PASSWORD_REQUIRED","value":"false"},{"name":"JWT_KEY_SECRET_NAMESPACE","value":"agentcube"}]'
ROUTER_EXTRA_ENV='[{"name":"REDIS_PASSWORD_REQUIRED","value":"false"}]'

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
    --wait

step "Waiting for deployments..."
kubectl -n "${AGENTCUBE_NAMESPACE}" rollout status deployment/workloadmanager --timeout=300s
kubectl -n "${AGENTCUBE_NAMESPACE}" rollout status deployment/agentcube-router --timeout=300s

step "Creating ServiceAccount and Token..."
kubectl create serviceaccount e2e-test -n "${AGENTCUBE_NAMESPACE}" || true
kubectl create clusterrolebinding e2e-test-binding --clusterrole=workloadmanager --serviceaccount="${AGENTCUBE_NAMESPACE}:e2e-test" || true

step "Creating test AgentRuntimes..."
# Create normal echo-agent
kubectl apply --validate=false -f test/e2e/echo_agent.yaml
# Create echo-agent-short-ttl with short sessionTimeout for TTL testing
tmp_ttl_agent=$(mktemp)
sed 's/name: echo-agent/name: echo-agent-short-ttl/; s/app: echo-agent/app: echo-agent-short-ttl/; s/sessionTimeout: "15m"/sessionTimeout: "30s"/' test/e2e/echo_agent.yaml > "$tmp_ttl_agent"
kubectl apply --validate=false -f "$tmp_ttl_agent"
rm -f "$tmp_ttl_agent"

step "Creating test CodeInterpreter..."
# Create e2e-code-interpreter CodeInterpreter
kubectl apply --validate=false -f test/e2e/e2e_code_interpreter.yaml

step "Waiting for AgentRuntimes to be ready..."
kubectl get agentruntime echo-agent -n "${AGENTCUBE_NAMESPACE}" -o jsonpath='{.metadata.name}{"\n"}' || echo "echo-agent may still be starting..."
kubectl get agentruntime echo-agent-short-ttl -n "${AGENTCUBE_NAMESPACE}" -o jsonpath='{.metadata.name}{"\n"}' || echo "echo-agent-short-ttl may still be starting..."
echo "AgentRuntimes created, waiting for pods to be ready..."
sleep 10

step "Pre-cleanup"
# Clean up any leftover processes before starting
echo "Performing pre-run cleanup..."
pkill -f "kubectl port-forward" 2>/dev/null || true
for port in 8080 8081; do
    if command -v lsof >/dev/null 2>&1 && lsof -i :$port >/dev/null 2>&1; then
        lsof -ti :$port | xargs kill -9 2>/dev/null || true
    elif command -v netstat >/dev/null 2>&1 && netstat -tulpn 2>/dev/null | grep ":$port " >/dev/null; then
        netstat -tulpn 2>/dev/null | grep ":$port " | awk '{print $7}' | cut -d'/' -f1 | xargs kill -9 2>/dev/null || true
    fi
done
rm -f /tmp/workload_port_forward.log /tmp/router_port_forward.log 2>/dev/null || true
sleep 2

step "Running tests..."
# Create token
API_TOKEN=$(kubectl create token e2e-test -n "${AGENTCUBE_NAMESPACE}" --duration=24h)
echo "Token created"

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
for i in $(seq 1 10); do
    if curl -sf -o /dev/null "http://localhost:${WORKLOAD_MANAGER_LOCAL_PORT}/health" && curl -sf -o /dev/null "http://localhost:${ROUTER_LOCAL_PORT}/health/live"; then
        echo "Port-forwards are ready."
        break
    fi
    if [ $i -eq 10 ]; then
        echo "Timed out waiting for port-forwards." >&2
        exit 1
    fi
    sleep 1
done

# Run tests
echo "Running Go tests..."
WORKLOAD_MANAGER_ADDR="http://localhost:${WORKLOAD_MANAGER_LOCAL_PORT}" ROUTER_URL="http://localhost:${ROUTER_LOCAL_PORT}" API_TOKEN=$API_TOKEN go test -v ./test/e2e/...

echo "Running Python CodeInterpreter tests..."
cd "$(dirname "$0")"

# Setup Python virtual environment for testing
if [ ! -d "$E2E_VENV_DIR" ]; then
    echo "Creating Python virtual environment..."
    python3 -m venv "$E2E_VENV_DIR"
fi

echo "Activating virtual environment and installing dependencies..."
source "$E2E_VENV_DIR/bin/activate"
pip install --upgrade pip

# Install agentcube SDK in development mode
pip install -e ../../sdk-python

# Check if agentcube package is available after installation
require_python

WORKLOAD_MANAGER_ADDR="http://localhost:${WORKLOAD_MANAGER_LOCAL_PORT}" ROUTER_URL="http://localhost:${ROUTER_LOCAL_PORT}" API_TOKEN=$API_TOKEN AGENTCUBE_NAMESPACE="${AGENTCUBE_NAMESPACE}" "$E2E_VENV_DIR/bin/python" test_codeinterpreter.py
