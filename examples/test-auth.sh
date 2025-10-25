#!/bin/bash
# pico-apiserver authentication test suite

set -e

NAMESPACE="${PICO_NAMESPACE:-pico}"
API_URL="${PICO_API_URL:-http://localhost:8080}"

echo "============================================"
echo "Pico-APIServer Authentication Tests"
echo "============================================"
echo "Namespace: $NAMESPACE"
echo "API URL: $API_URL"
echo ""

# Check if kubectl is available
if ! command -v kubectl &> /dev/null; then
    echo "ERROR: kubectl is not installed or not in PATH"
    exit 1
fi

# Check if service account exists
echo "1. Checking service account..."
if ! kubectl get serviceaccount pico-apiserver -n "$NAMESPACE" &> /dev/null; then
    echo "   ❌ Service account 'pico-apiserver' does not exist"
    echo "   Please deploy pico-apiserver: kubectl apply -f k8s/pico-apiserver.yaml"
    exit 1
fi
echo "   ✅ Service account exists"
echo ""

# Get token
echo "2. Getting service account token..."
TOKEN=$(kubectl create token pico-apiserver -n "$NAMESPACE" --duration=1h 2>/dev/null)
if [ -z "$TOKEN" ]; then
    echo "   ❌ Failed to create token"
    exit 1
fi
echo "   ✅ Token retrieved successfully"
echo ""

# Test 1: Access without token (should fail)
echo "3. Test 1: Access without token (expected to fail)..."
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" \
    -X GET "$API_URL/v1/sessions")
if [ "$HTTP_CODE" = "401" ]; then
    echo "   ✅ Correctly returned 401 Unauthorized"
else
    echo "   ❌ Expected 401, got $HTTP_CODE"
fi
echo ""

# Test 2: Access with invalid token format (should fail)
echo "4. Test 2: Access with invalid token format (expected to fail)..."
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" \
    -H "Authorization: InvalidToken" \
    -X GET "$API_URL/v1/sessions")
if [ "$HTTP_CODE" = "401" ]; then
    echo "   ✅ Correctly returned 401 Unauthorized"
else
    echo "   ❌ Expected 401, got $HTTP_CODE"
fi
echo ""

# Test 3: Access with valid token (should succeed)
echo "5. Test 3: Access with valid token (expected to succeed)..."
RESPONSE=$(curl -s -w "\n%{http_code}" \
    -H "Authorization: Bearer $TOKEN" \
    -X GET "$API_URL/v1/sessions")

HTTP_CODE=$(echo "$RESPONSE" | tail -n 1)
BODY=$(echo "$RESPONSE" | head -n -1)

if [ "$HTTP_CODE" = "200" ]; then
    echo "   ✅ Successfully accessed API (returned 200)"
    echo "   Response: $BODY"
else
    echo "   ❌ Expected 200, got $HTTP_CODE"
    echo "   Response: $BODY"
fi
echo ""

# Test 4: Health check endpoint (no authentication required)
echo "6. Test 4: Health check endpoint (no authentication required)..."
RESPONSE=$(curl -s -w "\n%{http_code}" \
    -X GET "$API_URL/health")

HTTP_CODE=$(echo "$RESPONSE" | tail -n 1)
BODY=$(echo "$RESPONSE" | head -n -1)

if [ "$HTTP_CODE" = "200" ]; then
    echo "   ✅ Health check OK (returned 200)"
    echo "   Response: $BODY"
else
    echo "   ❌ Health check failed (returned $HTTP_CODE)"
    echo "   Response: $BODY"
fi
echo ""

# Test 5: Try using default service account token (should fail)
echo "7. Test 5: Using default service account token (expected to fail)..."
DEFAULT_TOKEN=$(kubectl create token default -n "$NAMESPACE" --duration=1h 2>/dev/null)
if [ -n "$DEFAULT_TOKEN" ]; then
    HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" \
        -H "Authorization: Bearer $DEFAULT_TOKEN" \
        -X GET "$API_URL/v1/sessions")
    if [ "$HTTP_CODE" = "403" ]; then
        echo "   ✅ Correctly returned 403 Forbidden"
    else
        echo "   ⚠️  Expected 403, got $HTTP_CODE"
    fi
else
    echo "   ⚠️  Could not create default service account token, skipping this test"
fi
echo ""

echo "============================================"
echo "Tests completed!"
echo "============================================"
echo ""
echo "Use the following command to save token to environment variable:"
echo "export PICO_TOKEN='$TOKEN'"
echo ""
echo "Then you can create a session with:"
echo 'curl -X POST $PICO_API_URL/v1/sessions \'
echo '  -H "Authorization: Bearer $PICO_TOKEN" \'
echo '  -H "Content-Type: application/json" \'
echo '  -d '"'"'{"image": "your-sandbox-image:latest", "ttl": 3600}'"'"
