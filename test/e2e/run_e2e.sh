#!/bin/bash
set -e

# Configuration
E2E_CLUSTER_NAME=${E2E_CLUSTER_NAME:-agentcube-e2e}
AGENT_SANDBOX_REPO=${AGENT_SANDBOX_REPO:-https://github.com/kubernetes-sigs/agent-sandbox.git}
AGENT_SANDBOX_VERSION=${AGENT_SANDBOX_VERSION:-main}
APISERVER_IMAGE=${APISERVER_IMAGE:-agentcube-apiserver:latest}
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
rm -rf /tmp/agent-sandbox
git clone --depth 1 --branch "${AGENT_SANDBOX_VERSION}" "${AGENT_SANDBOX_REPO}" /tmp/agent-sandbox

# Build agent-sandbox-controller image
echo "Building agent-sandbox-controller image..."
docker build -t agent-sandbox-controller:latest -f /tmp/agent-sandbox/images/agent-sandbox-controller/Dockerfile /tmp/agent-sandbox
kind load docker-image agent-sandbox-controller:latest --name "${E2E_CLUSTER_NAME}"

kubectl apply -f /tmp/agent-sandbox/k8s/crds
kubectl apply -f /tmp/agent-sandbox/k8s/rbac.generated.yaml

# Patch controller deployment to use local image
echo "Patching agent-sandbox-controller deployment..."
sed -i 's|ko://sigs.k8s.io/agent-sandbox/cmd/agent-sandbox-controller|agent-sandbox-controller:latest|g' /tmp/agent-sandbox/k8s/controller.yaml
# Append imagePullPolicy: IfNotPresent after the image line
sed -i '/image: agent-sandbox-controller:latest/a \        imagePullPolicy: IfNotPresent' /tmp/agent-sandbox/k8s/controller.yaml

kubectl apply -f /tmp/agent-sandbox/k8s/controller.yaml

echo "3. Building images..."
# We assume we are in the project root
make docker-build
make sandbox-build

echo "4. Loading images into Kind..."
kind load docker-image "${APISERVER_IMAGE}" --name "${E2E_CLUSTER_NAME}"
kind load docker-image "${SANDBOX_IMAGE}" --name "${E2E_CLUSTER_NAME}"

echo "5. Deploying agentcube-apiserver..."
kubectl apply -f k8s/agentcube-apiserver.yaml

echo "6. Waiting for deployment..."
kubectl wait --for=condition=available --timeout=300s deployment/agentcube-apiserver -n agentcube

echo "7. Creating ServiceAccount and Token..."
kubectl create serviceaccount e2e-test -n agentcube || true
kubectl create clusterrolebinding e2e-test-binding --clusterrole=agentcube-apiserver --serviceaccount=agentcube:e2e-test || true

echo "8. Running tests..."
# Create token
API_TOKEN=$(kubectl create token e2e-test -n agentcube --duration=24h)
echo "Token created"

# Port forward in background
kubectl port-forward svc/agentcube-apiserver -n agentcube 8080:8080 > /dev/null 2>&1 &
PID=$!
echo "Port forward started with PID $PID"

# Wait for port-forward to be ready
echo "Waiting for port-forward..."
for i in $(seq 1 10); do
    if curl -sf -o /dev/null http://localhost:8080/health; then
        echo "Port-forward is ready."
        break
    fi
    if [ $i -eq 10 ]; then
        echo "Timed out waiting for port-forward." >&2
        exit 1
    fi
    sleep 1
done

# Run tests
echo "Running Go tests..."
API_URL=http://localhost:8080 API_TOKEN=$API_TOKEN go test -v ./test/e2e/...
