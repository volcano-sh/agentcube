# Local Development

This guide will help you set up your local environment to develop and test AgentCube.

## Prerequisites

To contribute to AgentCube, you will need:

- **Go** (v1.22+)
- **Docker** or **Podman**
- **Kubectl**
- **Kind** (Kubernetes in Docker) for local clusters
- **GNU Make**

## 1. Clone the Repository

```bash
git clone https://github.com/volcano-sh/agentcube.git
cd agentcube
```

## 2. Building the Project

AgentCube uses a `Makefile` to simplify common tasks.

### Build All Binaries

To build the `workloadmanager`, `agentd`, and `agentcube-router` binaries:

```bash
make build-all
```

Binaries will be placed in the `bin/` directory.

### Build Specific Components

- **Workload Manager**: `make build`
- **AgentD**: `make build-agentd`
- **Router**: `make build-router`

## 3. Running Locally

### Run Workload Manager

You can run the Workload Manager locally using your existing Kubeconfig:

```bash

make run-local
```

This will start the server on port 8080 by default.

### Run Router

```bash
make run-router
```

## 4. Docker Images

### Build All Images

```bash
make docker-build        # Workload Manager
make docker-build-router # Router
make docker-build-picod  # Picod (Agent Daemon)
```

### Loading to Kind
If you are using Kind for local development, you can load images directly:

```bash
make kind-load
make kind-load-router
```

## 5. Coding Standards

We use `golangci-lint` to ensure code quality.

```bash
make lint
```

To format your code:

```bash
make fmt
```
