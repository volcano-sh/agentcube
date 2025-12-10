# AgentRun CLI

AgentRun CLI is a developer tool that streamlines the development, packaging, building, and deployment of AI agents to AgentCube. It provides a unified interface for managing the complete agent lifecycle from local development to cloud deployment.

## Quick Start

### Prerequisites

- Python 3.8+
- Git
- Docker (optional, for container builds)

### Installation

```bash
# Clone the repository
git clone https://github.com/volcano-sh/agentcube.git
cd agentcube/cmd/agentrun

# Create virtual environment
python3 -m venv venv
source venv/bin/activate  # On Windows: venv\Scripts\activate

# Install in development mode
pip install -e .
```

### Your First Agent

1. **Package an existing agent:**
   ```bash
   kubectl agentrun pack -f examples/hello-agent --agent-name "my-agent"
   ```

2. **Build the container image:**
   ```bash
   kubectl agentrun build -f examples/hello-agent
   ```

3. **Publish to AgentCube:**
   ```bash
   kubectl agentrun publish \
      -f examples/hello-agent \
      --image-url "docker.io/username/my-agent" \
   ```

4. **Invoke your agent:**
   ```bash
   kubectl agentrun invoke -f examples/hello-agent --payload '{"prompt": "Hello World!"}'
   ```

5. **Check status:**
   ```bash
   kubectl agentrun status -f examples/hello-agent
   ```

## Features

- **Multi-language Support**: Python, Java (with more languages planned)
- **Flexible Build Modes**: Local Docker builds and cloud builds
- **Multi-Provider Deployment**: Support for AgentCube CRDs and standard Kubernetes deployments
- **AgentCube Integration**: Seamless publishing and management
- **Developer-friendly**: Rich CLI experience with detailed feedback
- **CI/CD Ready**: Python SDK for programmatic access
- **Extensible Architecture**: Plugin system for custom providers

## Installation

### From PyPI (Recommended)

```bash
pip install agentrun
```

### From Source

```bash
git clone https://github.com/volcano-sh/agentcube.git
cd agentcube/cmd/agentrun
pip install -e .
```

## Documentation

- [Design](AgentRun-CLI-Design.md)
- [Examples](examples/)

## Configuration

AgentRun uses a `agent_metadata.yaml` file to configure your agent:

```yaml
agent_name: my-agent
description: "A sample AI agent"
language: python
entrypoint: python main.py
port: 8080
build_mode: local
requirements_file: requirements.txt
```

## Architecture

AgentRun CLI follows a modular four-layer architecture:

1. **CLI Layer**: Typer-based command interface
2. **Runtime Layer**: Business logic and Python SDK
3. **Operations Layer**: Core domain logic
4. **Services Layer**: External integrations

## Deployment Providers

AgentRun supports two deployment providers:

### AgentCube Provider (Default)

Deploys agents using AgentCube's custom AgentRuntime CRDs, providing enhanced agent lifecycle management and integration with the AgentCube ecosystem.

```bash
kubectl agentrun publish -f examples/hello-agent --provider agentcube
```

### Standard Kubernetes Provider

Deploys agents as standard Kubernetes Deployments and Services, suitable for environments without AgentCube CRDs installed.

```bash
kubectl agentrun publish -f examples/hello-agent --provider k8s \
  --node-port 30080 \
  --replicas 3
```

## Command Reference

### `kubectl agentrun pack`
Package the agent application into a standardized workspace.

```bash
kubectl agentrun pack [OPTIONS]

Options:
  -f, --workspace TEXT    Path to the agent workspace directory [default: .]
  --agent-name TEXT       Override the agent name
  --language TEXT         Programming language (python, java)
  --entrypoint TEXT       Override the entrypoint command
  --port INTEGER          Port to expose in the Dockerfile
  --build-mode TEXT       Build strategy: local or cloud
  --description TEXT      Agent description
  --output TEXT           Output path for packaged workspace
  --verbose               Enable detailed logging
```

### `kubectl agentrun build`
Build the agent image based on the packaged workspace.

```bash
kubectl agentrun build [OPTIONS]

Options:
  -f, --workspace TEXT    Path to the agent workspace directory [default: .]
  -p, --proxy TEXT        Custom proxy URL for dependency resolution
  --cloud-provider TEXT   Cloud provider name (e.g., huawei)
  --output TEXT           Output path for build artifacts
  --verbose               Enable detailed logging
```

### `kubectl agentrun publish`
Publish the agent to AgentCube

```bash
kubectl agentrun publish [OPTIONS]

Options:
  -f, --workspace TEXT    Path to the agent workspace directory [default: .]
  --version TEXT          Semantic version string (e.g., v1.0.0)
  --image-url TEXT        Image repository URL (required in local build mode)
  --image-username TEXT   Username for image repository
  --image-password TEXT   Password for image repository
  --description TEXT      Agent description
  --region TEXT           Deployment region
  --cloud-provider TEXT   Cloud provider name (e.g., huawei)
  --provider TEXT         Target provider for deployment (agentcube, k8s). 'agentcube' deploys AgentRuntime CR, 'k8s' deploys standard K8s Deployment/Service. [default: agentcube]
  --node-port INTEGER     Specific NodePort to use (30000-32767) for K8s deployment
  --replicas INTEGER      Number of replicas for K8s deployment (default: 1)
  --endpoint TEXT         Custom API endpoint for AgentCube or Kubernetes cluster
  --namespace TEXT        Kubernetes namespace to use for deployment [default: default]
  --verbose               Enable detailed logging
```

### `kubectl agentrun invoke`
Invoke a published agent via AgentCube or Kubernetes.

```bash
kubectl agentrun invoke [OPTIONS]

Options:
  -f, --workspace TEXT    Path to the agent workspace directory [default: .]
  --payload TEXT          JSON-formatted input passed to the agent [default: {}]
  --header TEXT           Custom HTTP headers (e.g., 'Authorization: Bearer token')
  --provider TEXT         Target provider for deployment (agentcube, k8s). 'agentcube' deploys AgentRuntime CR, 'k8s' deploys standard K8s Deployment/Service. [default: agentcube]
  --verbose               Enable detailed logging
```

### `kubectl agentrun status`
Check the status of a published agent.

```bash
kubectl agentrun status [OPTIONS]

Options:
  -f, --workspace TEXT    Path to the agent workspace directory [default: .]
  --provider TEXT         Target provider for deployment (agentcube, k8s). 'agentcube' deploys AgentRuntime CR, 'k8s' deploys standard K8s Deployment/Service. [default: agentcube]
  --verbose               Enable detailed logging
```

## Agent Structure

An AgentRun workspace typically contains:

```
my-agent/
├── agent_metadata.yaml    # Agent configuration (auto-generated)
├── Dockerfile            # Container definition (auto-generated)
├── requirements.txt      # Python dependencies
├── main.py              # Agent entrypoint
└── src/                 # Source code
```

### `agent_metadata.yaml`

```yaml
agent_name: my-agent
description: "My AI agent"
language: python
entrypoint: python main.py
port: 8080
build_mode: local
requirements_file: requirements.txt
```

## Language Support

### Python

Fully supported with automatic dependency management and Dockerfile generation.

- Supported versions: Python 3.8+
- Package manager: pip
- Dependencies file: requirements.txt
- Example: [examples/hello-agent](examples/hello-agent)

### Java

Supported with Maven-based builds and OpenJDK runtime.

- Supported versions: Java 17+
- Build tool: Maven
- Dependencies file: pom.xml
- Note: Java example coming soon

## Troubleshooting

### Common Issues

1. **"Docker is not available"**
   - Install Docker and make sure it's running
   - Use `--build-mode cloud` for cloud builds

2. **"Metadata file not found"**
   - Run `kubectl agentrun pack` first to generate metadata
   - Ensure you're in the correct workspace directory

3. **"Agent not published yet"**
   - Run `kubectl agentrun publish` before trying to invoke
   - Check that the build completed successfully

4. **"Provider not found"**
   - Ensure you're using a valid provider: `agentcube` or `k8s`
   - Check that your Kubernetes cluster has the required CRDs (for agentcube provider)

### Getting Help

```bash
# General help
kubectl agentrun --help

# Command-specific help
kubectl agentrun pack --help
kubectl agentrun build --help
kubectl agentrun publish --help
kubectl agentrun invoke --help
kubectl agentrun status --help
```

## License

This project is licensed under the Apache License 2.0 - see the [LICENSE](LICENSE) file for details.

## Links

- [AgentCube Main Project](https://github.com/volcano-sh/agentcube)
- [Volcano Scheduler](https://github.com/volcano-sh/volcano)
- [Issue Tracker](https://github.com/volcano-sh/agentcube/issues)
