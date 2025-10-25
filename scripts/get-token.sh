#!/bin/bash
# Get pico-apiserver service account token
# Usage: ./scripts/get-token.sh [namespace]
# Default namespace is pico

NAMESPACE="${1:-pico}"
SERVICE_ACCOUNT="pico-apiserver"

echo "Getting service account token..."
echo "Namespace: $NAMESPACE"
echo "Service Account: $SERVICE_ACCOUNT"
echo ""

# Check if service account exists
if ! kubectl get serviceaccount "$SERVICE_ACCOUNT" -n "$NAMESPACE" &> /dev/null; then
    echo "ERROR: Service account '$SERVICE_ACCOUNT' does not exist in namespace '$NAMESPACE'"
    echo "Please deploy pico-apiserver first: kubectl apply -f k8s/pico-apiserver.yaml"
    exit 1
fi

# Get token
TOKEN=$(kubectl create token "$SERVICE_ACCOUNT" -n "$NAMESPACE" --duration=24h 2>/dev/null)

if [ -z "$TOKEN" ]; then
    echo "ERROR: Failed to create token"
    echo "Please ensure you have sufficient permissions to create tokens"
    exit 1
fi

echo "Token retrieved successfully!"
echo ""
echo "Use the following command for API calls:"
echo ""
echo "export PICO_TOKEN='$TOKEN'"
echo ""
echo "Example: Create session"
echo "curl -X POST http://localhost:8080/v1/sessions \\"
echo "  -H 'Authorization: Bearer \$PICO_TOKEN' \\"
echo "  -H 'Content-Type: application/json' \\"
echo "  -d '{\"image\": \"your-sandbox-image:latest\", \"ttl\": 3600}'"
echo ""
echo "Note: This token is valid for 24 hours"
