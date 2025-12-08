from langchain_core.messages import HumanMessage
from langchain.chat_models import init_chat_model
from langgraph.checkpoint.memory import MemorySaver
from langchain_core.tools import tool
from contextlib import asynccontextmanager
from langchain.agents import create_agent
from fastapi import FastAPI, Request
from fastapi.responses import JSONResponse
import uvicorn
import os
from dotenv import load_dotenv

# Load environment variables from .env file
load_dotenv()

# Configuration from environment variables
api_key = os.getenv("OPENAI_API_KEY", "")
api_base_url = os.getenv("OPENAI_API_BASE", "")
model_name = os.getenv("OPENAI_MODEL", "DeepSeek-V3")

# Global variable for the Code Interpreter client
ci_client = None

@tool
def run_python_code(code: str) -> str:
    """Wrapper to run Python code inside Code Interpreter."""
    global ci_client
    try:
        if ci_client is None:
            from agentcube import CodeInterpreterClient
            # Initialize the client (Lazy Loading)
            # This maintains the session across multiple tool calls
            ci_client = CodeInterpreterClient(ttl=600)
        
        # Run the code
        return ci_client.run_code("python", code)

    except Exception as e:
        error_msg = f"Error executing Python code: Could not connect to Code Interpreter backend. Details: {e}"
        print(f"ERROR: {error_msg}")
        # If the session is broken, we might want to reset the client so it tries to reconnect next time
        # ci_client = None 
        return error_msg


# Define tools
tools = [run_python_code]

# Initialize LLM
try:
    llm = init_chat_model(
        model_name,
        model_provider="openai",
        base_url=api_base_url,
        api_key=api_key,
        temperature=0.1
    )
except Exception as e:
    print(f"Warning: init_chat_model failed ({e}), falling back to ChatOpenAI")
    from langchain_openai import ChatOpenAI
    llm = ChatOpenAI(
        model=model_name,
        base_url=api_base_url,
        api_key=api_key,
        temperature=0.1
    )

# Initialize Memory
memory = MemorySaver()

# Create Agent
# We use create_react_agent which is the standard for modern LangGraph agents
# Added checkpointer for conversation history
agent_graph = create_agent(llm, tools, checkpointer=memory)

@asynccontextmanager
async def lifespan(app: FastAPI):
    # Startup
    # We delay initialization until the first request needs it (Lazy Loading)
    global ci_client
    yield
    
    # Shutdown
    if ci_client:
        try:
            print("Stopping Code Interpreter session...")
            ci_client.stop()
        except Exception as e:
            print(f"Error stopping Code Interpreter session: {e}")

# FastAPI app with lifespan
app = FastAPI(lifespan=lifespan)

@app.post("/")
async def run_agent(request: Request):
    try:
        if not api_key:
            return JSONResponse(status_code=500, content={"error": "Configuration Error: OPENAI_API_KEY environment variable is not set."})
        
        data = await request.json()
        query = data.get("query", "")
        # Default to "default_thread" to allow simple testing of continuity without managing IDs
        thread_id = data.get("thread_id", "default_thread")
        
        print(f"Received query: {query} (thread_id: {thread_id})")
        
        config = {"configurable": {"thread_id": thread_id}}
        
        # Invoke the agent
        # LangGraph expects 'messages' key in the input dictionary
        result = await agent_graph.ainvoke(
            {"messages": [HumanMessage(content=query)]},
            config=config
        )
        
        print(f"Agent ainvoke result: {result}")
        # Result contains 'messages' key with the conversation history
        # The last message is the AI's response
        last_message = result["messages"][-1]
        print(f"Agent last message: {last_message}")
        return {"response": last_message.content, "thread_id": thread_id}
    except Exception as e:
        import traceback
        traceback.print_exc()
        return JSONResponse(status_code=500, content={"error": f"Internal Processing Error: {str(e)}"})

if __name__ == "__main__":
    uvicorn.run(app, host="0.0.0.0", port=8080)