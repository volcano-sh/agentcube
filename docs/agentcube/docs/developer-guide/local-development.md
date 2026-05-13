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

- **Workload Manager**: `make build-workloadmanager`
- **AgentD**: `make build-agentd`
- **Router**: `make build-router`

## 4. Docker Images

### Build All Images

```bash
make docker-build-workloadmanager
make docker-build-router
make docker-build-picod
```

### Loading to Kind
If you are using Kind for local development, you can load images directly:

```bash
make kind-load
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
