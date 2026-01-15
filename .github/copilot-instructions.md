# Copilot Instructions for AgentCube

- **Project snapshot**: AgentCube (Volcano subproject) runs AI agent/code-interpreter sessions on Kubernetes using microVM sandboxes. Control plane = **Workload Manager** (`cmd/workload-manager`, `pkg/workloadmanager`); data plane = **Router** (`cmd/router`, `pkg/router`); runtime = **PicoD** (`cmd/picod`, `pkg/picod`); CRDs live in `pkg/apis/runtime/v1alpha1` with generated clients under `client-go/`.
- **Session flow**: Clients hit Router `/v1/namespaces/{ns}/(agent-runtimes|code-interpreters)/{name}/invocations/*` with optional `x-agentcube-session-id`. Router uses Redis/ValKey (`pkg/store`) to find/create sessions, signs requests (JWT), and proxies to sandboxes. Workload Manager provisions sandboxes (may use warm pools) via agent-sandbox CRDs and tracks TTL/idle GC.
- **Coding conventions**: 
  - **Follow Kubernetes coding conventions**: https://www.kubernetes.dev/docs/guide/coding-convention/
  - Prefer `klog/logr` over `fmt.Printf`; error strings lowercase/no punctuation; wrap with `%w`. Tie work to `context.Context`, avoid goroutine leaks, keep TODOs as `TODO(name):`. Regenerate after API changes.
- **Common build targets (from repo root)**: `make build` (workloadmanager), `make build-router`, `make build-agentd`, `make build-all`. Regenerate code/CRDs/clients with `make generate`, `make gen-client`, or `make gen-all`; verify with `make gen-check`. Go toolchain pinned to 1.24.x in `go.mod`.
- **Quality gates**: `make fmt lint vet test` for fast checks (`lint` uses golangci-lint). `make test` runs `go test -v ./...`. Keep `go mod tidy` when deps change.
- **Patterns to reuse**:
  - Router uses Gin (`pkg/router/server.go`) with session management and JWT signing—follow existing middleware layout.
  - Workload Manager uses controller-runtime; reconcile CRDs via `pkg/workloadmanager` helpers and keep DeepCopy/client code fresh (`make gen-all`).
  - Store implementations in `pkg/store` support Redis/ValKey—reuse `store.Storage()` instead of new clients.
  - Tests live beside code (`pkg/router/*_test.go`, `pkg/workloadmanager/*_test.go`); E2E cases under `test/e2e` document flows.
- **Docs to consult**: `docs/design/agentcube-proposal.md` for architecture, `docs/devguide/copilot-agent-guide.md` for detailed conventions, `test/e2e/README.md` for end-to-end setup.
- **PR hygiene**: Include generated file updates, keep binaries out of git (`bin/`). Favor minimal deps and avoid re-implementing helpers already in `pkg/common` and `pkg/workloadmanager/utils.go`.