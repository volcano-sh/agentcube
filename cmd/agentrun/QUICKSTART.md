# AgentRun CLI Quick Start Guide

Welcome to AgentRun CLI - a developer tool for packaging, building, and deploying AI agents to AgentCube!

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
   agentrun pack -f examples/hello-agent --agent-name "my-agent"
   ```

2. **Build the container image:**
   ```bash
   agentrun build -f examples/hello-agent --verbose
   ```

3. **Publish to AgentCube:**
   ```bash
   agentrun publish \
      -f examples/hello-agent \
      --image-url "taoruiw/my-agent:v1.0.0" \
      --verbose \
      --use-k8s
   ```

4. **Invoke your agent:**
   ```bash
   agentrun invoke -f examples/hello-agent --payload '{"prompt": "Hello World!"}'
   ```

5. **Check status:**
   ```bash
   agentrun status -f examples/hello-agent
   ```

## 📋 Command Reference

### `agentrun pack`
Package an agent into a standardized workspace.

```bash
agentrun pack -f <workspace> [OPTIONS]

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

### `agentrun build`
Build a container image from the packaged workspace.

```bash
agentrun build -f <workspace> [OPTIONS]

Options:
  -f, --workspace TEXT    Path to agent workspace [default: .]
  -p, --proxy TEXT        Custom proxy URL for dependencies
  --cloud-provider TEXT   Cloud provider for cloud builds
  --output TEXT           Output path for build artifacts
  --verbose               Enable detailed logging
```

### `agentrun publish`
Publish the agent to AgentCube.

```bash
agentrun publish -f <workspace> [OPTIONS]

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

### `agentrun invoke`
Invoke a published agent.

```bash
agentrun invoke [OPTIONS]

Options:
  -f, --workspace TEXT    Path to agent workspace [default: .]
  --payload TEXT          JSON payload for the agent
  --header TEXT           Custom HTTP headers
  --verbose               Enable detailed logging
```

### `agentrun status`
Check the status of a published agent.

```bash
agentrun status -f <workspace> [OPTIONS]

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

## 🔄 Workflow Example

```bash
# 1. Initialize or navigate to your agent project
cd my-agent-project

# 2. Package the agent
agentrun pack --agent-name "chatbot" --description "Customer service chatbot"

# 3. Build the container image
agentrun build

# 4. Publish to AgentCube
agentrun publish --version "v1.0.0" --image-url "docker.io/myorg/chatbot"

# 5. Test the agent
agentrun invoke --payload '{"message": "Hello!"}'

# 6. Check status
agentrun status
```

## 🎯 Features

### ✅ MVP Features (Available Now)
- Python agent support
- Local Docker builds
- AgentCube integration (simulated)
- Rich CLI experience
- Metadata management
- Dockerfile generation

### 🚧 Coming Soon
- Java agent support
- Cloud builds (AWS, GCP, Azure)
- Real AgentCube API integration
- Monitoring and logs
- CI/CD templates
- Plugin system

## 🛠️ Development

### Running Tests
```bash
pytest
```

### Code Formatting
```bash
black .
isort .
```

### Type Checking
```bash
mypy .
```

## 📚 Examples

Check out the `examples/` directory for sample agents:

- `hello-agent/` - Simple HTTP API agent
- More examples coming soon!

## 🔧 Troubleshooting

### Common Issues

1. **"Docker is not available"**
   - Install Docker and make sure it's running
   - Use `--build-mode cloud` for cloud builds

2. **"Metadata file not found"**
   - Run `agentrun pack` first to generate metadata
   - Ensure you're in the correct workspace directory

3. **"Agent not published yet"**
   - Run `agentrun publish` before trying to invoke
   - Check that the build completed successfully

### Getting Help

```bash
# General help
agentrun --help

# Command-specific help
agentrun pack --help
agentrun build --help
agentrun publish --help
agentrun invoke --help
agentrun status --help
```

## 🤝 Contributing

We welcome contributions! Please see our [Contributing Guide](CONTRIBUTING.md) for details.

## 📄 License

This project is licensed under the Apache License 2.0.