# E2B API Examples

This directory contains runnable Python examples that show how to use AgentCube's
E2B-compatible REST API through the official `e2b` SDKs. AgentCube exposes the
E2B Platform API (sandboxes + templates) via its Router, so any client built
against the standard E2B SDK can talk to AgentCube unchanged.

## Prerequisites

1. **AgentCube cluster** with the Router deployed and `ENABLE_E2B_API=true`
   (see `docs/tutorials/e2b-api-guide.md` for setup).
2. **An API key** stored in the `e2b-api-keys` Secret in the `agentcube-system`
   namespace. Retrieve it with:

   ```bash
   kubectl get secret e2b-api-keys -n agentcube-system \
     -o jsonpath='{.data.<your-key-name>}' | base64 -d
   ```

3. **Python 3.8 or newer** plus the SDKs:

   ```bash
   pip install e2b e2b-code-interpreter
   ```

## Environment Variables

| Variable           | Default                          | Purpose                                          |
| ------------------ | -------------------------------- | ------------------------------------------------ |
| `E2B_API_KEY`      | (required)                       | API key for authentication                       |
| `E2B_BASE_URL`     | (required)                       | Full URL of the AgentCube Router                 |
| `E2B_DOMAIN`       | derived from `E2B_BASE_URL`      | Host[:port] used by the e2b SDK internals        |
| `E2B_TEMPLATE_ID`  | `default/code-interpreter`       | Template to instantiate when creating sandboxes  |

The scripts derive `E2B_DOMAIN` from `E2B_BASE_URL` automatically when it is not
already set.

## Running the Examples

```bash
export E2B_API_KEY="<your-api-key>"
export E2B_BASE_URL="<your-router-url>"

python example/e2b/01_sandbox_lifecycle.py
python example/e2b/02_template_management.py
python example/e2b/03_code_interpreter_workflow.py
```

Each script is self-contained and can be run independently.

## What Each Example Covers

- **`01_sandbox_lifecycle.py`** — full sandbox lifecycle: create, inspect, list,
  extend timeout, refresh, and close. Demonstrates the context-manager pattern
  and basic error handling.
- **`02_template_management.py`** — template CRUD: list, create, poll until
  build is ready, get details, update aliases/description, list builds, and
  delete. Built on the official `e2b.Template` API.
- **`03_code_interpreter_workflow.py`** — a typical AI agent flow: spin up a
  sandbox, run a sequence of Python code cells with persistent kernel state,
  read and write files via the sandbox filesystem, and inspect execution
  errors instead of letting them crash the script.

## Cross-References

- API guide: `docs/tutorials/e2b-api-guide.md`
- Architecture design: `docs/design/e2b-api-architecture.md`
- Implementation guide: `docs/devguide/e2b-implementation.md`
