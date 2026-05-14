# AgentCube LangChain Integration

This directory contains the AgentCube sandbox provider for LangChain and the DeepAgents ecosystem.

## Features

- **AgentCubeSandbox**: A sandbox provider for executing code in AgentCube sessions.
- **Async Support**: Fully non-blocking async methods using `asyncio.to_thread`.
- **Combined Output**: Merges stdout and stderr for better agent reasoning.

## Installation

```bash
pip install agentcube-sdk[langchain]
```

## Usage

```python
from agentcube import CodeInterpreterClient
from agentcube.integrations.langchain import AgentCubeSandbox

client = CodeInterpreterClient()
sandbox = AgentCubeSandbox(client)

response = sandbox.execute("print('hello world')")
print(response.output)
```
