#!/bin/bash
set -e

# Configuration
E2E_CLUSTER_NAME=${E2E_CLUSTER_NAME:-agentcube-e2e}
AGENT_SANDBOX_REPO=${AGENT_SANDBOX_REPO:-https://github.com/kubernetes-sigs/agent-sandbox.git}
AGENT_SANDBOX_VERSION=${AGENT_SANDBOX_VERSION:-main}
APISERVER_IMAGE=${APISERVER_IMAGE:-workloadmanager:latest}
SANDBOX_IMAGE=${SANDBOX_IMAGE:-sandbox:latest}

# Function to clean up
cleanup() {
    echo "Cleaning up..."
    if [ -n "$PID" ]; then
        echo "Stopping port forward..."
        kill $PID || true
    fi
}

# Register cleanup on exit
trap cleanup EXIT

echo "Starting E2E tests..."

echo "1. Creating Kind cluster..."
kind create cluster --name "${E2E_CLUSTER_NAME}" || true

echo "2. Installing agent-sandbox..."

AGENT_SANDBOX_VERSION="v0.1.0"

# To install only the core components:
kubectl apply -f https://github.com/kubernetes-sigs/agent-sandbox/releases/download/${AGENT_SANDBOX_VERSION}/manifest.yaml

# To install the extensions components:
kubectl apply -f https://github.com/kubernetes-sigs/agent-sandbox/releases/download/${AGENT_SANDBOX_VERSION}/extensions.yaml

echo "3. Building images..."
# We assume we are in the project root
make docker-build
make sandbox-build

echo "4. Loading images into Kind..."
kind load docker-image "${APISERVER_IMAGE}" --name "${E2E_CLUSTER_NAME}"
kind load docker-image "${SANDBOX_IMAGE}" --name "${E2E_CLUSTER_NAME}"

# echo "5. Deploying workloadmanager..."
# kubectl apply -f k8s/workloadmanager.yaml

# echo "6. Waiting for deployment..."
# kubectl wait --for=condition=available --timeout=300s deployment/workloadmanager -n agentcube

# echo "7. Creating ServiceAccount and Token..."
# kubectl create serviceaccount e2e-test -n agentcube || true
# kubectl create clusterrolebinding e2e-test-binding --clusterrole=workloadmanager --serviceaccount=agentcube:e2e-test || true

# echo "8. Running tests..."
# # Create token
# API_TOKEN=$(kubectl create token e2e-test -n agentcube --duration=24h)
# echo "Token created"

# # Port forward in background
# kubectl port-forward svc/workloadmanager -n agentcube 8080:8080 > /dev/null 2>&1 &
# PID=$!
# echo "Port forward started with PID $PID"

# # Wait for port-forward to be ready
# echo "Waiting for port-forward..."
# for i in $(seq 1 10); do
#     if curl -sf -o /dev/null http://localhost:8080/health; then
#         echo "Port-forward is ready."
#         break
#     fi
#     if [ $i -eq 10 ]; then
#         echo "Timed out waiting for port-forward." >&2
#         exit 1
#     fi
#     sleep 1
# done

# # Run tests
# echo "Running Go tests..."
# API_URL=http://localhost:8080 API_TOKEN=$API_TOKEN go test -v ./test/e2e/...
