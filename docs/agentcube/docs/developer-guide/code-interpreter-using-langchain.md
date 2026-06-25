---
sidebar_position: 8
---

# Using Code Interpreter with LangChain

You can integrate the AgentCube Code Interpreter with [LangChain](https://www.langchain.com/) and [LangGraph](https://langchain-ai.github.io/langgraph/) to build AI agents capable of executing code, solving mathematical problems, and processing data. This guide demonstrates how to wrap the `CodeInterpreterClient` as a LangChain tool and deploy it within a ReAct agent.

## Prerequisites

Before you begin, ensure you have the following installed:

- **Python 3.10+**
- **LangChain & LangGraph**: `pip install langchain langchain-core langgraph`
- **AgentCube SDK**: `pip install agentcube-sdk`
- **An LLM provider SDK** (e.g., OpenAI): `pip install langchain-openai`

You'll also need:

- AgentCube deployed on your cluster (see [Getting Started](../getting-started.md))
- A `CodeInterpreter` resource created in Kubernetes
- Port-forwarding set up and `WORKLOAD_MANAGER_URL`/`ROUTER_URL` environment variables set

---

## Step 1: Initialize the Code Interpreter Client

The `CodeInterpreterClient` from the `agentcube` package manages the connection to the backend execution environment. Initialize it once for the lifetime of your application.

```python
from agentcube import CodeInterpreterClient

# Initialize for the session
ci_client = CodeInterpreterClient(name="my-interpreter")
```

---

## Step 2: Define the LangChain Tool

To allow an LLM to interact with the interpreter, wrap the client execution logic in a tool using the `@tool` decorator.

```python
from langchain_core.tools import tool
from agentcube import CodeInterpreterClient

# Global client reference (manage lifecycle at the application level)
ci_client: CodeInterpreterClient | None = None

@tool
def run_python_code(code: str) -> str:
    """
    Executes Python code in a sandboxed environment and returns the output.
    Use this tool to perform calculations, data analysis, or script execution.
    Always write complete, runnable Python scripts.
    """
    global ci_client
    try:
        if ci_client is None:
            ci_client = CodeInterpreterClient(name="my-interpreter")
        return ci_client.run_code("python", code)
    except Exception as e:
        return f"Error executing code: {str(e)}"
```

---

## Step 3: Create and Run the Agent

Combine the tool with an LLM using LangGraph's prebuilt agent functions.

```python
from langchain.chat_models import init_chat_model
from langgraph.prebuilt import create_react_agent
from langgraph.checkpoint.memory import MemorySaver
from langchain_core.messages import HumanMessage
import os

# 1. Setup
os.environ["OPENAI_API_KEY"] = "your-api-key-here"  # Use env vars or a secrets manager in production

# 2. Initialize LLM
llm = init_chat_model("gpt-4o", model_provider="openai")

# 3. Define Tools
tools = [run_python_code]

# 4. Create Agent with conversational memory
memory = MemorySaver()
agent = create_react_agent(llm, tools, checkpointer=memory)

# 5. Run the Agent
config = {"configurable": {"thread_id": "math-session-1"}}
query = "Calculate the 10th Fibonacci number using Python."

print(f"User: {query}")
result = agent.invoke(
    {"messages": [HumanMessage(content=query)]},
    config=config
)
print(f"Agent: {result['messages'][-1].content}")
```

**Example output:**

```
User: Calculate the 10th Fibonacci number using Python.
Agent: The 10th Fibonacci number is **55**. Here's the Python code that was used:

def fib(n):
    a, b = 0, 1
    for _ in range(n):
        a, b = b, a + b
    return a

print(fib(10))  # Output: 55
```

---

## Full Example: Math Agent Service (FastAPI)

The following example demonstrates how to host the agent as a production-grade service using FastAPI. It manages the `CodeInterpreterClient` lifecycle using application lifespan events.

```python
from fastapi import FastAPI, Request
from contextlib import asynccontextmanager
from langchain.chat_models import init_chat_model
from langchain_core.tools import tool
from langgraph.prebuilt import create_react_agent
from langgraph.checkpoint.memory import MemorySaver
from agentcube import CodeInterpreterClient
import uvicorn
import os

# --- Global State ---
ci_client: CodeInterpreterClient | None = None

@tool
def run_python_code(code: str) -> str:
    """Executes Python code in an isolated sandbox and returns the output."""
    global ci_client
    if ci_client:
        return ci_client.run_code("python", code)
    return "Code Interpreter is not initialized."

# --- Application Lifespan ---
@asynccontextmanager
async def lifespan(app: FastAPI):
    # Startup: Initialize the Code Interpreter session
    global ci_client
    print("Initializing Code Interpreter session...")
    ci_client = CodeInterpreterClient(name="my-interpreter", ttl=86400)
    yield
    # Shutdown: Properly clean up the session
    if ci_client:
        print("Stopping Code Interpreter session...")
        ci_client.stop()

# --- App Setup ---
app = FastAPI(title="AgentCube Math Agent", lifespan=lifespan)

llm = init_chat_model("gpt-4o", model_provider="openai")
memory = MemorySaver()
agent = create_react_agent(llm, [run_python_code], checkpointer=memory)

# --- API Endpoint ---
@app.post("/chat")
async def chat_endpoint(request: Request):
    data = await request.json()
    query = data.get("query")
    thread_id = data.get("thread_id", "default")

    config = {"configurable": {"thread_id": thread_id}}

    result = await agent.ainvoke(
        {"messages": [("user", query)]},
        config=config
    )

    return {
        "response": result["messages"][-1].content,
        "thread_id": thread_id
    }

if __name__ == "__main__":
    uvicorn.run(app, host="0.0.0.0", port=8000)
```

**Usage:**
```bash
# Start the service
python app.py

# Query the agent
curl -X POST http://localhost:8000/chat \
  -H "Content-Type: application/json" \
  -d '{"query": "What is 2^32?", "thread_id": "session-1"}'
```

---

## Best Practices

### Client Lifecycle Management

- **Initialize once** at application startup, not per-request. Creating a new session on every request is expensive.
- **Clean up on shutdown** using `client.stop()` or `lifespan` context managers.
- **Handle exceptions** in your tool function — the LLM can recover from tool errors if you return a descriptive error string.

### Security

- **Never hardcode API keys** in source code. Use environment variables or a secrets manager.
- The AgentCube sandbox isolates code execution — the LLM cannot access your host filesystem or other system resources.
- Consider setting resource limits in your `CodeInterpreter` CRD to prevent runaway code from consuming excessive cluster resources.

### Conversational Memory

The `MemorySaver` in these examples stores conversation history **in-memory**. For production applications, use a persistent checkpointer (e.g., `PostgresSaver` or `RedisSaver` from LangGraph) to survive service restarts.

---

## Next Steps

- [Python SDK Guide](./code-interpreter-python-sdk.md) — Detailed reference for all SDK methods
- [API Reference](../api-reference.md) — Low-level REST API documentation
- [PCAP Analyzer Tutorial](../tutorials/pcap-analyzer.md) — A real-world example using AgentCube
