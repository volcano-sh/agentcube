# Testing Guide

Quality is paramount at AgentCube. We maintain a comprehensive suite of unit and end-to-end tests to ensure system stability.

## 1. Unit Tests

Unit tests are located alongside the code in `_test.go` files.

### Run All Unit Tests

```bash
make test
```

### Run Tests for a Specific Package

```bash
go test -v ./pkg/workloadmanager/...
```

## 2. End-to-End (E2E) Tests

E2E tests verify the entire system flow, from session creation to code execution.

### Prerequisites

- A running Kubernetes cluster (Kind is recommended).
- AgentCube components deployed to the cluster.

### Run E2E Tests

```bash
make e2e
```

This script will:

1. Create a local Kind cluster (if not existing).
2. Build and load all Docker images.
3. Deploy AgentCube.
4. Execute test scenarios against the cluster.

### Clean Up E2E Environment

```bash
make e2e-clean
```

## 3. Python SDK Tests

If you are making changes to the Python SDK, ensure you run the SDK-specific tests.

```bash
cd sdk-python
# Install test dependencies
pip install pytest requests PyJWT cryptography

# Run the tests
pytest tests/
```

## 4. CI Pipeline

Every Pull Request triggers our GitHub Actions CI pipeline, which runs:

- **Linting**: `golangci-lint`
- **Unit Tests**: `go test`
- **Build Validation**: Ensures all binaries can be compiled.
- **E2E Tests**: A subset of E2E tests are run on every PR.
