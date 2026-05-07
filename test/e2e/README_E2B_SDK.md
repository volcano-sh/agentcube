# E2B SDK Compatibility E2E Tests

This directory contains E2E tests for verifying E2B API compatibility using the official E2B Python SDK.

## Overview

The `test_e2b_sdk.py` test file uses the official `e2b-code-interpreter` Python SDK to verify that AgentCube Router correctly implements E2B-compatible REST API.

## Test Coverage

The test suite covers the following scenarios:

### Sandbox Lifecycle Tests
1. **Create Sandbox** - Verify sandbox creation via E2B SDK
2. **Context Manager** - Test automatic cleanup using `with` statement
3. **List Sandboxes** - Verify listing all running sandboxes
4. **Get Sandbox Info** - Test retrieving sandbox details
5. **Set Timeout** - Verify timeout extension
6. **Refresh TTL** - Test TTL refresh functionality
7. **Delete Sandbox** - Verify explicit sandbox deletion
8. **Full Workflow** - End-to-end lifecycle test

### Error Handling Tests
9. **Invalid Template** - Test error handling for invalid template IDs
10. **Concurrent Sandboxes** - Test creating multiple sandboxes simultaneously

### Code Execution Tests (Optional)
11. **Execute Python Code** - Test code execution if SDK supports it

## Prerequisites

1. **E2B Python SDK**:
   ```bash
   pip install e2b-code-interpreter
   ```

2. **Environment Variables**:
   - `E2B_API_KEY` - API key for authentication (default: `test-api-key`)
   - `E2B_BASE_URL` - Base URL of AgentCube Router (default: `http://localhost:8081`)
   - `E2B_TEMPLATE_ID` - Template ID to use (default: `default/code-interpreter`)

## Running Tests

### Using run_e2e.sh (Recommended)

The E2E test script automatically installs the E2B SDK and runs the tests:

```bash
./test/e2e/run_e2e.sh
```

### Manual Execution

```bash
# Install E2B SDK
pip install e2b-code-interpreter

# Set environment variables
export E2B_API_KEY="your-api-key"
export E2B_BASE_URL="http://localhost:8081"
export E2B_TEMPLATE_ID="default/code-interpreter"

# Run tests
cd test/e2e
python test_e2b_sdk.py
```

### Run Specific Test

```bash
python test_e2b_sdk.py TestE2BSDKCompatibility.test_01_create_sandbox
```

## Test Output Example

```
E2B SDK Compatibility E2E Tests
======================================================================

Test Configuration:
  Base URL: http://localhost:8081
  Template ID: default/code-interpreter
  API Key: **********

[Test 1] Creating sandbox...
  Created sandbox: sb-abc123
  State: running
  Started at: 2026-04-07T15:30:00Z
  Sandbox closed successfully
...
----------------------------------------------------------------------
Ran 10 tests in 45.234s

OK
```

## How It Works

The test suite uses the E2B Python SDK's `Sandbox` class to interact with AgentCube Router:

```python
from e2b_code_interpreter import Sandbox

# Create sandbox
sandbox = Sandbox.create(
    api_key="your-api-key",
    template_id="default/code-interpreter",
    timeout=300
)

# Get info
info = sandbox.get_info()
print(f"Sandbox: {info.sandbox_id}, State: {info.state}")

# Set timeout
sandbox.set_timeout(600)

# Refresh TTL
sandbox.refresh(timeout=300)

# Delete
sandbox.close()
```

## Configuration

The E2B SDK is configured to use AgentCube Router by setting environment variables:

```python
os.environ["E2B_DOMAIN"] = "localhost:8081"  # Router address
os.environ["E2B_HTTPS"] = "false"             # Use HTTP
```

This redirects all E2B SDK requests to AgentCube Router instead of the official E2B API.

## Skipping Tests

If the E2B SDK is not installed, tests will be skipped:

```
Warning: e2b_code_interpreter not installed. Install with: pip install e2b-code-interpreter
```

## CI/CD Integration

The E2E test script (`run_e2e.sh`) automatically:
1. Installs the E2B SDK
2. Configures the environment
3. Runs the tests
4. Reports results (non-blocking for now)

Future improvements may make these tests blocking if full compatibility is required.
