# Using Code Interpreter via LangChain

You can integrate the AgentCube Code Interpreter (PicoD) with LangChain and LangGraph to build agents capable of executing code, solving mathematical problems, and processing data. This guide demonstrates how to wrap the `CodeInterpreterClient` as a LangChain tool and deploy it within a ReAct agent.

## Prerequisites

Before you begin, ensure you have the following installed:

*   **Python 3.9+**
*   **LangChain & LangGraph**: `pip install langchain langchain-core langgraph`
*   **AgentCube SDK**: `pip install agentcube-sdk`
*   **OpenAI SDK** (or other LLM provider): `pip install langchain-openai`

## Step 1: Initialize the Code Interpreter Client

The `CodeInterpreterClient` from the `agentcube` package manages the connection to the backend execution environment. You should handle the client's lifecycle (initialization and cleanup) to ensure sessions are maintained efficiently.

```python
from agentcube import CodeInterpreterClient

# Initialize for the session
ci_client = CodeInterpreterClient()
```

## Step 2: Define the LangChain Tool

To allow an LLM to interact with the interpreter, you must wrap the client execution logic in a tool. The `@tool` decorator simplifies this process.

```python
from langchain_core.tools import tool

# Global client reference (or manage via class/closure)
ci_client = None

@tool
def run_python_code(code: str) -> str:
    """
    Executes Python code in a sandboxed environment and returns the output.
    Use this tool to perform calculations, data analysis, or script execution.
    """
    global ci_client
    try:
        # Lazy initialization
        if ci_client is None:
            from agentcube import CodeInterpreterClient
            ci_client = CodeInterpreterClient()
        
        # Execute code
        return ci_client.run_code("python", code)
        
    except Exception as e:
        return f"Error executing code: {str(e)}"
```

## Step 3: Create and Run the Agent

Combine the tool with an LLM using LangGraph's prebuilt agent functions.

```python
from langchain.chat_models import init_chat_model
from langchain.agents import create_agent # or create_react_agent
from langgraph.checkpoint.memory import MemorySaver
from langchain_core.messages import HumanMessage
import os

# 1. Setup Environment
# WARNING: Never hardcode API keys in source code. Load credentials from environment variables or secure configuration.
os.environ["OPENAI_API_KEY"] = "your-api-key-here"  # Replace with your actual API key, preferably loaded securely

# 2. Initialize LLM
llm = init_chat_model("gpt-4o", model_provider="openai")

# 3. Define Tools
tools = [run_python_code]

# 4. Create Agent with Memory
memory = MemorySaver()
agent_graph = create_agent(llm, tools, checkpointer=memory)

# 5. Run the Agent
config = {"configurable": {"thread_id": "math-session-1"}}
query = "Calculate the 10th Fibonacci number using Python."

print(f"User: {query}")
result = agent_graph.invoke(
    {"messages": [HumanMessage(content=query)]},
    config=config
)

print(f"Agent: {result['messages'][-1].content}")
```

## Example: Math Agent Service (FastAPI)

The following full example demonstrates how to host the agent as a service using FastAPI. This setup manages the `CodeInterpreterClient` lifecycle using the application lifespan events.

```python
from fastapi import FastAPI, Request
from contextlib import asynccontextmanager
from langchain.chat_models import init_chat_model
from langchain_core.tools import tool
from langchain.agents import create_agent
from langgraph.checkpoint.memory import MemorySaver
from agentcube import CodeInterpreterClient
import uvicorn
import os

# --- Configuration ---
ci_client = None

@tool
def run_python_code(code: str) -> str:
    """Wrapper to run Python code inside Code Interpreter."""
    global ci_client
    if ci_client:
        return ci_client.run_code("python", code)
    return "Code Interpreter is not initialized."

@asynccontextmanager
async def lifespan(app: FastAPI):
    # Startup: Initialize Client
    global ci_client
    print("Initializing Code Interpreter Session...")
    ci_client = CodeInterpreterClient()
    yield
    # Shutdown: Cleanup
    if ci_client:
        print("Stopping Code Interpreter Session...")
        ci_client.stop()

# --- App Setup ---
app = FastAPI(lifespan=lifespan)

# Setup Agent
llm = init_chat_model("gpt-4o", model_provider="openai")
memory = MemorySaver()
agent_graph = create_agent(llm, [run_python_code], checkpointer=memory)

@app.post("/chat")
async def chat_endpoint(request: Request):
    data = await request.json()
    query = data.get("query")
    thread_id = data.get("thread_id", "default")
    
    config = {"configurable": {"thread_id": thread_id}}
    
    # Invoke Agent
    result = await agent_graph.ainvoke(
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
