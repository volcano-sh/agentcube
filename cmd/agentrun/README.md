# AgentRun CLI

AgentRun CLI is a developer tool that streamlines the development, packaging, building, and deployment of AI agents to AgentCube. It provides a unified interface for managing the complete agent lifecycle from local development to cloud deployment.

## 🚀 Quick Start

```bash
# Install
pip install agentrun

# Initialize a new agent project
agentrun init my-agent

# Package your agent
agentrun pack -f ./my-agent

# Build container image
agentrun build -f ./my-agent

# Publish to AgentCube
agentrun publish -f ./my-agent

# Invoke your agent
agentrun invoke -f ./my-agent --payload '{"prompt": "Hello, Agent!"}'
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
cd agentcube/cli-agentrun
pip install -e .
```

### Development Setup

```bash
git clone https://github.com/volcano-sh/agentcube.git
cd agentcube/cli-agentrun
pip install -e ".[dev]"
pre-commit install
```

## 📖 Documentation

- [Getting Started Guide](docs/getting-started.md)
- [Configuration Reference](docs/configuration.md)
- [CLI Commands](docs/commands.md)
- [Python SDK](docs/sdk.md)
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

## 🤝 Contributing

We welcome contributions! Please see our [Contributing Guide](CONTRIBUTING.md) for details.

### Development Workflow

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Add tests
5. Run the test suite
6. Submit a pull request

```bash
# Run tests
pytest

# Run linting
black .
isort .
flake8 .

# Run type checking
mypy .
```

## 📄 License

This project is licensed under the Apache License 2.0 - see the [LICENSE](LICENSE) file for details.

## 🔗 Links

- [AgentCube Main Project](https://github.com/volcano-sh/agentcube)
- [Volcano Scheduler](https://github.com/volcano-sh/volcano)
- [Documentation](https://agentcube.readthedocs.io/)
- [Issue Tracker](https://github.com/volcano-sh/agentcube/issues)