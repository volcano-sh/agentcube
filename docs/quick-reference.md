# Pico-APIServer Quick Reference

## Get Token

```bash
# Using script
./scripts/get-token.sh

# Manual (24-hour validity)
TOKEN=$(kubectl create token pico-apiserver -n pico --duration=24h)
```

## API Call Examples

### Set Environment Variables

```bash
export PICO_TOKEN='your-token-here'
export PICO_API='http://localhost:8080'
```

### Health Check (No Authentication Required)

```bash
curl $PICO_API/health
```

### Create Session

```bash
curl -X POST $PICO_API/v1/sessions \
  -H "Authorization: Bearer $PICO_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "image": "sandbox-image:latest",
    "ttl": 3600,
    "ssh_public_key": "ssh-rsa AAAAB3...",
    "metadata": {"key": "value"}
  }'
```

### List All Sessions

```bash
curl -X GET "$PICO_API/v1/sessions?limit=10&offset=0" \
  -H "Authorization: Bearer $PICO_TOKEN"
```

### Get Specific Session

```bash
SESSION_ID="your-session-id"
curl -X GET "$PICO_API/v1/sessions/$SESSION_ID" \
  -H "Authorization: Bearer $PICO_TOKEN"
```

### Delete Session

```bash
SESSION_ID="your-session-id"
curl -X DELETE "$PICO_API/v1/sessions/$SESSION_ID" \
  -H "Authorization: Bearer $PICO_TOKEN"
```

## Error Codes

| HTTP Status | Error Code            | Description                                    |
| ----------- | --------------------- | ---------------------------------------------- |
| 200         | -                     | Success                                        |
| 400         | INVALID_REQUEST       | Invalid request format                         |
| 400         | INVALID_TTL           | TTL not in allowed range (60-28800 sec)        |
| 401         | UNAUTHORIZED          | Missing, invalid, or expired token             |
| 403         | FORBIDDEN             | Valid token but service account not authorized |
| 404         | SESSION_NOT_FOUND     | Session not found or expired                   |
| 500         | SANDBOX_CREATE_FAILED | Failed to create Sandbox                       |
| 500         | SANDBOX_DELETE_FAILED | Failed to delete Sandbox                       |
| 500         | SANDBOX_TIMEOUT       | Sandbox creation timeout                       |

## Common Questions

### How to check if token is valid?

```bash
# Method 1: Verify using kubectl
kubectl auth can-i --list --token="$TOKEN"

# Method 2: Call API directly
curl -I $PICO_API/v1/sessions -H "Authorization: Bearer $TOKEN"
```

### Token expired?

Regenerate token:
```bash
TOKEN=$(kubectl create token pico-apiserver -n pico --duration=24h)
```

### How to view pico-apiserver logs?

```bash
kubectl logs -n pico deployment/pico-apiserver -f
```

### How to test authentication?

```bash
./examples/test-auth.sh
```

### How to disable authentication for development?

```bash
./pico-apiserver --port=8080 --namespace=default --disable-auth
```

**WARNING**: Never use `--disable-auth` in production!

## Deployment Commands

```bash
# Deploy
kubectl apply -f k8s/pico-apiserver.yaml

# Check status
kubectl get pods -n pico

# View logs
kubectl logs -n pico deployment/pico-apiserver -f

# Delete
kubectl delete -f k8s/pico-apiserver.yaml
```

## Session TTL Range

- Minimum: 60 seconds (1 minute)
- Maximum: 28800 seconds (8 hours)
- Default: 3600 seconds (1 hour)

## Required Permissions

The pico-apiserver service account needs:
- `agents.x-k8s.io/sandboxes`: get, list, watch, create, update, patch, delete
- `pods`: get, list, watch
- `authentication.k8s.io/tokenreviews`: create

## Configuration Flags

```
--port=8080              API server port
--namespace=default      Kubernetes namespace for Sandbox CRDs
--ssh-username=sandbox   Default SSH username
--ssh-port=22           SSH port on sandbox pods
--enable-tls=false      Enable TLS/HTTPS
--tls-cert=             Path to TLS certificate file
--tls-key=              Path to TLS private key file
--disable-auth=false    Disable authentication (development only)
```
