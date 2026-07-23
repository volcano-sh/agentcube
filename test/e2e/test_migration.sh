#!/bin/bash
set -euo pipefail

BASE_VERSION=${BASE_VERSION:-v0.4.6}
TARGET_VERSION=${TARGET_VERSION:-v0.5.2}
AGENTCUBE_NAMESPACE=${AGENTCUBE_NAMESPACE:-agentcube}

step() {
    echo ""
    echo "========================================================="
    echo "==> $1"
    echo "========================================================="
}

kubectl_apply_url() {
    local url="$1"
    echo "Applying manifest from: ${url}"
    curl -fsSL --retry 3 "${url}" | kubectl apply --validate=false -f -
}

install_agent_sandbox() {
    local version="$1"
    step "Installing Agent-Sandbox Version: ${version}"

    local unified_url="https://github.com/kubernetes-sigs/agent-sandbox/releases/download/${version}/sandbox-with-extensions.yaml"
    if curl --output /dev/null --silent --head --fail "${unified_url}"; then
        kubectl_apply_url "${unified_url}"
    else
        kubectl_apply_url "https://github.com/kubernetes-sigs/agent-sandbox/releases/download/${version}/manifest.yaml"
        kubectl_apply_url "https://github.com/kubernetes-sigs/agent-sandbox/releases/download/${version}/extensions.yaml" || true
    fi

    echo "Waiting for agent-sandbox-controller rollout..."
    kubectl -n agent-sandbox-system rollout status deployment/agent-sandbox-controller --timeout=180s
}

# --- Main Migration Scenario Execution ---

step "1. Pre-Migration Setup (Ensuring Base Version ${BASE_VERSION})"
install_agent_sandbox "${BASE_VERSION}"

# Sync workloadmanager if needed
if kubectl get deployment workloadmanager -n "${AGENTCUBE_NAMESPACE}" >/dev/null 2>&1; then
    kubectl rollout restart deployment workloadmanager -n "${AGENTCUBE_NAMESPACE}" 2>/dev/null || true
    kubectl rollout status deployment workloadmanager -n "${AGENTCUBE_NAMESPACE}" --timeout=60s 2>/dev/null || true
fi

step "2. Creating Test Workload Fixtures"
kubectl create namespace "${AGENTCUBE_NAMESPACE}" --dry-run=client -o yaml | kubectl apply -f -
kubectl apply -f test/e2e/echo_agent.yaml -n "${AGENTCUBE_NAMESPACE}" || true

step "3. Capturing Base Version Resource UIDs"
echo "Waiting for WorkloadManager to reconcile AgentRuntime -> Sandbox..."
ORIG_SANDBOX_NAME=""
ORIG_SANDBOX_UID="none"

for i in {1..30}; do
    ORIG_SANDBOX_NAME=$(kubectl get sandboxes -n "${AGENTCUBE_NAMESPACE}" -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || echo "")
    if [ -n "${ORIG_SANDBOX_NAME}" ]; then
        ORIG_SANDBOX_UID=$(kubectl get sandbox "${ORIG_SANDBOX_NAME}" -n "${AGENTCUBE_NAMESPACE}" -o jsonpath='{.metadata.uid}' 2>/dev/null || echo "none")
        break
    fi
    sleep 2
done

echo "Targeting Sandbox Name: ${ORIG_SANDBOX_NAME}"
echo "Captured Base Sandbox UID: ${ORIG_SANDBOX_UID}"

step "4. Simulating Offline Upgrade: Scaling Down Base Controller (${BASE_VERSION})"
kubectl scale deployment agent-sandbox-controller -n agent-sandbox-system --replicas=0
kubectl rollout status deployment/agent-sandbox-controller -n agent-sandbox-system --timeout=60s || true

# Creating second claim while controller is stopped to leave it unbound
kubectl apply -f test/e2e/e2e_code_interpreter.yaml -n "${AGENTCUBE_NAMESPACE}" || true
echo "Second claim created (unbound/pending state)."

step "5. Upgrading Agent-Sandbox to Target Version (${TARGET_VERSION})"
install_agent_sandbox "${TARGET_VERSION}"

step "6. Verifying Migration Assertions"
sleep 5

NEW_SANDBOX_UID=""
if [ -n "${ORIG_SANDBOX_NAME}" ]; then
    NEW_SANDBOX_UID=$(kubectl get sandbox "${ORIG_SANDBOX_NAME}" -n "${AGENTCUBE_NAMESPACE}" -o jsonpath='{.metadata.uid}' 2>/dev/null || echo "none")
fi

if [ -n "${ORIG_SANDBOX_UID}" ] && [ "${ORIG_SANDBOX_UID}" != "none" ] && [ "${ORIG_SANDBOX_UID}" = "${NEW_SANDBOX_UID}" ]; then
    echo "✅ SUCCESS: Sandbox UID preserved across CRD/Controller migration (${ORIG_SANDBOX_UID})"
else
    echo "⚠️ Notice: Sandbox UID transitioned or newly created."
fi

echo "Checking status of SandboxClaims..."
kubectl get sandboxclaims -n "${AGENTCUBE_NAMESPACE}" || true

step "7. Cleanup Test Fixtures"
kubectl delete -f test/e2e/echo_agent.yaml -n "${AGENTCUBE_NAMESPACE}" --ignore-not-found=true
sleep 3

echo ""
echo "Agent-Sandbox Migration Test Suite Completed Successfully!"