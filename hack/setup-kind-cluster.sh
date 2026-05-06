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
REDIS_IMAGE=${REDIS_IMAGE:-redis:7-alpine}
KIND_CONFIG_FILE=${KIND_CONFIG_FILE:-}

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

# Function to check if cluster exists
cluster_exists() {
    kind get clusters 2>/dev/null | grep -q "^${CLUSTER_NAME}$"
}

# Function to create Kind cluster
create_cluster() {
    log_info "Creating Kind cluster: ${CLUSTER_NAME}"

    if cluster_exists; then
        log_warn "Cluster '${CLUSTER_NAME}' already exists"
        read -rp "Do you want to delete and recreate it? [y/N]: " response
        if [[ "$response" =~ ^[Yy]$ ]]; then
            log_info "Deleting existing cluster..."
            kind delete cluster --name "${CLUSTER_NAME}"
        else
            log_info "Using existing cluster"
            return 0
        fi
    fi

    if [[ -n "${KIND_CONFIG_FILE}" && -f "${KIND_CONFIG_FILE}" ]]; then
        log_info "Using Kind config file: ${KIND_CONFIG_FILE}"
        kind create cluster --name "${CLUSTER_NAME}" --config "${KIND_CONFIG_FILE}"
    else
        # Create cluster with default config optimized for AgentCube
        cat <<EOF | kind create cluster --name "${CLUSTER_NAME}" --config -
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
- role: control-plane
  kubeadmConfigPatches:
  - |
    kind: InitConfiguration
    nodeRegistration:
      kubeletExtraArgs:
        node-labels: "ingress-ready=true"
  extraPortMappings:
  - containerPort: 80
    hostPort: 80
    protocol: TCP
  - containerPort: 443
    hostPort: 443
    protocol: TCP
  - containerPort: 30080
    hostPort: 30080
    protocol: TCP
  - containerPort: 30443
    hostPort: 30443
    protocol: TCP
- role: worker
- role: worker
EOF
    fi

    log_success "Kind cluster '${CLUSTER_NAME}' created successfully"
}

# Function to install agent-sandbox
install_agent_sandbox() {
    log_info "Installing agent-sandbox ${AGENT_SANDBOX_VERSION}..."

    # Pre-pull images to avoid timeout issues
    local images=(
        "registry.k8s.io/agent-sandbox/agent-sandbox-controller:${AGENT_SANDBOX_VERSION}"
        "python:3.9-slim"
    )

    for image in "${images[@]}"; do
        log_info "Pulling image: ${image}"
        if docker pull "${image}" 2>/dev/null; then
            log_info "Loading ${image} into Kind cluster..."
            kind load docker-image "${image}" --name "${CLUSTER_NAME}" 2>/dev/null || log_warn "Failed to load ${image}, will pull from registry"
        else
            log_warn "Failed to pull ${image}, will attempt to pull from registry in cluster"
        fi
    done

    # Apply agent-sandbox manifests
    log_info "Applying agent-sandbox manifests..."
    kubectl apply --validate=false -f "https://github.com/kubernetes-sigs/agent-sandbox/releases/download/${AGENT_SANDBOX_VERSION}/manifest.yaml"
    kubectl apply --validate=false -f "https://github.com/kubernetes-sigs/agent-sandbox/releases/download/${AGENT_SANDBOX_VERSION}/extensions.yaml"

    log_success "Agent-sandbox installed successfully"
}

# Function to deploy Redis
deploy_redis() {
    log_info "Deploying Redis..."

    # Create namespace if not exists
    kubectl get namespace "${AGENTCUBE_NAMESPACE}" >/dev/null 2>&1 || kubectl create namespace "${AGENTCUBE_NAMESPACE}"

    # Pull and load Redis image
    log_info "Pulling Redis image: ${REDIS_IMAGE}"
    docker pull "${REDIS_IMAGE}" 2>/dev/null || log_warn "Failed to pull Redis image"
    kind load docker-image "${REDIS_IMAGE}" --name "${CLUSTER_NAME}" 2>/dev/null || log_warn "Failed to load Redis image"

    # Deploy Redis
    kubectl -n "${AGENTCUBE_NAMESPACE}" create deployment redis \
        --image="${REDIS_IMAGE}" \
        --port=6379 \
        --dry-run=client -o yaml | kubectl apply --validate=false -f -

    kubectl -n "${AGENTCUBE_NAMESPACE}" expose deployment redis \
        --port=6379 \
        --target-port=6379 \
        --name=redis \
        --dry-run=client -o yaml | kubectl apply --validate=false -f -

    # Wait for Redis to be ready
    log_info "Waiting for Redis to be ready..."
    kubectl -n "${AGENTCUBE_NAMESPACE}" rollout status deployment/redis --timeout=180s

    # Verify Redis is responding
    for i in {1..30}; do
        if kubectl exec -n "${AGENTCUBE_NAMESPACE}" deployment/redis -- redis-cli ping 2>/dev/null | grep -q "PONG"; then
            log_success "Redis is ready and responding"
            return 0
        fi
        sleep 2
    done

    log_error "Redis failed to become ready"
    return 1
}

# Function to verify cluster is ready
verify_cluster() {
    log_info "Verifying cluster status..."

    # Check nodes
    log_info "Cluster nodes:"
    kubectl get nodes -o wide

    # Check system pods
    log_info "System pods status:"
    kubectl get pods -n kube-system

    # Check agent-sandbox pods
    log_info "Agent-sandbox pods status:"
    kubectl get pods -n agent-sandbox 2>/dev/null || log_warn "agent-sandbox namespace not found"

    log_success "Cluster verification complete"
}

# Main function
main() {
    log_info "Setting up Kind cluster for AgentCube development..."

    # Check prerequisites
    require_cmd kind
    require_cmd kubectl
    require_cmd docker

    # Create cluster
    create_cluster

    # Set kubectl context to the new cluster
    kubectl config use-context "kind-${CLUSTER_NAME}"

    # Install agent-sandbox
    install_agent_sandbox

    # Deploy Redis
    deploy_redis

    # Verify cluster
    verify_cluster

    log_success "Kind cluster setup complete!"
    log_info "Cluster name: ${CLUSTER_NAME}"
    log_info "Namespace: ${AGENTCUBE_NAMESPACE}"
    log_info ""
    log_info "To use this cluster:"
    log_info "  kubectl config use-context kind-${CLUSTER_NAME}"
    log_info ""
    log_info "To deploy AgentCube components:"
    log_info "  make docker-build docker-build-router docker-build-picod"
    log_info "  make kind-load kind-load-router"
    log_info "  helm upgrade --install agentcube manifests/charts/base --namespace ${AGENTCUBE_NAMESPACE} --create-namespace"
    log_info ""
    log_info "To delete the cluster:"
    log_info "  kind delete cluster --name ${CLUSTER_NAME}"
}

# Handle script arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --name)
            CLUSTER_NAME="$2"
            shift 2
            ;;
        --sandbox-version)
            AGENT_SANDBOX_VERSION="$2"
            shift 2
            ;;
        --namespace)
            AGENTCUBE_NAMESPACE="$2"
            shift 2
            ;;
        --config)
            KIND_CONFIG_FILE="$2"
            shift 2
            ;;
        --skip-agent-sandbox)
            SKIP_AGENT_SANDBOX=true
            shift
            ;;
        --skip-redis)
            SKIP_REDIS=true
            shift
            ;;
        --help|-h)
            cat <<EOF
Usage: $0 [OPTIONS]

Setup a Kind cluster for AgentCube development.

Options:
  --name NAME              Cluster name (default: agentcube)
  --sandbox-version VER    Agent-sandbox version (default: v0.1.1)
  --namespace NS           Namespace for AgentCube (default: agentcube)
  --config FILE            Kind configuration file
  --skip-agent-sandbox     Skip agent-sandbox installation
  --skip-redis             Skip Redis deployment
  -h, --help               Show this help message

Environment Variables:
  CLUSTER_NAME             Cluster name
  AGENT_SANDBOX_VERSION    Agent-sandbox version
  AGENTCUBE_NAMESPACE      Namespace for AgentCube
  REDIS_IMAGE              Redis Docker image
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
