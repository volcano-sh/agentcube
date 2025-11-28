# AgentRun CLI

AgentRun CLI is a developer tool that streamlines the development, packaging, building, and deployment of AI agents to AgentCube. It provides a unified interface for managing the complete agent lifecycle from local development to cloud deployment.

## 🚀 Quick Start

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
   kubectl agentrun build -f examples/hello-agent --verbose
   ```

3. **Publish to AgentCube:**
   ```bash
   kubectl agentrun publish \
      -f examples/hello-agent \
      --image-url "docker.io/username/my-agent" \
      --verbose \
      --use-k8s
   ```

4. **Invoke your agent:**
   ```bash
   kubectl agentrun invoke -f examples/hello-agent --payload '{"prompt": "Hello World!"}'
   ```

5. **Check status:**
   ```bash
   kubectl agentrun status -f examples/hello-agent --use-k8s
   ```

## 📋 Features

- **Multi-language Support**: Python, Java (with more languages planned)
- **Flexible Build Modes**: Local Docker builds and cloud builds
- **AgentCube Integration**: Seamless publishing and management
- **Developer-friendly**: Rich CLI experience with detailed feedback
- **CI/CD Ready**: Python SDK for programmatic access
- **Extensible Architecture**: Plugin system for custom providers

## 🛠️ Installation

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

### Development Setup

```bash
git clone https://github.com/volcano-sh/agentcube.git
cd agentcube/cmd/agentrun
pip install -e ".[dev]"
pre-commit install
```

## 📖 Documentation

- [Design](AgentRun-CLI-Design.md)
- [Examples](examples/)

## 🔧 Configuration

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

## 🏗️ Architecture

AgentRun CLI follows a modular four-layer architecture:

1. **CLI Layer**: Typer-based command interface
2. **Runtime Layer**: Business logic and Python SDK
3. **Operations Layer**: Core domain logic
4. **Services Layer**: External integrations

## 📋 Command Reference

### `kubectl agentrun pack`
Package an agent into a standardized workspace.

```bash
kubectl agentrun pack -f <workspace> [OPTIONS]

Options:
  -f, --workspace TEXT    Path to agent workspace [default: .]
  --agent-name TEXT       Override agent name
  --language TEXT         Programming language (python, java)
  --entrypoint TEXT       Override entrypoint command
  --port INTEGER          Port for Dockerfile [default: 8080]
  --build-mode TEXT       Build strategy: local or cloud
  --description TEXT      Agent description
  --output TEXT           Output path for packaged workspace
  --verbose               Enable detailed logging
```

### `kubectl agentrun build`
Build a container image from the packaged workspace.

```bash
kubectl agentrun build -f <workspace> [OPTIONS]

Options:
  -f, --workspace TEXT    Path to agent workspace [default: .]
  -p, --proxy TEXT        Custom proxy URL for dependencies
  --cloud-provider TEXT   Cloud provider for cloud builds
  --output TEXT           Output path for build artifacts
  --verbose               Enable detailed logging
```

### `kubectl agentrun publish`
Publish the agent to AgentCube.

```bash
kubectl agentrun publish -f <workspace> [OPTIONS]

Options:
  -f, --workspace TEXT    Path to agent workspace [default: .]
  --version TEXT          Semantic version (e.g., v1.0.0)
  --image-url TEXT        Image repository URL (local build mode)
  --image-username TEXT   Registry username
  --image-password TEXT   Registry password
  --description TEXT      Agent description
  --region TEXT           Deployment region
  --cloud-provider TEXT   Cloud provider name
  --verbose               Enable detailed logging
```

### `kubectl agentrun invoke`
Invoke a published agent.

```bash
kubectl agentrun invoke [OPTIONS]

Options:
  -f, --workspace TEXT    Path to agent workspace [default: .]
  --payload TEXT          JSON payload for the agent
  --header TEXT           Custom HTTP headers
  --verbose               Enable detailed logging
```

### `kubectl agentrun status`
Check the status of a published agent.

```bash
kubectl agentrun status -f <workspace> [OPTIONS]

Options:
  -f, --workspace TEXT    Path to agent workspace [default: .]
  --verbose               Enable detailed logging
```

## 🏗️ Agent Structure

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

## 🔧 Troubleshooting

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

## 📄 License

This project is licensed under the Apache License 2.0 - see the [LICENSE](LICENSE) file for details.

## 🔗 Links

- [AgentCube Main Project](https://github.com/volcano-sh/agentcube)
- [Volcano Scheduler](https://github.com/volcano-sh/volcano)
- [Issue Tracker](https://github.com/volcano-sh/agentcube/issues)