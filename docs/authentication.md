# Pico-APIServer Authentication Guide

## Overview

Pico-apiserver uses Kubernetes Service Account-based authentication. All protected API endpoints (such as creating, querying, and deleting sessions) require a valid service account token.

## Authentication Mechanism

### How It Works

1. Client provides Bearer token in the HTTP `Authorization` header
2. Pico-apiserver validates the token using the Kubernetes TokenReview API
3. Verifies the token corresponds to the `pico-apiserver` service account
4. If validation passes, access is granted; otherwise returns 401 or 403 error

### Supported Service Accounts

Pico-apiserver accepts tokens from the following service accounts:
- `system:serviceaccount:pico:pico-apiserver` (recommended)
- `system:serviceaccount:<namespace>:pico-apiserver` (where namespace is where pico-apiserver is deployed)

## Getting a Token

### Method 1: Using the Provided Script

```bash
# Get token (default namespace is pico)
./scripts/get-token.sh

# Specify different namespace
./scripts/get-token.sh my-namespace
```

The script will output the token and usage examples.

### Method 2: Manual Token Generation

```bash
# Create a token with 24-hour validity
kubectl create token pico-apiserver -n pico --duration=24h

# Or create token with longer validity (max 8760h = 365 days)
kubectl create token pico-apiserver -n pico --duration=8760h
```

## Using the Token

### Set Environment Variable

```bash
export PICO_TOKEN='<your-token-here>'
```

### API Call Examples

#### 1. Create Session

```bash
curl -X POST http://localhost:8080/v1/sessions \
  -H "Authorization: Bearer $PICO_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "image": "your-sandbox-image:latest",
    "ttl": 3600,
    "metadata": {
      "purpose": "testing"
    }
  }'
```

#### 2. List Sessions

```bash
curl -X GET "http://localhost:8080/v1/sessions?limit=10&offset=0" \
  -H "Authorization: Bearer $PICO_TOKEN"
```

#### 3. Get Specific Session

```bash
curl -X GET "http://localhost:8080/v1/sessions/<session-id>" \
  -H "Authorization: Bearer $PICO_TOKEN"
```

#### 4. Delete Session

```bash
curl -X DELETE "http://localhost:8080/v1/sessions/<session-id>" \
  -H "Authorization: Bearer $PICO_TOKEN"
```

## Disabling Authentication (Development Only)

For development purposes, you can disable authentication using the `--disable-auth` flag:

```bash
./pico-apiserver --port=8080 --namespace=default --disable-auth
```

**WARNING**: 
- This should **NEVER** be used in production environments
- All API endpoints become publicly accessible without any authentication
- Only use this for local development or testing

When authentication is disabled, you'll see warning messages in the logs:
```
WARNING: Authentication is disabled. This should only be used in development environments!
WARNING: Authentication is disabled - allowing unauthenticated request
```

## Error Handling

### 401 Unauthorized

Reasons:
- Missing `Authorization` header
- Invalid token format (not Bearer token)
- Invalid or expired token
- Token validation failed

Solutions:
- Ensure correct token format: `Authorization: Bearer <token>`
- Regenerate token

### 403 Forbidden

Reasons:
- Token is valid but the service account is not authorized

Solutions:
- Use token generated from `pico-apiserver` service account
- Ensure correct namespace

## Security Recommendations

1. **Token Storage**: Keep tokens secure, never commit them to version control
2. **Token Rotation**: Rotate tokens regularly (recommend short expiration times)
3. **Principle of Least Privilege**: Ensure pico-apiserver service account has only necessary permissions
4. **TLS Encryption**: Enable TLS in production to prevent token interception
5. **Network Isolation**: Use NetworkPolicy to restrict access to pico-apiserver
6. **Never Disable Auth in Production**: The `--disable-auth` flag is for development only

## Permission Configuration

The pico-apiserver service account requires the following permissions:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: pico-apiserver
rules:
  # Manage Sandbox CRDs
  - apiGroups: ["agents.x-k8s.io"]
    resources: ["sandboxes"]
    verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
  
  # Query Pod status
  - apiGroups: [""]
    resources: ["pods"]
    verbs: ["get", "list", "watch"]
  
  # Validate tokens (required for authentication)
  - apiGroups: ["authentication.k8s.io"]
    resources: ["tokenreviews"]
    verbs: ["create"]
```

These permissions are already included in the `k8s/pico-apiserver.yaml` configuration file.

## Development Environment Notes

When running pico-apiserver outside the cluster (e.g., local development), you need:

1. Configure proper kubeconfig
2. Ensure permission to access Kubernetes API
3. Can use any valid service account token for testing
4. Or use `--disable-auth` for quick testing (no token needed)

## Troubleshooting

### How to verify if token is valid?

```bash
kubectl create token pico-apiserver -n pico --duration=1h | \
  kubectl auth can-i --token="$(cat)" --list
```

### How to view token details?

Note: Kubernetes service account tokens are in JWT format, you can decode them (view claims only, not verifying signature):

```bash
# Decode token payload
echo '<token>' | cut -d'.' -f2 | base64 -d 2>/dev/null | jq .
```

### View Logs

Pico-apiserver logs authentication-related events:

```bash
# View pico-apiserver logs
kubectl logs -n pico deployment/pico-apiserver -f
```

Successful authentication log example:
```
Authenticated request from service account: system:serviceaccount:pico:pico-apiserver
```

Failed authentication log examples:
```
Token validation error: ...
Unauthorized service account: ...
WARNING: Authentication is disabled - allowing unauthenticated request
```

## Testing Authentication

Run the authentication test suite:

```bash
export PICO_API_URL="http://localhost:8080"
./examples/test-auth.sh
```

The test script will verify:
- Requests without token are rejected (401)
- Requests with invalid token format are rejected (401)
- Requests with valid pico-apiserver token are accepted (200)
- Requests with other service account tokens are rejected (403)
- Health endpoint is accessible without authentication (200)
