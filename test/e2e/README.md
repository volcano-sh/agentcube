# AgentCube E2E Tests

End-to-end tests for AgentCube backend services, validating the complete request flow from SDK to execution runtime.

## Test Scope

### Components Tested

| Component | Description |
|-----------|-------------|
| **Python SDK** (`sdk-python`) | `CodeInterpreterClient` - session management, code execution, file operations |
| **WorkloadManager** | Control plane - session creation/deletion, pod scheduling |
| **Router** | Data plane - request signing (JWT) and forwarding |
| **PicoD** | Code execution runtime with authentication verification |
| **agent-sandbox** | Kubernetes sandbox lifecycle management |

### Components NOT Tested

| Component | Description |
|-----------|-------------|
| **CLI** (`cmd/cli`) | `kubectl agentcube` commands (pack, build, publish, invoke, status) |

## Request Flow

1. **Session Creation**: SDK → WorkloadManager → agent-sandbox (creates Pod with PicoD)
2. **Code Execution**: SDK → Router (signs request) → PicoD (verifies signature, executes code)
3. **Session Deletion**: SDK → WorkloadManager → agent-sandbox (deletes Pod)

## What the Script Does

The `run_e2e.sh` script performs the following steps:

1. Creates a Kind cluster
2. Installs CRDs and agent-sandbox
3. **Builds Docker images** (`make docker-build`, `make docker-build-router`, `make docker-build-picod`)
4. Loads images into Kind cluster
5. Deploys Redis, WorkloadManager, and Router
6. Creates test resources (AgentRuntime, CodeInterpreter)
7. Runs Go tests and Python SDK tests
8. Cleans up resources

## Prerequisites

- **Docker** - Container runtime
- **Kind** - Kubernetes in Docker
- **kubectl** - Kubernetes CLI
- **Python 3.8+** - For SDK tests
- **Go 1.24+** - For Go tests

## Running Tests

```bash
# Run all E2E tests (creates Kind cluster, deploys services, runs tests)
./test/e2e/run_e2e.sh

# Skip cluster recreation (faster for re-runs)
E2E_CLEAN_CLUSTER=false ./test/e2e/run_e2e.sh
```

## Test Cases

### Go Tests (`e2e_test.go`)

| Test | Description |
|------|-------------|
| `TestAgentRuntimeBasicInvocation` | Basic echo agent invocation (basic, empty, complex input) |
| `TestAgentRuntimeErrorHandling` | Error handling for non-existent runtimes |
| `TestAgentRuntimeSessionTTL` | Session timeout and cleanup verification |

### Python SDK Tests (`test_codeinterpreter.py`)

| Test | Description |
|------|-------------|
| `test_case1_simple_code_execution_auto_session` | Simple code execution with auto-created session |
| `test_case2_code_execution_in_session` | Stateless execution verification (variables not preserved) |
| `test_case3_file_based_workflow_fibonacci_json` | File upload, execution, and download workflow |

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `E2E_CLUSTER_NAME` | `agentcube-e2e` | Kind cluster name |
| `E2E_CLEAN_CLUSTER` | `true` | Delete and recreate cluster before tests |
| `AGENTCUBE_NAMESPACE` | `agentcube` | Kubernetes namespace for deployments |
| `E2E_VENV_DIR` | `/tmp/agentcube-e2e-venv` | Python virtual environment path |
| `WORKLOAD_MANAGER_LOCAL_PORT` | `8080` | Local port for WorkloadManager |
| `ROUTER_LOCAL_PORT` | `8081` | Local port for Router |

## Test Resources

| File | Description |
|------|-------------|
| `echo_agent.yaml` | AgentRuntime CR for echo agent tests |
| `e2e_code_interpreter.yaml` | CodeInterpreter CR for SDK tests |

## Adding New Tests

### Go Tests (`e2e_test.go`)

Add test functions to `e2e_test.go` following the existing patterns.

### Python SDK Tests (`test_codeinterpreter.py`)

Add test methods to `TestCodeInterpreterE2E` class in `test_codeinterpreter.py`.

## Cleanup

The test script automatically cleans up:

- Port-forward processes
- Python virtual environment
- Temporary files

To manually delete the Kind cluster:

```bash
kind delete cluster --name agentcube-e2e
```
