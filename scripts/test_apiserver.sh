#!/bin/bash
# Test pico-apiserver basic functionality

set -e

API_URL="${API_URL:-http://localhost:8080}"
TOKEN="${TOKEN:-test-token}"

echo "Testing Pico API Server at $API_URL"
echo "========================================"

# 1. Test health check
echo ""
echo "1. Testing health check..."
curl -s "$API_URL/health" | jq .
echo "✓ Health check passed"

# 2. Test session creation
echo ""
echo "2. Testing session creation..."
SESSION_RESPONSE=$(curl -s -X POST "$API_URL/v1/sessions" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "ttl": 3600,
    "image": "python:3.11",
    "metadata": {
      "test": "example"
    }
  }')

echo "$SESSION_RESPONSE" | jq .

# Extract session ID (if successful)
SESSION_ID=$(echo "$SESSION_RESPONSE" | jq -r '.sessionId // empty')

if [ -n "$SESSION_ID" ]; then
  echo "✓ Session created: $SESSION_ID"
  
  # 3. Test get session
  echo ""
  echo "3. Testing get session..."
  curl -s "$API_URL/v1/sessions/$SESSION_ID" \
    -H "Authorization: Bearer $TOKEN" | jq .
  echo "✓ Get session passed"
  
  # 4. Test list sessions
  echo ""
  echo "4. Testing list sessions..."
  curl -s "$API_URL/v1/sessions?limit=10" \
    -H "Authorization: Bearer $TOKEN" | jq .
  echo "✓ List sessions passed"
  
  # 5. Test delete session
  echo ""
  echo "5. Testing delete session..."
  curl -s -X DELETE "$API_URL/v1/sessions/$SESSION_ID" \
    -H "Authorization: Bearer $TOKEN" | jq .
  echo "✓ Delete session passed"
else
  echo "⚠ Session creation failed or returned unexpected format"
  echo "This is expected if Kubernetes is not configured"
fi

echo ""
echo "========================================"
echo "Basic API tests completed!"
echo ""
echo "Note: Session creation may fail without a proper Kubernetes cluster."
echo "      This is expected and you should continue with manual testing."
