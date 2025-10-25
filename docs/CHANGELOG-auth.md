# Authentication Feature Changelog

## Overview

Implemented Kubernetes Service Account Token-based authentication mechanism, ensuring only clients with valid pico-apiserver service account tokens can access protected API endpoints.

## Modified Files

### Core Code

1. **pkg/pico-apiserver/auth.go**
   - Implemented `validateServiceAccountToken()` method using Kubernetes TokenReview API for token validation
   - Implemented `isAuthorizedServiceAccount()` method to check if service account is authorized
   - Updated `authMiddleware()` to integrate token validation logic
   - Added support for disabling authentication via `DisableAuth` config flag
   - Added detailed logging for debugging and monitoring

2. **pkg/pico-apiserver/config.go**
   - Added `DisableAuth` field for optional authentication disabling (development only)

3. **cmd/pico-apiserver/main.go**
   - Added `--disable-auth` command-line parameter
   - Updated configuration initialization code
   - Added warning when authentication is disabled

4. **k8s/pico-apiserver.yaml**
   - Added `authentication.k8s.io/tokenreviews` create permission to ClusterRole
   - This permission is used to call TokenReview API for token validation

### New Files

1. **scripts/get-token.sh** - Convenience script for getting service account tokens
   - Automatically checks if service account exists
   - Generates token with 24-hour validity
   - Provides usage examples

2. **docs/authentication.md** - Detailed authentication documentation
   - Authentication mechanism explanation
   - Token acquisition and usage methods
   - API call examples
   - Security recommendations
   - Troubleshooting guide
   - Instructions for disabling authentication in development

3. **examples/test-auth.sh** - Authentication functionality test suite
   - Automated testing of various authentication scenarios
   - Validates correct authentication behavior
   - Provides clear test result output

4. **docs/quick-reference.md** - Quick reference guide
   - Common command cheat sheet
   - API call examples
   - Error code descriptions
   - FAQ

5. **README.md** - Main project documentation (significantly updated)
   - Added authentication feature description
   - Updated quick start guide
   - Added security recommendations
   - Added development mode instructions

## Authentication Workflow

1. Client sends Bearer Token in HTTP Header
   ```
   Authorization: Bearer <kubernetes-service-account-token>
   ```

2. Pico-apiserver receives request and extracts token

3. Calls Kubernetes TokenReview API to validate token
   ```go
   tokenReview := &authv1.TokenReview{
       Spec: authv1.TokenReviewSpec{
           Token: token,
       },
   }
   ```

4. Checks if token is authenticated

5. Verifies token corresponds to `pico-apiserver` service account
   - Supported format: `system:serviceaccount:pico:pico-apiserver`
   - Supported format: `system:serviceaccount:<namespace>:pico-apiserver`

6. If validation passes, allows access; otherwise returns 401 or 403

## Optional Authentication (Development Mode)

Authentication can be disabled for development purposes:

```bash
./pico-apiserver --port=8080 --namespace=default --disable-auth
```

When disabled:
- Warning messages appear in logs
- All API endpoints become accessible without tokens
- Should **NEVER** be used in production

## Security Improvements

1. **Native Kubernetes Authentication**: No longer depends on custom JWT secrets, uses Kubernetes native TokenReview API
2. **Fine-grained Permission Control**: Only specific service account tokens can access
3. **Audit Logging**: Records all authentication attempts, including successes and failures
4. **Token Expiration Management**: Leverages Kubernetes token expiration mechanism, supports short-lived tokens
5. **Principle of Least Privilege**: pico-apiserver granted only necessary permissions
6. **Optional for Development**: Can disable authentication for local testing

## Usage Examples

### Get Token

```bash
# Using script
./scripts/get-token.sh

# Manual
kubectl create token pico-apiserver -n pico --duration=24h
```

### API Call

```bash
export PICO_TOKEN='your-token-here'

curl -X POST http://localhost:8080/v1/sessions \
  -H "Authorization: Bearer $PICO_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"image": "sandbox:latest", "ttl": 3600}'
```

### Testing

```bash
./examples/test-auth.sh
```

### Development Mode

```bash
# Disable authentication for local development
./pico-apiserver --port=8080 --namespace=default --disable-auth
```

## Compatibility

- Kubernetes: v1.20+ (requires TokenReview API)
- Backward Compatibility: Existing deployments need to update configuration

## Migration Guide

No special migration needed. New `--disable-auth` flag is optional and defaults to false (authentication enabled).

## Future Improvements

1. Support whitelist of multiple service accounts
2. Add role-based access control (RBAC)
3. Implement request rate limiting
4. Add audit webhook integration
5. Support client certificate authentication as alternative
6. Add token caching for better performance

## Test Coverage

- ✅ Requests without token → 401 Unauthorized
- ✅ Requests with invalid token format → 401 Unauthorized
- ✅ Requests with valid pico-apiserver token → 200 OK
- ✅ Requests with other service account tokens → 403 Forbidden
- ✅ Health endpoint accessible without authentication → 200 OK
- ✅ Authentication can be disabled with --disable-auth flag

## Performance Impact

- TokenReview API calls add approximately 10-50ms latency (depending on Kubernetes API Server response time)
- Consider token caching mechanism for high-performance scenarios (future improvement)
- When authentication is disabled, no performance impact

## Documentation

- [Authentication Documentation](authentication.md)
- [Quick Reference](quick-reference.md)
- [Main Project Documentation](../README.md)
