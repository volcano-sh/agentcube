#!/bin/bash
set -euo pipefail
IFS=$'\n\t'

# Configuration
E2E_CLUSTER_NAME=${E2E_CLUSTER_NAME:-agentcube-e2e}
E2E_CLEAN_CLUSTER=${E2E_CLEAN_CLUSTER:-true}
E2E_SKIP_SETUP=${E2E_SKIP_SETUP:-false}
if [ -z "${MTLS_ENABLED+x}" ]; then
    if [ "${E2E_SKIP_SETUP}" = "true" ]; then
        MTLS_ENABLED=false
    else
        MTLS_ENABLED=true
    fi
fi
AGENT_SANDBOX_VERSION=${AGENT_SANDBOX_VERSION:-v0.5.3}
E2E_REQUIRE_CODEINTERPRETER=${E2E_REQUIRE_CODEINTERPRETER:-false}
WORKLOAD_MANAGER_IMAGE=${WORKLOAD_MANAGER_IMAGE:-workloadmanager:latest}
ROUTER_IMAGE=${ROUTER_IMAGE:-agentcube-router:latest}
PICOD_IMAGE=${PICOD_IMAGE:-picod:latest}
REDIS_IMAGE=${REDIS_IMAGE:-redis:7-alpine}
AGENTCUBE_NAMESPACE=${AGENTCUBE_NAMESPACE:-agentcube}
WORKLOAD_NAMESPACE=${WORKLOAD_NAMESPACE:-agentcube}
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
    
    # E2E Upgrade test logic
    if [ "${E2E_SKIP_SETUP}" != "true" ] && [ "${AGENT_SANDBOX_VERSION}" = "v0.5.3" ]; then
        step "Seeding v0.4.6 agent-sandbox for upgrade test..."
        kubectl_apply_url "https://github.com/kubernetes-sigs/agent-sandbox/releases/download/v0.4.6/manifest.yaml"
        kubectl_apply_url "https://github.com/kubernetes-sigs/agent-sandbox/releases/download/v0.4.6/extensions.yaml"
        
        kubectl -n agent-sandbox-system rollout status deployment/agent-sandbox-controller --timeout=300s
        
        # Seed the template and pool first so the claim controller can adopt a
        # known Ready member instead of racing a cold fallback.
        ensure_namespace "${AGENTCUBE_NAMESPACE}"
        cat <<EOF | kubectl apply -f -
apiVersion: extensions.agents.x-k8s.io/v1alpha1
kind: SandboxTemplate
metadata:
  name: e2e-upgrade-template
  namespace: ${AGENTCUBE_NAMESPACE}
spec:
  podTemplate:
    spec:
      containers:
      - name: pause
        image: registry.k8s.io/pause:3.10
---
apiVersion: extensions.agents.x-k8s.io/v1alpha1
kind: SandboxWarmPool
metadata:
  name: e2e-upgrade-warmpool
  namespace: ${AGENTCUBE_NAMESPACE}
spec:
  replicas: 1
  sandboxTemplateRef:
    name: e2e-upgrade-template
EOF

        echo "Waiting for v0.4.6 warm pool to expose one Ready, pool-owned Sandbox..."
        for i in $(seq 1 30); do
            WARM_POOL_UID=$(kubectl get sandboxwarmpool e2e-upgrade-warmpool -n "${AGENTCUBE_NAMESPACE}" -o jsonpath='{.metadata.uid}' 2>/dev/null || true)
            POOL_SELECTOR=$(kubectl get sandboxwarmpool e2e-upgrade-warmpool -n "${AGENTCUBE_NAMESPACE}" -o jsonpath='{.status.selector}' 2>/dev/null || true)
            POOL_REPLICAS=$(kubectl get sandboxwarmpool e2e-upgrade-warmpool -n "${AGENTCUBE_NAMESPACE}" -o jsonpath='{.status.replicas}' 2>/dev/null || true)
            POOL_READY_REPLICAS=$(kubectl get sandboxwarmpool e2e-upgrade-warmpool -n "${AGENTCUBE_NAMESPACE}" -o jsonpath='{.status.readyReplicas}' 2>/dev/null || true)

            POOL_MEMBER_COUNT=0
            POOL_MEMBER_NAME=""
            if [ -n "${POOL_SELECTOR}" ]; then
                POOL_MEMBER_COUNT=$(kubectl get sandboxes -n "${AGENTCUBE_NAMESPACE}" -l "${POOL_SELECTOR}" -o name 2>/dev/null | wc -l | tr -d '[:space:]' || true)
                POOL_MEMBER_NAME=$(kubectl get sandboxes -n "${AGENTCUBE_NAMESPACE}" -l "${POOL_SELECTOR}" -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)
            fi

            if [ "${POOL_REPLICAS:-0}" -eq 1 ] && [ "${POOL_READY_REPLICAS:-0}" -eq 1 ] && [ "${POOL_MEMBER_COUNT:-0}" -eq 1 ] && [ -n "${POOL_MEMBER_NAME}" ]; then
                POOL_MEMBER_OWNER_KIND=$(kubectl get sandbox "${POOL_MEMBER_NAME}" -n "${AGENTCUBE_NAMESPACE}" -o jsonpath='{.metadata.ownerReferences[?(@.controller==true)].kind}' 2>/dev/null || true)
                POOL_MEMBER_OWNER_NAME=$(kubectl get sandbox "${POOL_MEMBER_NAME}" -n "${AGENTCUBE_NAMESPACE}" -o jsonpath='{.metadata.ownerReferences[?(@.controller==true)].name}' 2>/dev/null || true)
                POOL_MEMBER_OWNER_UID=$(kubectl get sandbox "${POOL_MEMBER_NAME}" -n "${AGENTCUBE_NAMESPACE}" -o jsonpath='{.metadata.ownerReferences[?(@.controller==true)].uid}' 2>/dev/null || true)
                POOL_MEMBER_READY=$(kubectl get sandbox "${POOL_MEMBER_NAME}" -n "${AGENTCUBE_NAMESPACE}" -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null || true)

                if [ "${POOL_MEMBER_OWNER_KIND}" = "SandboxWarmPool" ] && [ "${POOL_MEMBER_OWNER_NAME}" = "e2e-upgrade-warmpool" ] && [ "${POOL_MEMBER_OWNER_UID}" = "${WARM_POOL_UID}" ] && [ "${POOL_MEMBER_READY}" = "True" ]; then
                    BOUND_SANDBOX_NAME="${POOL_MEMBER_NAME}"
                    BOUND_SANDBOX_UID=$(kubectl get sandbox "${BOUND_SANDBOX_NAME}" -n "${AGENTCUBE_NAMESPACE}" -o jsonpath='{.metadata.uid}')
                    BOUND_POD_NAME=$(kubectl get sandbox "${BOUND_SANDBOX_NAME}" -n "${AGENTCUBE_NAMESPACE}" -o jsonpath='{.metadata.annotations.agents\.x-k8s\.io/pod-name}' 2>/dev/null || true)
                    BOUND_POD_NAME=${BOUND_POD_NAME:-${BOUND_SANDBOX_NAME}}
                    BOUND_POD_UID=$(kubectl get pod "${BOUND_POD_NAME}" -n "${AGENTCUBE_NAMESPACE}" -o jsonpath='{.metadata.uid}' 2>/dev/null || true)
                    if [ -n "${BOUND_POD_UID}" ]; then
                        echo "Captured Ready v0.4.6 pool member ${BOUND_SANDBOX_NAME} (${BOUND_SANDBOX_UID}) and Pod ${BOUND_POD_NAME} (${BOUND_POD_UID})"
                        break
                    fi
                fi
            fi
            if [ "$i" -eq 30 ]; then
                echo "Timed out waiting for one Ready Sandbox owned by e2e-upgrade-warmpool" >&2
                kubectl get sandboxwarmpool e2e-upgrade-warmpool -n "${AGENTCUBE_NAMESPACE}" -o yaml >&2 || true
                kubectl get sandboxes -n "${AGENTCUBE_NAMESPACE}" -o yaml >&2 || true
                exit 1
            fi
            sleep 2
        done

        echo "Creating a claim that targets the captured v0.4.6 warm pool member..."
        cat <<EOF | kubectl apply -f -
apiVersion: extensions.agents.x-k8s.io/v1alpha1
kind: SandboxClaim
metadata:
  name: upgrade-bound-claim
  namespace: ${AGENTCUBE_NAMESPACE}
spec:
  sandboxTemplateRef:
    name: e2e-upgrade-template
  warmpool: e2e-upgrade-warmpool
EOF

        CLAIM_UID=$(kubectl get sandboxclaim upgrade-bound-claim -n "${AGENTCUBE_NAMESPACE}" -o jsonpath='{.metadata.uid}')

        echo "Waiting for the v0.4.6 claim controller to complete adoption of ${BOUND_SANDBOX_NAME}..."
        for i in $(seq 1 30); do
            CLAIM_ASSIGNED_SANDBOX=$(kubectl get sandboxclaim upgrade-bound-claim -n "${AGENTCUBE_NAMESPACE}" -o jsonpath='{.metadata.labels.agents\.x-k8s\.io/sandbox-name}' 2>/dev/null || true)
            CLAIM_BOUND_SANDBOX=$(kubectl get sandboxclaim upgrade-bound-claim -n "${AGENTCUBE_NAMESPACE}" -o jsonpath='{.status.sandbox.name}' 2>/dev/null || true)
            CLAIM_READY=$(kubectl get sandboxclaim upgrade-bound-claim -n "${AGENTCUBE_NAMESPACE}" -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null || true)
            ADOPTED_OWNER_KIND=$(kubectl get sandbox "${BOUND_SANDBOX_NAME}" -n "${AGENTCUBE_NAMESPACE}" -o jsonpath='{.metadata.ownerReferences[?(@.controller==true)].kind}' 2>/dev/null || true)
            ADOPTED_OWNER_NAME=$(kubectl get sandbox "${BOUND_SANDBOX_NAME}" -n "${AGENTCUBE_NAMESPACE}" -o jsonpath='{.metadata.ownerReferences[?(@.controller==true)].name}' 2>/dev/null || true)
            ADOPTED_OWNER_UID=$(kubectl get sandbox "${BOUND_SANDBOX_NAME}" -n "${AGENTCUBE_NAMESPACE}" -o jsonpath='{.metadata.ownerReferences[?(@.controller==true)].uid}' 2>/dev/null || true)
            ADOPTED_POOL_LABEL=$(kubectl get sandbox "${BOUND_SANDBOX_NAME}" -n "${AGENTCUBE_NAMESPACE}" -o jsonpath='{.metadata.labels.agents\.x-k8s\.io/warm-pool-sandbox}' 2>/dev/null || true)
            ADOPTED_CLAIM_UID_LABEL=$(kubectl get sandbox "${BOUND_SANDBOX_NAME}" -n "${AGENTCUBE_NAMESPACE}" -o jsonpath='{.metadata.labels.agents\.x-k8s\.io/claim-uid}' 2>/dev/null || true)
            ADOPTED_SANDBOX_UID=$(kubectl get sandbox "${BOUND_SANDBOX_NAME}" -n "${AGENTCUBE_NAMESPACE}" -o jsonpath='{.metadata.uid}' 2>/dev/null || true)
            ADOPTED_POD_UID=$(kubectl get pod "${BOUND_POD_NAME}" -n "${AGENTCUBE_NAMESPACE}" -o jsonpath='{.metadata.uid}' 2>/dev/null || true)

            if [ "${CLAIM_ASSIGNED_SANDBOX}" = "${BOUND_SANDBOX_NAME}" ] && [ "${CLAIM_BOUND_SANDBOX}" = "${BOUND_SANDBOX_NAME}" ] && [ "${CLAIM_READY}" = "True" ] && [ "${ADOPTED_OWNER_KIND}" = "SandboxClaim" ] && [ "${ADOPTED_OWNER_NAME}" = "upgrade-bound-claim" ] && [ "${ADOPTED_OWNER_UID}" = "${CLAIM_UID}" ] && [ -z "${ADOPTED_POOL_LABEL}" ] && [ "${ADOPTED_CLAIM_UID_LABEL}" = "${CLAIM_UID}" ] && [ "${ADOPTED_SANDBOX_UID}" = "${BOUND_SANDBOX_UID}" ] && [ "${ADOPTED_POD_UID}" = "${BOUND_POD_UID}" ]; then
                echo "v0.4.6 controller adopted the captured Sandbox without recreating its Sandbox or Pod"
                break
            fi
            if [ "$i" -eq 30 ]; then
                echo "Timed out waiting for controller-driven adoption of ${BOUND_SANDBOX_NAME}" >&2
                kubectl get sandboxclaim upgrade-bound-claim -n "${AGENTCUBE_NAMESPACE}" -o yaml >&2 || true
                kubectl get sandbox "${BOUND_SANDBOX_NAME}" -n "${AGENTCUBE_NAMESPACE}" -o yaml >&2 || true
                kubectl logs -n agent-sandbox-system deployment/agent-sandbox-controller --tail=100 >&2 || true
                exit 1
            fi
            sleep 2
        done

        echo "Waiting for v0.4.6 to replenish the pool after the real adoption..."
        for i in $(seq 1 30); do
            POOL_READY_REPLICAS=$(kubectl get sandboxwarmpool e2e-upgrade-warmpool -n "${AGENTCUBE_NAMESPACE}" -o jsonpath='{.status.readyReplicas}' 2>/dev/null || true)
            POOL_MEMBER_COUNT=$(kubectl get sandboxes -n "${AGENTCUBE_NAMESPACE}" -l "${POOL_SELECTOR}" -o name 2>/dev/null | wc -l | tr -d '[:space:]' || true)
            PRE_UPGRADE_POOL_MEMBER_NAME=$(kubectl get sandboxes -n "${AGENTCUBE_NAMESPACE}" -l "${POOL_SELECTOR}" -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)
            if [ "${POOL_READY_REPLICAS:-0}" -eq 1 ] && [ "${POOL_MEMBER_COUNT:-0}" -eq 1 ] && [ -n "${PRE_UPGRADE_POOL_MEMBER_NAME}" ] && [ "${PRE_UPGRADE_POOL_MEMBER_NAME}" != "${BOUND_SANDBOX_NAME}" ]; then
                PRE_UPGRADE_POOL_MEMBER_UID=$(kubectl get sandbox "${PRE_UPGRADE_POOL_MEMBER_NAME}" -n "${AGENTCUBE_NAMESPACE}" -o jsonpath='{.metadata.uid}' 2>/dev/null || true)
                PRE_UPGRADE_POOL_OWNER_KIND=$(kubectl get sandbox "${PRE_UPGRADE_POOL_MEMBER_NAME}" -n "${AGENTCUBE_NAMESPACE}" -o jsonpath='{.metadata.ownerReferences[?(@.controller==true)].kind}' 2>/dev/null || true)
                PRE_UPGRADE_POOL_OWNER_NAME=$(kubectl get sandbox "${PRE_UPGRADE_POOL_MEMBER_NAME}" -n "${AGENTCUBE_NAMESPACE}" -o jsonpath='{.metadata.ownerReferences[?(@.controller==true)].name}' 2>/dev/null || true)
                PRE_UPGRADE_POOL_OWNER_UID=$(kubectl get sandbox "${PRE_UPGRADE_POOL_MEMBER_NAME}" -n "${AGENTCUBE_NAMESPACE}" -o jsonpath='{.metadata.ownerReferences[?(@.controller==true)].uid}' 2>/dev/null || true)
                PRE_UPGRADE_POOL_MEMBER_READY=$(kubectl get sandbox "${PRE_UPGRADE_POOL_MEMBER_NAME}" -n "${AGENTCUBE_NAMESPACE}" -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null || true)
                if [ -n "${PRE_UPGRADE_POOL_MEMBER_UID}" ] && [ "${PRE_UPGRADE_POOL_MEMBER_UID}" != "${BOUND_SANDBOX_UID}" ] && [ "${PRE_UPGRADE_POOL_OWNER_KIND}" = "SandboxWarmPool" ] && [ "${PRE_UPGRADE_POOL_OWNER_NAME}" = "e2e-upgrade-warmpool" ] && [ "${PRE_UPGRADE_POOL_OWNER_UID}" = "${WARM_POOL_UID}" ] && [ "${PRE_UPGRADE_POOL_MEMBER_READY}" = "True" ]; then
                    echo "Captured v0.4.6 replacement pool member ${PRE_UPGRADE_POOL_MEMBER_NAME} (${PRE_UPGRADE_POOL_MEMBER_UID})"
                    break
                fi
            fi
            if [ "$i" -eq 30 ]; then
                echo "Timed out waiting for v0.4.6 to replenish e2e-upgrade-warmpool after adoption" >&2
                kubectl get sandboxwarmpool e2e-upgrade-warmpool -n "${AGENTCUBE_NAMESPACE}" -o yaml >&2 || true
                kubectl get sandboxes -n "${AGENTCUBE_NAMESPACE}" -o yaml >&2 || true
                exit 1
            fi
            sleep 2
        done

        echo "Creating an isolated v0.4.6 cold-start fixture..."
        cat <<EOF | kubectl apply -f -
apiVersion: extensions.agents.x-k8s.io/v1alpha1
kind: SandboxClaim
metadata:
  name: shadow-pool-e2e-code-interpreter
  namespace: ${AGENTCUBE_NAMESPACE}
spec:
  sandboxTemplateRef:
    name: e2e-upgrade-template
  warmpool: none
EOF
        kubectl wait --for='jsonpath={.status.conditions[?(@.type=="Ready")].status}=True' sandboxclaim/shadow-pool-e2e-code-interpreter -n "${AGENTCUBE_NAMESPACE}" --timeout=60s
        COLD_SANDBOX_NAME=$(kubectl get sandboxclaim shadow-pool-e2e-code-interpreter -n "${AGENTCUBE_NAMESPACE}" -o jsonpath='{.status.sandbox.name}')
        if [ "${COLD_SANDBOX_NAME}" != "shadow-pool-e2e-code-interpreter" ]; then
            echo "Error: cold fixture unexpectedly adopted a warm pool member: ${COLD_SANDBOX_NAME}" >&2
            exit 1
        fi

        echo "Running migration bootstrap phase..."
        curl -fsSL https://raw.githubusercontent.com/kubernetes-sigs/agent-sandbox/refs/tags/v0.5.3/helm/files/migrate.sh -o /tmp/migrate.sh
        chmod +x /tmp/migrate.sh
        /tmp/migrate.sh --phase=bootstrap
    fi

    # Download then apply to avoid URL parsing issues / improve debuggability.
    if [[ "${AGENT_SANDBOX_VERSION}" =~ ^v0\.4\. ]]; then
        kubectl_apply_url "https://github.com/kubernetes-sigs/agent-sandbox/releases/download/${AGENT_SANDBOX_VERSION}/manifest.yaml"
        kubectl_apply_url "https://github.com/kubernetes-sigs/agent-sandbox/releases/download/${AGENT_SANDBOX_VERSION}/extensions.yaml"
    else
        kubectl_apply_url "https://github.com/kubernetes-sigs/agent-sandbox/releases/download/${AGENT_SANDBOX_VERSION}/sandbox-with-extensions.yaml"
        kubectl -n agent-sandbox-system patch deployment agent-sandbox-controller --type='json' -p='[{"op": "add", "path": "/spec/template/spec/containers/0/args/-", "value": "--sandbox-concurrent-workers=10"}]'
    fi
    
    verify_agent_sandbox_controller
    if [ "${E2E_SKIP_SETUP}" != "true" ] && [ "${AGENT_SANDBOX_VERSION}" = "v0.5.3" ]; then
        echo "Waiting for conversion webhook to become responsive (kube-proxy endpoint sync)..."
        for i in {1..30}; do
            if kubectl get sandboxwarmpools.extensions.agents.x-k8s.io -A >/dev/null 2>&1; then
                echo "Webhook is responsive!"
                break
            fi
            echo "Webhook not ready yet, waiting... (attempt $i/30)"
            if [ $i -eq 30 ]; then
                echo "Timed out waiting for webhook. Controller logs:"
                kubectl logs -n agent-sandbox-system -l control-plane=controller-manager || true
                exit 1
            fi
            sleep 5
        done
        
        echo "Running migration migrate phase..."
        /tmp/migrate.sh --phase=migrate
        
        step "Verifying SandboxClaims survived migration"
        kubectl get sandboxclaim shadow-pool-e2e-code-interpreter -n "${AGENTCUBE_NAMESPACE}" || {
            echo "Error: Seeded cold SandboxClaim was lost during migration!" >&2
            exit 1
        }
        
        kubectl get sandboxclaim upgrade-bound-claim -n "${AGENTCUBE_NAMESPACE}" || {
            echo "Error: Seeded bound SandboxClaim was lost during migration!" >&2
            exit 1
        }

        echo "Waiting for v0.5.3 controllers to reconcile the exact bound and idle pool lineages..."
        CONTROLLER_NAMESPACE="agent-sandbox-system"
        for i in $(seq 1 60); do
            POST_UPGRADE_CLAIM_UID=$(kubectl get sandboxclaim upgrade-bound-claim -n "${AGENTCUBE_NAMESPACE}" -o jsonpath='{.metadata.uid}' 2>/dev/null || true)
            POST_UPGRADE_WARM_POOL_REF=$(kubectl get sandboxclaim upgrade-bound-claim -n "${AGENTCUBE_NAMESPACE}" -o jsonpath='{.spec.warmPoolRef.name}' 2>/dev/null || true)
            POST_UPGRADE_BINDING=$(kubectl get sandboxclaim upgrade-bound-claim -n "${AGENTCUBE_NAMESPACE}" -o jsonpath='{.status.sandbox.name}' 2>/dev/null || true)
            POST_UPGRADE_OWNER_KIND=$(kubectl get sandbox "${BOUND_SANDBOX_NAME}" -n "${AGENTCUBE_NAMESPACE}" -o jsonpath='{.metadata.ownerReferences[?(@.controller==true)].kind}' 2>/dev/null || true)
            POST_UPGRADE_OWNER_NAME=$(kubectl get sandbox "${BOUND_SANDBOX_NAME}" -n "${AGENTCUBE_NAMESPACE}" -o jsonpath='{.metadata.ownerReferences[?(@.controller==true)].name}' 2>/dev/null || true)
            POST_UPGRADE_OWNER_UID=$(kubectl get sandbox "${BOUND_SANDBOX_NAME}" -n "${AGENTCUBE_NAMESPACE}" -o jsonpath='{.metadata.ownerReferences[?(@.controller==true)].uid}' 2>/dev/null || true)
            BOUND_LAUNCH_TYPE=$(kubectl get sandbox "${BOUND_SANDBOX_NAME}" -n "${AGENTCUBE_NAMESPACE}" -o jsonpath='{.metadata.labels.agents\.x-k8s\.io/launch-type}' 2>/dev/null || true)
            IDLE_LAUNCH_TYPE=$(kubectl get sandbox "${PRE_UPGRADE_POOL_MEMBER_NAME}" -n "${AGENTCUBE_NAMESPACE}" -o jsonpath='{.metadata.labels.agents\.x-k8s\.io/launch-type}' 2>/dev/null || true)
            POST_UPGRADE_POOL_UID=$(kubectl get sandboxwarmpool e2e-upgrade-warmpool -n "${AGENTCUBE_NAMESPACE}" -o jsonpath='{.metadata.uid}' 2>/dev/null || true)
            POST_UPGRADE_POOL_SELECTOR=$(kubectl get sandboxwarmpool e2e-upgrade-warmpool -n "${AGENTCUBE_NAMESPACE}" -o jsonpath='{.status.selector}' 2>/dev/null || true)
            POST_UPGRADE_IDLE_UID=$(kubectl get sandbox "${PRE_UPGRADE_POOL_MEMBER_NAME}" -n "${AGENTCUBE_NAMESPACE}" -o jsonpath='{.metadata.uid}' 2>/dev/null || true)
            POST_UPGRADE_IDLE_OWNER_KIND=$(kubectl get sandbox "${PRE_UPGRADE_POOL_MEMBER_NAME}" -n "${AGENTCUBE_NAMESPACE}" -o jsonpath='{.metadata.ownerReferences[?(@.controller==true)].kind}' 2>/dev/null || true)
            POST_UPGRADE_IDLE_OWNER_NAME=$(kubectl get sandbox "${PRE_UPGRADE_POOL_MEMBER_NAME}" -n "${AGENTCUBE_NAMESPACE}" -o jsonpath='{.metadata.ownerReferences[?(@.controller==true)].name}' 2>/dev/null || true)
            POST_UPGRADE_IDLE_OWNER_UID=$(kubectl get sandbox "${PRE_UPGRADE_POOL_MEMBER_NAME}" -n "${AGENTCUBE_NAMESPACE}" -o jsonpath='{.metadata.ownerReferences[?(@.controller==true)].uid}' 2>/dev/null || true)

            if [ "${POST_UPGRADE_CLAIM_UID}" = "${CLAIM_UID}" ] &&
                [ "${POST_UPGRADE_WARM_POOL_REF}" = "e2e-upgrade-warmpool" ] &&
                [ "${POST_UPGRADE_BINDING}" = "${BOUND_SANDBOX_NAME}" ] &&
                [ "${POST_UPGRADE_OWNER_KIND}" = "SandboxClaim" ] &&
                [ "${POST_UPGRADE_OWNER_NAME}" = "upgrade-bound-claim" ] &&
                [ "${POST_UPGRADE_OWNER_UID}" = "${CLAIM_UID}" ] &&
                [ "${BOUND_LAUNCH_TYPE}" = "warm" ] &&
                [ "${IDLE_LAUNCH_TYPE}" = "warm" ] &&
                [ "${POST_UPGRADE_POOL_UID}" = "${WARM_POOL_UID}" ] &&
                [ -n "${POST_UPGRADE_POOL_SELECTOR}" ] &&
                [ "${POST_UPGRADE_POOL_SELECTOR}" = "${POOL_SELECTOR}" ] &&
                [ "${POST_UPGRADE_IDLE_UID}" = "${PRE_UPGRADE_POOL_MEMBER_UID}" ] &&
                [ "${POST_UPGRADE_IDLE_OWNER_KIND}" = "SandboxWarmPool" ] &&
                [ "${POST_UPGRADE_IDLE_OWNER_NAME}" = "e2e-upgrade-warmpool" ] &&
                [ "${POST_UPGRADE_IDLE_OWNER_UID}" = "${WARM_POOL_UID}" ]; then
                echo "v0.5.3 controllers marked both preserved warm-start lineages"
                break
            fi
            if [ "$i" -eq 60 ]; then
                echo "Error: v0.5.3 controllers did not reconcile the preserved bound and idle pool lineages" >&2
                kubectl get sandboxclaim upgrade-bound-claim -n "${AGENTCUBE_NAMESPACE}" -o yaml >&2 || true
                kubectl get sandboxwarmpool e2e-upgrade-warmpool -n "${AGENTCUBE_NAMESPACE}" -o yaml >&2 || true
                kubectl get sandbox "${BOUND_SANDBOX_NAME}" "${PRE_UPGRADE_POOL_MEMBER_NAME}" -n "${AGENTCUBE_NAMESPACE}" -o yaml >&2 || true
                kubectl logs -n "${CONTROLLER_NAMESPACE}" deployment/agent-sandbox-controller --tail=100 >&2 || true
                exit 1
            fi
            sleep 2
        done

        echo "Waiting for upgrade-bound-claim to report Ready=True after current-controller reconciliation..."
        if ! kubectl wait --for='jsonpath={.status.conditions[?(@.type=="Ready")].status}=True' sandboxclaim/upgrade-bound-claim -n "${AGENTCUBE_NAMESPACE}" --timeout=60s; then
            echo "Error: Timed out waiting 60s for SandboxClaim upgrade-bound-claim Ready=True!" >&2
            echo "=== SandboxClaim Diagnostics ===" >&2
            kubectl get sandboxclaim upgrade-bound-claim -n "${AGENTCUBE_NAMESPACE}" -o yaml >&2
            kubectl describe sandboxclaim upgrade-bound-claim -n "${AGENTCUBE_NAMESPACE}" >&2
            echo "=== Events ===" >&2
            kubectl get events -n "${AGENTCUBE_NAMESPACE}" --sort-by=.metadata.creationTimestamp | tail -50 >&2
            echo "=== Controller Logs ===" >&2
            kubectl logs -n "${CONTROLLER_NAMESPACE}" deployment/agent-sandbox-controller --tail=100 >&2 || true
            exit 1
        fi
        
        echo "Verifying warm-start regression: bound Sandbox and Pod UIDs must not change..."
        POST_UPGRADE_SANDBOX_UID=$(kubectl get sandbox "${BOUND_SANDBOX_NAME}" -n "${AGENTCUBE_NAMESPACE}" -o jsonpath='{.metadata.uid}')
        POST_UPGRADE_POD_UID=$(kubectl get pod "${BOUND_POD_NAME}" -n "${AGENTCUBE_NAMESPACE}" -o jsonpath='{.metadata.uid}')
        
        if [ "${POST_UPGRADE_SANDBOX_UID}" != "${BOUND_SANDBOX_UID}" ] || [ -z "${POST_UPGRADE_SANDBOX_UID}" ]; then
            echo "Error: Bound Sandbox UID changed or lost during upgrade! (Expected: ${BOUND_SANDBOX_UID}, Got: ${POST_UPGRADE_SANDBOX_UID})" >&2
            exit 1
        fi
        
        if [ "${POST_UPGRADE_POD_UID}" != "${BOUND_POD_UID}" ] || [ -z "${POST_UPGRADE_POD_UID}" ]; then
            echo "Error: Bound Pod UID changed or lost during upgrade! (Expected: ${BOUND_POD_UID}, Got: ${POST_UPGRADE_POD_UID})" >&2
            exit 1
        fi
        
        echo "SandboxClaim migration, bound lifecycle, and exact pool lineage verified successfully"
        
        echo "Verifying garbage collection of the migrated claim-owned lineage..."
        kubectl delete sandboxclaim upgrade-bound-claim -n "${AGENTCUBE_NAMESPACE}"
        
        if ! kubectl wait --for=delete "sandbox/${BOUND_SANDBOX_NAME}" -n "${AGENTCUBE_NAMESPACE}" --timeout=90s; then
            echo "Error: bound Sandbox ${BOUND_SANDBOX_NAME} was not garbage-collected" >&2
            kubectl get sandbox "${BOUND_SANDBOX_NAME}" -n "${AGENTCUBE_NAMESPACE}" -o yaml >&2 || true
            exit 1
        fi
        if ! kubectl wait --for=delete "pod/${BOUND_POD_NAME}" -n "${AGENTCUBE_NAMESPACE}" --timeout=90s; then
            echo "Error: bound Pod ${BOUND_POD_NAME} was not garbage-collected" >&2
            kubectl get pod "${BOUND_POD_NAME}" -n "${AGENTCUBE_NAMESPACE}" -o yaml >&2 || true
            exit 1
        fi
        echo "Original Sandbox ${BOUND_SANDBOX_NAME} (${BOUND_SANDBOX_UID}) and Pod ${BOUND_POD_NAME} (${BOUND_POD_UID}) were garbage-collected"

        echo "Verifying claim deletion did not disturb the idle pool member..."
        POST_GC_IDLE_UID=$(kubectl get sandbox "${PRE_UPGRADE_POOL_MEMBER_NAME}" -n "${AGENTCUBE_NAMESPACE}" -o jsonpath='{.metadata.uid}' 2>/dev/null || true)
        POST_GC_IDLE_OWNER_UID=$(kubectl get sandbox "${PRE_UPGRADE_POOL_MEMBER_NAME}" -n "${AGENTCUBE_NAMESPACE}" -o jsonpath='{.metadata.ownerReferences[?(@.controller==true)].uid}' 2>/dev/null || true)
        if [ "${POST_GC_IDLE_UID}" != "${PRE_UPGRADE_POOL_MEMBER_UID}" ] || [ "${POST_GC_IDLE_OWNER_UID}" != "${WARM_POOL_UID}" ]; then
            echo "Error: claim garbage collection changed the independent idle pool lineage" >&2
            exit 1
        fi

        echo "Deleting the preserved idle member to causally exercise v0.5.3 pool refill..."
        kubectl delete sandbox "${PRE_UPGRADE_POOL_MEMBER_NAME}" -n "${AGENTCUBE_NAMESPACE}"
        if ! kubectl wait --for=delete "sandbox/${PRE_UPGRADE_POOL_MEMBER_NAME}" -n "${AGENTCUBE_NAMESPACE}" --timeout=90s; then
            echo "Error: idle pool member ${PRE_UPGRADE_POOL_MEMBER_NAME} was not deleted" >&2
            exit 1
        fi

        for i in $(seq 1 60); do
            READY_REPLICAS=$(kubectl get sandboxwarmpool e2e-upgrade-warmpool -n "${AGENTCUBE_NAMESPACE}" -o jsonpath='{.status.readyReplicas}' 2>/dev/null || true)
            CURRENT_POOL_SELECTOR=$(kubectl get sandboxwarmpool e2e-upgrade-warmpool -n "${AGENTCUBE_NAMESPACE}" -o jsonpath='{.status.selector}' 2>/dev/null || true)
            CURRENT_POOL_MEMBER_COUNT=0
            NEW_POOL_MEMBER_NAME=""
            if [ -n "${CURRENT_POOL_SELECTOR}" ]; then
                CURRENT_POOL_MEMBER_COUNT=$(kubectl get sandboxes -n "${AGENTCUBE_NAMESPACE}" -l "${CURRENT_POOL_SELECTOR}" -o name 2>/dev/null | wc -l | tr -d '[:space:]' || true)
                NEW_POOL_MEMBER_NAME=$(kubectl get sandboxes -n "${AGENTCUBE_NAMESPACE}" -l "${CURRENT_POOL_SELECTOR}" -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)
            fi

            if [ "${READY_REPLICAS:-0}" -eq 1 ] && [ "${CURRENT_POOL_MEMBER_COUNT:-0}" -eq 1 ] && [ "${CURRENT_POOL_SELECTOR}" = "${POOL_SELECTOR}" ] && [ -n "${NEW_POOL_MEMBER_NAME}" ] && [ "${NEW_POOL_MEMBER_NAME}" != "${PRE_UPGRADE_POOL_MEMBER_NAME}" ] && [ "${NEW_POOL_MEMBER_NAME}" != "${BOUND_SANDBOX_NAME}" ]; then
                NEW_POOL_MEMBER_UID=$(kubectl get sandbox "${NEW_POOL_MEMBER_NAME}" -n "${AGENTCUBE_NAMESPACE}" -o jsonpath='{.metadata.uid}' 2>/dev/null || true)
                NEW_POOL_OWNER_KIND=$(kubectl get sandbox "${NEW_POOL_MEMBER_NAME}" -n "${AGENTCUBE_NAMESPACE}" -o jsonpath='{.metadata.ownerReferences[?(@.controller==true)].kind}' 2>/dev/null || true)
                NEW_POOL_OWNER_NAME=$(kubectl get sandbox "${NEW_POOL_MEMBER_NAME}" -n "${AGENTCUBE_NAMESPACE}" -o jsonpath='{.metadata.ownerReferences[?(@.controller==true)].name}' 2>/dev/null || true)
                NEW_POOL_OWNER_UID=$(kubectl get sandbox "${NEW_POOL_MEMBER_NAME}" -n "${AGENTCUBE_NAMESPACE}" -o jsonpath='{.metadata.ownerReferences[?(@.controller==true)].uid}' 2>/dev/null || true)
                NEW_POOL_LAUNCH_TYPE=$(kubectl get sandbox "${NEW_POOL_MEMBER_NAME}" -n "${AGENTCUBE_NAMESPACE}" -o jsonpath='{.metadata.labels.agents\.x-k8s\.io/launch-type}' 2>/dev/null || true)
                NEW_POOL_MEMBER_READY=$(kubectl get sandbox "${NEW_POOL_MEMBER_NAME}" -n "${AGENTCUBE_NAMESPACE}" -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null || true)
                if [ -n "${NEW_POOL_MEMBER_UID}" ] && [ "${NEW_POOL_MEMBER_UID}" != "${PRE_UPGRADE_POOL_MEMBER_UID}" ] && [ "${NEW_POOL_MEMBER_UID}" != "${BOUND_SANDBOX_UID}" ] && [ "${NEW_POOL_OWNER_KIND}" = "SandboxWarmPool" ] && [ "${NEW_POOL_OWNER_NAME}" = "e2e-upgrade-warmpool" ] && [ "${NEW_POOL_OWNER_UID}" = "${WARM_POOL_UID}" ] && [ "${NEW_POOL_LAUNCH_TYPE}" = "warm" ] && [ "${NEW_POOL_MEMBER_READY}" = "True" ]; then
                    echo "v0.5.3 refilled e2e-upgrade-warmpool with exact replacement ${NEW_POOL_MEMBER_NAME} (${NEW_POOL_MEMBER_UID})"
                    break
                fi
            fi
            if [ "$i" -eq 60 ]; then
                echo "Error: v0.5.3 did not refill e2e-upgrade-warmpool with an exact Ready replacement" >&2
                kubectl get sandboxwarmpool e2e-upgrade-warmpool -n "${AGENTCUBE_NAMESPACE}" -o yaml >&2 || true
                kubectl get sandboxes -n "${AGENTCUBE_NAMESPACE}" -o yaml >&2 || true
                kubectl logs -n "${CONTROLLER_NAMESPACE}" deployment/agent-sandbox-controller --tail=100 >&2 || true
                exit 1
            fi
            sleep 2
        done
    fi


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
    echo "Skipping setup phase (E2E_SKIP_SETUP=true)"
    echo "Assuming cluster '${E2E_CLUSTER_NAME}' is already running with deployed services..."
    echo "Using namespace: ${AGENTCUBE_NAMESPACE}"
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
# We are currently in project root, sdk-python is at ./sdk-python
pip install -e ./sdk-python
pip install -e ./integrations/code-interpreter-mcp
pip install -e ./integrations/langchain-agentcube

# Check if agentcube package is available after installation
require_python

# Run tests with error handling to collect logs on failure
TEST_FAILED=0

echo "Running Go tests..."
# When SPIRE/mTLS is active, direct-WM tests skip because the test client has no client cert.
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
cd "$(dirname "$0")"

if ! WORKLOAD_MANAGER_URL="http://localhost:${WORKLOAD_MANAGER_LOCAL_PORT}" \
   ROUTER_URL="http://localhost:${ROUTER_LOCAL_PORT}" \
   MTLS_ENABLED="${MTLS_ENABLED}" \
   OIDC_ENABLED="${OIDC_ENABLED:-false}" \
   KEYCLOAK_TOKEN_URL="${KEYCLOAK_TOKEN_URL:-}" \
   API_TOKEN=$API_TOKEN \
   AGENTCUBE_NAMESPACE="${WORKLOAD_NAMESPACE}" \
   "$E2E_VENV_DIR/bin/python" test_codeinterpreter.py; then
    TEST_FAILED=1
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
if ! WORKLOAD_MANAGER_URL="http://localhost:${WORKLOAD_MANAGER_LOCAL_PORT}" ROUTER_URL="http://localhost:${ROUTER_LOCAL_PORT}" MTLS_ENABLED="${MTLS_ENABLED}" API_TOKEN=$API_TOKEN AGENTCUBE_NAMESPACE="${AGENTCUBE_NAMESPACE}" "$E2E_VENV_DIR/bin/python" test_langchain_agentcube_sandbox.py; then
    TEST_FAILED=1
fi

echo "Running Python Code Interpreter MCP tests (streamable-http, local subprocess)..."
if ! WORKLOAD_MANAGER_URL="http://localhost:${WORKLOAD_MANAGER_LOCAL_PORT}" ROUTER_URL="http://localhost:${ROUTER_LOCAL_PORT}" MTLS_ENABLED="${MTLS_ENABLED}" API_TOKEN=$API_TOKEN AGENTCUBE_NAMESPACE="${WORKLOAD_NAMESPACE}" "$E2E_VENV_DIR/bin/python" test_mcp_code_interpreter.py; then
    TEST_FAILED=1
fi

echo "Running Python Code Interpreter MCP stdio tests..."
if ! WORKLOAD_MANAGER_URL="http://localhost:${WORKLOAD_MANAGER_LOCAL_PORT}" ROUTER_URL="http://localhost:${ROUTER_LOCAL_PORT}" MTLS_ENABLED="${MTLS_ENABLED}" API_TOKEN=$API_TOKEN AGENTCUBE_NAMESPACE="${WORKLOAD_NAMESPACE}" "$E2E_VENV_DIR/bin/python" test_mcp_code_interpreter_stdio.py; then
    TEST_FAILED=1
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
            TEST_FAILED=1
        fi
    fi
fi



# Collect logs if tests failed
if [ $TEST_FAILED -eq 1 ]; then
    echo "Tests failed, collecting component logs..."
    collect_component_logs
    exit 1
fi

echo "All tests passed!"
