# Project Structure

This page provides an overview of the AgentCube repository layout to help you find your way around the codebase.

## Directory Layout

| Directory | Description |
| :--- | :--- |
| `cmd/` | Main entry points for all AgentCube binaries (`workload-manager`, `router`, `agentd`). |
| `pkg/` | Core logic and libraries used across the project. |
| `pkg/apis/` | Kubernetes Custom Resource Definitions (CRDs) and API types. |
| `pkg/workloadmanager/` | Implementation of the Control Plane (Workload Manager). |
| `pkg/router/` | Implementation of the Data Plane (AgentCube Router). |
| `pkg/agentd/` | Implementation of the agent daemon that runs inside sandboxes. |
| `manifests/` | Helm charts and base Kubernetes manifests for deployment. |
| `sdk-python/` | The official Python SDK for interacting with AgentCube. |
| `client-go/` | Generated Go client for AgentCube CRDs. |
| `docs/` | Project documentation, design proposals, and the Docusaurus website source. |
| `example/` | Real-world usage examples like the PCAP Analyzer. |
| `test/` | End-to-end and integration tests. |
| `hack/` | Scripts for code generation, copyright headers, and boilerplate. |

## Key Components

### Control Plane

Located in `pkg/workloadmanager/`, this component manages the lifecycle of sessions and sandboxes. It interacts with the Kubernetes API to provision pods.

### Data Plane

Located in `pkg/router/`, the router is a high-performance proxy that handles request routing and authentication for agent sessions.

### Runtime

Located in `pkg/agentd/`, this is the client-facing daemon that runs inside the job container. It provides the execution environment for user scripts and commands.
