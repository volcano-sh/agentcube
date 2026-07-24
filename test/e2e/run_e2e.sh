#!/bin/bash
set -euo pipefail
IFS=$'\n\t'

# Prevent Docker attestation metadata bugs during Kind image load
export BUILDX_NO_DEFAULT_ATTESTATION=${BUILDX_NO_DEFAULT_ATTESTATION:-1}
export DOCKER_BUILDKIT=${DOCKER_BUILDKIT:-1}

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
AGENT_SANDBOX_VERSION=${AGENT_SANDBOX_VERSION:-v0.5.2}
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
<<<<<<< HEAD
E2E_RUN_AGENT_SANDBOX_UPGRADE_TEST=${E2E_RUN_AGENT_SANDBOX_UPGRADE_TEST:-true}
=======
## By default we disable the historical v0.4.6->v0.5.2 upgrade/migration test
## AgentCube requires v1beta1 types in its compiled client; running the
## in-place upgrade fixture against a v0.4.6 controller is no longer valid
## for normal CI runs. Set this to "true" to opt into the migration test.
E2E_RUN_AGENT_SANDBOX_UPGRADE_TEST=${E2E_RUN_AGENT_SANDBOX_UPGRADE_TEST:-false}

if [[ "${AGENT_SANDBOX_VERSION}" == "v0.4.6" && "${E2E_RUN_AGENT_SANDBOX_UPGRADE_TEST}" != "true" ]]; then
    echo "Error: AGENT_SANDBOX_VERSION=v0.4.6 is not valid unless E2E_RUN_AGENT_SANDBOX_UPGRADE_TEST=true." >&2
    echo "This script now defaults to agent-sandbox v0.5.2 for normal E2E runs." >&2
    exit 1
fi

echo "Resolved AGENT_SANDBOX_VERSION=${AGENT_SANDBOX_VERSION}"
>>>>>>> 66e057f (update e2e.yml)

_SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" && pwd)"
REPO_ROOT="$(cd "$_SCRIPT_DIR/../.." && pwd)"

# Images that need to be pre-pulled and loaded into kind cluster
PRE_PULL_IMAGES=(
    "registry.k8s.io/agent-sandbox/agent-sandbox-controller:${AGENT_SANDBOX_VERSION}"
    "python:3.9-slim"
)

WORKLOAD_MANAGER_LOCAL_PORT=${WORKLOAD_MANAGER_LOCAL_PORT:-8080}
ROUTER_LOCAL_PORT=${ROUTER_LOCAL_PORT:-8081}

# Artifacts path for collecting logs on test failure
ARTIFACTS_PATH=${ARTIFACTS_PATH:-"${PWD}/e2e-logs"}

cleanup() {
    echo "Cleaning up..."

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

    kubectl delete deployment agentcube-code-interpreter-mcp -n "${AGENTCUBE_NAMESPACE:-agentcube}" --ignore-not-found=true 2>/dev/null || true

    echo "Killing any remaining kubectl port-forward processes..."
    pkill -f "kubectl port-forward" 2>/dev/null || true

    sleep 2

    echo "Force killing any processes using ports 8080-8081 and MCP_K8S_LOCAL_PORT..."
    for port in 8080 8081 "${MCP_K8S_LOCAL_PORT:-19446}"; do
        if command -v lsof >/dev/null 2>&1 && lsof -i :$port >/dev/null 2>&1; then
            lsof -ti :$port | xargs kill -9 2>/dev/null || true
        elif command -v netstat >/dev/null 2>&1 && netstat -tulpn 2>/dev/null | grep ":$port " >/dev/null; then
            netstat -tulpn 2>/dev/null | grep ":$port " | awk '{print $7}' | cut -d'/' -f1 | xargs kill -9 2>/dev/null || true
        fi
    done

    if [ -d "${E2E_VENV_DIR:-}" ]; then
        echo "Removing Python virtual environment..."
        rm -rf "$E2E_VENV_DIR" || true
    fi

    rm -f /tmp/workload_port_forward.log /tmp/router_port_forward.log 2>/dev/null || true
}

trap cleanup EXIT

require_cmd() {
    command -v "$1" >/dev/null 2>&1 || {
        echo "Missing required command: $1" >&2
        exit 1
    }
}

require_python() {
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
        kubectl -n "${AGENTCUBE_NAMESPACE}" get pods -o wide > "${artifacts_dir}/${component_name}-all-pods.txt" 2>&1 || true
    fi
}

collect_component_logs() {
    local artifacts_dir="${ARTIFACTS_PATH}"
    echo "Collecting component logs to ${artifacts_dir}..."
    mkdir -p "${artifacts_dir}"

    collect_pod_logs "app=workloadmanager" "workloadmanager" "${artifacts_dir}"
    collect_pod_logs "app=agentcube-router" "router" "${artifacts_dir}"
    collect_pod_logs "app=agentcube-code-interpreter-mcp" "code-interpreter-mcp" "${artifacts_dir}"

    echo "Collecting sandbox pods logs..."
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
                    > "${artifacts_dir}/sandbox-${pod}-${c}.log" 2>&1 || true
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
    if ! kind load docker-image "${image}" --name "${E2E_CLUSTER_NAME}"; then
        echo "Warning: Failed to load image ${image} into Kind. Will attempt to pull from registry." >&2
        return 0
    fi
}

cleanup_old_agent_sandbox_install() {
    echo "Cleaning up any old agent-sandbox installation..."
    kubectl delete namespace agent-sandbox-system --ignore-not-found=true --wait=true --timeout=120s 2>/dev/null || true
    kubectl delete crd sandboxclaims.extensions.agents.x-k8s.io sandboxes.agents.x-k8s.io \
        sandboxtemplates.extensions.agents.x-k8s.io sandboxwarmpools.extensions.agents.x-k8s.io \
        --ignore-not-found=true 2>/dev/null || true
}

validate_agent_sandbox_crd_version() {
    local expected_version="v1beta1"
    local crds=(
        sandboxclaims.extensions.agents.x-k8s.io
        sandboxes.agents.x-k8s.io
        sandboxtemplates.extensions.agents.x-k8s.io
        sandboxwarmpools.extensions.agents.x-k8s.io
    )

    for crd in "${crds[@]}"; do
        if ! kubectl get crd "${crd}" >/dev/null 2>&1; then
            echo "Error: expected CRD ${crd} is missing." >&2
            exit 1
        fi
        if ! kubectl get crd "${crd}" -o jsonpath='{.spec.versions[*].name}' | grep -qw "${expected_version}"; then
            echo "Error: CRD ${crd} does not expose expected version ${expected_version}." >&2
            kubectl get crd "${crd}" -o yaml 2>/dev/null || true
            exit 1
        fi
    done
}

curl_download() {
    local url="$1"
    local out="$2"
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

    docker_pull_if_missing "${REDIS_IMAGE}"
    kind_load_image "${REDIS_IMAGE}"

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
<<<<<<< HEAD
=======
    cleanup_old_agent_sandbox_install

>>>>>>> 66e057f (update e2e.yml)
    if [[ "${AGENT_SANDBOX_VERSION}" == "v0.5.2"* ]]; then
        kubectl_apply_url "https://github.com/kubernetes-sigs/agent-sandbox/releases/download/${AGENT_SANDBOX_VERSION}/sandbox-with-extensions.yaml"
    else
        kubectl_apply_url "https://github.com/kubernetes-sigs/agent-sandbox/releases/download/${AGENT_SANDBOX_VERSION}/manifest.yaml"
        kubectl_apply_url "https://github.com/kubernetes-sigs/agent-sandbox/releases/download/${AGENT_SANDBOX_VERSION}/extensions.yaml"
    fi
    verify_agent_sandbox_controller
    validate_agent_sandbox_crd_version

    step "Building images..."
    make docker-build
    make docker-build-router
    make docker-build-picod

    step "Loading images into Kind..."
    kind load docker-image "${WORKLOAD_MANAGER_IMAGE}" --name "${E2E_CLUSTER_NAME}"
    kind load docker-image "${ROUTER_IMAGE}" --name "${E2E_CLUSTER_NAME}"
    kind load docker-image "${PICOD_IMAGE}" --name "${E2E_CLUSTER_NAME}"

    deploy_redis

    step "Waiting for Redis to be ready..."
    kubectl -n "${AGENTCUBE_NAMESPACE}" rollout status deployment/redis --timeout=120s

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

    step "Deploying AgentCube via Helm..."
    WM_EXTRA_ENV=$(printf '[{"name":"REDIS_PASSWORD_REQUIRED","value":"false"},{"name":"JWT_KEY_SECRET_NAMESPACE","value":"%s"}]' "${AGENTCUBE_NAMESPACE}")
    ROUTER_EXTRA_ENV='[{"name":"REDIS_PASSWORD_REQUIRED","value":"false"}]'

    if [ "${MTLS_ENABLED}" = "true" ]; then
        step "Installing SPIRE CRDs..."
        kubectl apply -k "https://github.com/spiffe/spire-controller-manager/config/crd?ref=v0.6.4"
    fi

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
    apply_workload_fixture test/e2e/echo_agent.yaml
    tmp_ttl_agent=$(mktemp)
    sed 's/name: echo-agent/name: echo-agent-short-ttl/; s/app: echo-agent/app: echo-agent-short-ttl/; s/sessionTimeout: "15m"/sessionTimeout: "30s"/' test/e2e/echo_agent.yaml > "$tmp_ttl_agent"
    apply_workload_fixture "$tmp_ttl_agent"
    rm -f "$tmp_ttl_agent"

    step "Creating test CodeInterpreter..."
    apply_workload_fixture test/e2e/e2e_code_interpreter.yaml

    step "Waiting for AgentRuntimes to be ready..."
    kubectl get agentruntime echo-agent -n "${WORKLOAD_NAMESPACE}" -o jsonpath='{.metadata.name}{"\n"}' || echo "echo-agent may still be starting..."
    kubectl get agentruntime echo-agent-short-ttl -n "${WORKLOAD_NAMESPACE}" -o jsonpath='{.metadata.name}{"\n"}' || echo "echo-agent-short-ttl may still be starting..."
    echo "AgentRuntimes created, waiting for pods to be ready..."
    sleep 10
}

echo "Starting E2E tests..."

if [ "${E2E_SKIP_SETUP}" = "true" ]; then
    echo "Skipping setup phase (E2E_SKIP_SETUP=true)"
else
    run_setup
fi

step "Pre-cleanup"
pkill -f "kubectl port-forward" 2>/dev/null || true
for port in 8080 8081 "${MCP_K8S_LOCAL_PORT:-19446}" 19245; do
    if command -v lsof > /dev/null 2>&1 && lsof -i :$port > /dev/null 2>&1; then
        lsof -ti :$port | xargs kill -9 2>/dev/null || true
    fi
done
rm -f /tmp/workload_port_forward.log /tmp/router_port_forward.log 2>/dev/null || true
sleep 2

step "Running tests..."
API_TOKEN=$(kubectl create token e2e-test -n "${AGENTCUBE_NAMESPACE}" --duration=24h)

echo "Starting workload manager port-forward..."
kubectl port-forward svc/workloadmanager -n "${AGENTCUBE_NAMESPACE}" "${WORKLOAD_MANAGER_LOCAL_PORT}:8080" > /tmp/workload_port_forward.log 2>&1 &
WORKLOAD_PID=$!

echo "Starting router port-forward..."
kubectl port-forward svc/agentcube-router -n "${AGENTCUBE_NAMESPACE}" "${ROUTER_LOCAL_PORT}:8080" > /tmp/router_port_forward.log 2>&1 &
ROUTER_PID=$!
sleep 3

if [ ! -d "$E2E_VENV_DIR" ]; then
    python3 -m venv "$E2E_VENV_DIR"
fi

source "$E2E_VENV_DIR/bin/activate"
pip install --upgrade pip
pip install -e ./sdk-python
pip install -e ./integrations/code-interpreter-mcp
pip install -e ./integrations/langchain-agentcube

require_python

TEST_FAILED=0

echo "Running Go tests..."
if ! WORKLOAD_MANAGER_URL="http://localhost:${WORKLOAD_MANAGER_LOCAL_PORT}" \
   ROUTER_URL="http://localhost:${ROUTER_LOCAL_PORT}" \
   MTLS_ENABLED="${MTLS_ENABLED}" \
   WORKLOAD_NAMESPACE="${WORKLOAD_NAMESPACE}" \
   API_TOKEN=$API_TOKEN \
   go test -v ./test/e2e/...; then
    TEST_FAILED=1
fi

echo "Running Python CodeInterpreter tests..."
cd "$(dirname "$0")"
if ! WORKLOAD_MANAGER_URL="http://localhost:${WORKLOAD_MANAGER_LOCAL_PORT}" \
   ROUTER_URL="http://localhost:${ROUTER_LOCAL_PORT}" \
   MTLS_ENABLED="${MTLS_ENABLED}" \
   API_TOKEN=$API_TOKEN \
   AGENTCUBE_NAMESPACE="${WORKLOAD_NAMESPACE}" \
   "$E2E_VENV_DIR/bin/python" test_codeinterpreter.py; then
    TEST_FAILED=1
fi

# ==============================================================================
# Agent-Sandbox Upgrade & Migration Test Block
# ==============================================================================
if [ $TEST_FAILED -eq 0 ] && [ "${E2E_RUN_AGENT_SANDBOX_UPGRADE_TEST}" = "true" ]; then
    step "Running Agent-Sandbox Upgrade & Migration Test (v0.4.6 -> v0.5.2)..."
    UPGRADE_FAILED=0
    NEW_VERSION="v0.5.2"
    
    echo "1. Creating 1st CodeInterpreter claim (upgrade-ci-1)..."
    cat <<EOF | kubectl apply -n "${WORKLOAD_NAMESPACE}" -f -
apiVersion: runtime.agentcube.volcano.sh/v1alpha1
kind: CodeInterpreter
metadata:
  name: upgrade-ci-1
spec:
  warmPoolSize: 1
  minReplicas: 0
  maxReplicas: 2
EOF
    
    echo "Waiting for upgrade-ci-1 SandboxClaim to become Bound..."
    SB_CLAIM_1=""
    for i in {1..30}; do
        SB_CLAIM_1=$(kubectl get sandboxclaim -n "${WORKLOAD_NAMESPACE}" -o json 2>/dev/null | jq -r '.items[] | select(.metadata.ownerReferences[0].name=="upgrade-ci-1") | .metadata.name' || true)
        if [ -n "$SB_CLAIM_1" ]; then
            if kubectl get sandboxclaim "$SB_CLAIM_1" -n "${WORKLOAD_NAMESPACE}" | grep -q Bound; then
                break
            fi
        fi
        sleep 2
    done
    
    if [ -z "$SB_CLAIM_1" ]; then
        echo "Error: upgrade-ci-1 SandboxClaim was not created."
        UPGRADE_FAILED=1
    fi

    if [ $UPGRADE_FAILED -eq 0 ]; then
        echo "2. Capturing Sandbox and Pod UIDs for upgrade-ci-1..."
        SB_NAME_1=$(kubectl get sandboxclaim "${SB_CLAIM_1}" -n "${WORKLOAD_NAMESPACE}" -o jsonpath='{.status.sandboxName}')
        OLD_SB_UID=$(kubectl get sandbox "${SB_NAME_1}" -n "${WORKLOAD_NAMESPACE}" -o jsonpath='{.metadata.uid}')
        OLD_POD_UID=$(kubectl get pod "${SB_NAME_1}-0" -n "${WORKLOAD_NAMESPACE}" -o jsonpath='{.metadata.uid}' 2>/dev/null || echo "")

        echo "Captured UIDs -> Sandbox: ${OLD_SB_UID}, Pod: ${OLD_POD_UID}"

        echo "3. Stopping v0.4.6 agent-sandbox-controller..."
        kubectl scale deployment agent-sandbox-controller -n agent-sandbox-system --replicas=0
        kubectl -n agent-sandbox-system rollout status deployment/agent-sandbox-controller --timeout=60s || true

        echo "4. Creating 2nd CodeInterpreter claim (upgrade-ci-2) while controller is down..."
        cat <<EOF | kubectl apply -n "${WORKLOAD_NAMESPACE}" -f -
apiVersion: runtime.agentcube.volcano.sh/v1alpha1
kind: CodeInterpreter
metadata:
  name: upgrade-ci-2
spec:
  warmPoolSize: 1
  minReplicas: 0
  maxReplicas: 2
EOF

        echo "5. Upgrading agent-sandbox manifests to ${NEW_VERSION}..."
        # Note: v0.5.2 renamed manifest.yaml -> sandbox-with-extensions.yaml
        kubectl_apply_url "https://github.com/kubernetes-sigs/agent-sandbox/releases/download/${NEW_VERSION}/sandbox-with-extensions.yaml"
        kubectl scale deployment agent-sandbox-controller -n agent-sandbox-system --replicas=1

        echo "Waiting for v0.5.2 controller & webhook readiness..."
        kubectl -n agent-sandbox-system rollout status deployment/agent-sandbox-controller --timeout=300s

        echo "6. Verifying UID preservation for upgrade-ci-1..."
        NEW_SB_UID=$(kubectl get sandbox "${SB_NAME_1}" -n "${WORKLOAD_NAMESPACE}" -o jsonpath='{.metadata.uid}')
        NEW_POD_UID=$(kubectl get pod "${SB_NAME_1}-0" -n "${WORKLOAD_NAMESPACE}" -o jsonpath='{.metadata.uid}' 2>/dev/null || echo "")

        if [ "$OLD_SB_UID" != "$NEW_SB_UID" ]; then
            echo "Verification Failed: Sandbox UID changed! (${OLD_SB_UID} != ${NEW_SB_UID})"
            UPGRADE_FAILED=1
        else
            echo "Sandbox UID preserved successfully."
        fi

        if [ -n "$OLD_POD_UID" ] && [ "$OLD_POD_UID" != "$NEW_POD_UID" ]; then
            echo "Verification Failed: Pod UID changed! (${OLD_POD_UID} != ${NEW_POD_UID})"
            UPGRADE_FAILED=1
        else
            echo "Pod UID preserved successfully."
        fi

        echo "7. Verifying unbound claim (upgrade-ci-2) becomes Ready..."
        SB_CLAIM_2=""
        UNBOUND_READY=false
        for i in {1..30}; do
            SB_CLAIM_2=$(kubectl get sandboxclaim -n "${WORKLOAD_NAMESPACE}" -o json 2>/dev/null | jq -r '.items[] | select(.metadata.ownerReferences[0].name=="upgrade-ci-2") | .metadata.name' || true)
            if [ -n "$SB_CLAIM_2" ]; then
                if kubectl get sandboxclaim "$SB_CLAIM_2" -n "${WORKLOAD_NAMESPACE}" | grep -q Bound; then
                    UNBOUND_READY=true
                    break
                fi
            fi
            sleep 3
        done

        if [ "$UNBOUND_READY" = "true" ]; then
            echo "Unbound claim (upgrade-ci-2) became Bound/Ready."
        else
            echo "Verification Failed: upgrade-ci-2 did not become Bound."
            UPGRADE_FAILED=1
        fi

        echo "8. Verifying Garbage Collection on claim deletion..."
        kubectl delete codeinterpreter upgrade-ci-1 -n "${WORKLOAD_NAMESPACE}"
        
        GC_SUCCESS=false
        for i in {1..30}; do
            if ! kubectl get sandbox "${SB_NAME_1}" -n "${WORKLOAD_NAMESPACE}" >/dev/null 2>&1; then
                GC_SUCCESS=true
                break
            fi
            sleep 2
        done

        if [ "$GC_SUCCESS" = "true" ]; then
            echo "Claim deletion correctly garbage-collected Sandbox (${SB_NAME_1})."
        else
            echo "Verification Failed: Sandbox ${SB_NAME_1} was not garbage collected."
            UPGRADE_FAILED=1
        fi
    fi

    if [ $UPGRADE_FAILED -ne 0 ]; then
        echo "Agent-Sandbox Upgrade Test FAILED!"
        TEST_FAILED=1
    fi
fi

# Exit with non-zero if any test block failed
if [ $TEST_FAILED -eq 1 ]; then
    echo "Tests failed, collecting component logs..."
    collect_component_logs
    exit 1
fi

echo "All tests passed successfully!"
