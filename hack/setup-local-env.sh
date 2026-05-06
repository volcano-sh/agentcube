#!/bin/bash
# Copyright 2025 The Volcano Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

set -euo pipefail
IFS=$'\n\t'

# Configuration
CLUSTER_NAME=${CLUSTER_NAME:-agentcube}
AGENT_SANDBOX_VERSION=${AGENT_SANDBOX_VERSION:-v0.1.1}
AGENTCUBE_NAMESPACE=${AGENTCUBE_NAMESPACE:-agentcube}
WORKLOAD_MANAGER_IMAGE=${WORKLOAD_MANAGER_IMAGE:-workloadmanager:latest}
ROUTER_IMAGE=${ROUTER_IMAGE:-agentcube-router:latest}
PICOD_IMAGE=${PICOD_IMAGE:-picod:latest}
SKIP_BUILD=${SKIP_BUILD:-false}
SKIP_SETUP=${SKIP_SETUP:-false}
WORKLOAD_MANAGER_PORT=${WORKLOAD_MANAGER_PORT:-8080}
ROUTER_PORT=${ROUTER_PORT:-8081}

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

log_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

log_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

require_cmd() {
    command -v "$1" >/dev/null 2>&1 || {
        log_error "Missing required command: $1"
        exit 1
    }
}

# Function to setup Kind cluster
setup_kind_cluster() {
    log_info "Setting up Kind cluster..."

    if [[ -f "hack/setup-kind-cluster.sh" ]]; then
        bash hack/setup-kind-cluster.sh --name "${CLUSTER_NAME}" \
            --sandbox-version "${AGENT_SANDBOX_VERSION}" \
            --namespace "${AGENTCUBE_NAMESPACE}"
    else
        log_error "hack/setup-kind-cluster.sh not found"
        exit 1
    fi
}

# Function to build Docker images
build_images() {
    if [[ "${SKIP_BUILD}" == "true" ]]; then
        log_info "Skipping Docker image build (SKIP_BUILD=true)"
        return 0
    fi

    log_info "Building AgentCube Docker images..."

    make docker-build
    make docker-build-router
    make docker-build-picod

    log_success "Docker images built successfully"
}

# Function to load images into Kind
load_images() {
    log_info "Loading Docker images into Kind cluster..."

    kind load docker-image "${WORKLOAD_MANAGER_IMAGE}" --name "${CLUSTER_NAME}"
    kind load docker-image "${ROUTER_IMAGE}" --name "${CLUSTER_NAME}"
    kind load docker-image "${PICOD_IMAGE}" --name "${CLUSTER_NAME}"

    log_success "Images loaded successfully"
}

# Function to deploy AgentCube via Helm
deploy_agentcube() {
    log_info "Deploying AgentCube components..."

    # Prepare extra environment variables as JSON for Helm
    local WM_EXTRA_ENV='[{"name":"REDIS_PASSWORD_REQUIRED","value":"false"},{"name":"JWT_KEY_SECRET_NAMESPACE","value":"'"${AGENTCUBE_NAMESPACE}"'"}]'
    local ROUTER_EXTRA_ENV='[{"name":"REDIS_PASSWORD_REQUIRED","value":"false"}]'

    # Install using Helm
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

    # Wait for deployments to be ready
    log_info "Waiting for deployments to be ready..."
    kubectl -n "${AGENTCUBE_NAMESPACE}" rollout status deployment/workloadmanager --timeout=300s
    kubectl -n "${AGENTCUBE_NAMESPACE}" rollout status deployment/agentcube-router --timeout=300s

    log_success "AgentCube deployed successfully"
}

# Function to create test resources
create_test_resources() {
    log_info "Creating test resources..."

    # Create ServiceAccount for testing
    kubectl create serviceaccount e2e-test -n "${AGENTCUBE_NAMESPACE}" 2>/dev/null || log_warn "ServiceAccount e2e-test already exists"

    # Create ClusterRoleBinding
    kubectl create clusterrolebinding e2e-test-binding \
        --clusterrole=workloadmanager \
        --serviceaccount="${AGENTCUBE_NAMESPACE}:e2e-test" 2>/dev/null || log_warn "ClusterRoleBinding already exists"

    # Create test AgentRuntimes if they exist
    if [[ -f "test/e2e/echo_agent.yaml" ]]; then
        kubectl apply --validate=false -f test/e2e/echo_agent.yaml
        log_info "Created echo-agent"
    fi

    if [[ -f "test/e2e/e2e_code_interpreter.yaml" ]]; then
        kubectl apply --validate=false -f test/e2e/e2e_code_interpreter.yaml
        log_info "Created e2e-code-interpreter"
    fi

    log_success "Test resources created"
}

# Function to setup port forwarding
setup_port_forwarding() {
    log_info "Setting up port forwarding..."

    # Kill any existing port-forward processes
    pkill -f "kubectl port-forward.*${AGENTCUBE_NAMESPACE}" 2>/dev/null || true
    sleep 2

    # Port forward workload manager
    log_info "Forwarding workloadmanager port 8080 to localhost:${WORKLOAD_MANAGER_PORT}..."
    kubectl port-forward svc/workloadmanager -n "${AGENTCUBE_NAMESPACE}" "${WORKLOAD_MANAGER_PORT}:8080" &
    local workload_pid=$!

    # Port forward router
    log_info "Forwarding router port 8080 to localhost:${ROUTER_PORT}..."
    kubectl port-forward svc/agentcube-router -n "${AGENTCUBE_NAMESPACE}" "${ROUTER_PORT}:8080" &
    local router_pid=$!

    # Wait for port-forwards to be ready
    sleep 3

    # Check if port-forwards are working
    local max_attempts=10
    local attempt=1
    local workload_ready=false
    local router_ready=false

    while [[ $attempt -le $max_attempts ]]; do
        if curl -sf -o /dev/null "http://localhost:${WORKLOAD_MANAGER_PORT}/health" 2>/dev/null; then
            workload_ready=true
        fi
        if curl -sf -o /dev/null "http://localhost:${ROUTER_PORT}/health/live" 2>/dev/null; then
            router_ready=true
        fi

        if [[ "$workload_ready" == "true" && "$router_ready" == "true" ]]; then
            log_success "Port forwarding is ready"
            break
        fi

        log_info "Waiting for port-forwards to be ready (attempt $attempt/$max_attempts)..."
        sleep 2
        ((attempt++))
    done

    if [[ "$workload_ready" != "true" || "$router_ready" != "true" ]]; then
        log_error "Port forwarding failed to start"
        kill $workload_pid 2>/dev/null || true
        kill $router_pid 2>/dev/null || true
        return 1
    fi

    # Save port-forward PIDs for cleanup
    echo $workload_pid > /tmp/agentcube-workload-portforward.pid
    echo $router_pid > /tmp/agentcube-router-portforward.pid

    log_info "Port forwarding PIDs: workloadmanager=$workload_pid, router=$router_pid"
}

# Function to print usage information
print_usage() {
    log_info "Local development environment is ready!"
    echo ""
    echo "========================================="
    echo "AgentCube Local Development Environment"
    echo "========================================="
    echo ""
    echo "Cluster:        kind-${CLUSTER_NAME}"
    echo "Namespace:      ${AGENTCUBE_NAMESPACE}"
    echo ""
    echo "Services:"
    echo "  Workload Manager: http://localhost:${WORKLOAD_MANAGER_PORT}"
    echo "  Router:           http://localhost:${ROUTER_PORT}"
    echo ""
    echo "Environment Variables:"
    echo "  export WORKLOAD_MANAGER_URL=http://localhost:${WORKLOAD_MANAGER_PORT}"
    echo "  export ROUTER_URL=http://localhost:${ROUTER_PORT}"
    echo "  export API_TOKEN=\$(kubectl create token e2e-test -n ${AGENTCUBE_NAMESPACE} --duration=24h)"
    echo ""
    echo "Useful Commands:"
    echo "  kubectl get pods -n ${AGENTCUBE_NAMESPACE}"
    echo "  kubectl logs -n ${AGENTCUBE_NAMESPACE} deployment/workloadmanager"
    echo "  kubectl logs -n ${AGENTCUBE_NAMESPACE} deployment/agentcube-router"
    echo ""
    echo "Stop port forwarding:"
    echo "  kill \$(cat /tmp/agentcube-workload-portforward.pid) \$(cat /tmp/agentcube-router-portforward.pid)"
    echo ""
    echo "Delete cluster:"
    echo "  kind delete cluster --name ${CLUSTER_NAME}"
    echo ""
}

# Main function
main() {
    log_info "Setting up local development environment for AgentCube..."

    # Check prerequisites
    require_cmd kind
    require_cmd kubectl
    require_cmd docker
    require_cmd helm
    require_cmd curl
    require_cmd make

    if [[ "${SKIP_SETUP}" != "true" ]]; then
        # Setup Kind cluster
        setup_kind_cluster

        # Build images
        build_images

        # Load images into Kind
        load_images

        # Deploy AgentCube
        deploy_agentcube

        # Create test resources
        create_test_resources
    else
        log_info "Skipping setup phase (SKIP_SETUP=true)"
    fi

    # Setup port forwarding
    setup_port_forwarding

    # Print usage
    print_usage
}

# Handle script arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --name)
            CLUSTER_NAME="$2"
            shift 2
            ;;
        --namespace)
            AGENTCUBE_NAMESPACE="$2"
            shift 2
            ;;
        --sandbox-version)
            AGENT_SANDBOX_VERSION="$2"
            shift 2
            ;;
        --skip-build)
            SKIP_BUILD=true
            shift
            ;;
        --skip-setup)
            SKIP_SETUP=true
            shift
            ;;
        --workload-port)
            WORKLOAD_MANAGER_PORT="$2"
            shift 2
            ;;
        --router-port)
            ROUTER_PORT="$2"
            shift 2
            ;;
        --help|-h)
            cat <<EOF
Usage: $0 [OPTIONS]

Setup a complete local development environment for AgentCube.

Options:
  --name NAME              Cluster name (default: agentcube)
  --namespace NS           Namespace for AgentCube (default: agentcube)
  --sandbox-version VER    Agent-sandbox version (default: v0.1.1)
  --skip-build             Skip building Docker images
  --skip-setup             Skip Kind cluster setup and deployment
  --workload-port PORT     Port for workload manager forwarding (default: 8080)
  --router-port PORT       Port for router forwarding (default: 8081)
  -h, --help               Show this help message

Environment Variables:
  CLUSTER_NAME             Cluster name
  AGENT_SANDBOX_VERSION    Agent-sandbox version
  AGENTCUBE_NAMESPACE      Namespace for AgentCube
  SKIP_BUILD               Skip Docker image build if set to "true"
  SKIP_SETUP               Skip setup if set to "true"
  WORKLOAD_MANAGER_PORT    Port for workload manager forwarding
  ROUTER_PORT              Port for router forwarding

Examples:
  # Full setup
  $0

  # Skip build (use existing images)
  $0 --skip-build

  # Only setup port forwarding on existing cluster
  $0 --skip-setup

  # Use different ports
  $0 --workload-port 9090 --router-port 9091
EOF
            exit 0
            ;;
        *)
            log_error "Unknown option: $1"
            exit 1
            ;;
    esac
done

main
