#!/usr/bin/env python3
"""
Math Agent - An example AI agent using LangChain and LangGraph.

This agent provides an HTTP API for solving math problems using Python code.
"""

import os
import json
from http.server import HTTPServer, BaseHTTPRequestHandler
from typing import Dict, Any
import datetime

from dotenv import load_dotenv
from langchain_core.messages import HumanMessage
from langchain.chat_models import init_chat_model
from langgraph.checkpoint.memory import MemorySaver
from langchain_core.tools import tool
from langchain.agents import create_agent
from agentcube import CodeInterpreterClient
from pydantic import BaseModel, Field

# Load environment variables from .env file
load_dotenv()

# Configuration from environment variables
api_key = os.getenv("OPENAI_API_KEY", "")
api_base_url = os.getenv("OPENAI_API_BASE", "")
model_name = os.getenv("OPENAI_MODEL", "DeepSeek-V3")

class RunPythonCodeArgs(BaseModel):
    code: str = Field(description="The python code to execute.")

@tool(args_schema=RunPythonCodeArgs)
def run_python_code(code: str) -> str:
    """
    Executes python code in a secure sandbox.
    Use this tool to perform calculations, analyze data, or run any python script.
    The code is executed in a persistent session, so variables defined in previous calls are available.
    """
    try:
        # Initialize the client for each call, as requested.
        # This will create a new session for each tool invocation.
        ci_client = CodeInterpreterClient()
        
        # Run the code
        return ci_client.run_code("python", code)

    except Exception as e:
        error_msg = f"Error executing Python code: Could not connect to Code Interpreter backend. Details: {e}"
        print(f"ERROR: {error_msg}")
        return error_msg


# Initialize Agent components
def initialize_agent():
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
    return create_agent(llm, tools, checkpointer=memory)


# Initialize global agent graph
agent_graph = initialize_agent()


class MathAgentHandler(BaseHTTPRequestHandler):
    """HTTP handler for the Math Agent."""

    def do_GET(self):
        """Handle GET requests."""
        if self.path == '/health':
            self._send_json_response({"status": "healthy", "agent": "math-agent"})
        elif self.path == '/':
            self._send_json_response({
                "message": "Hello from Math Agent!",
                "endpoints": [
                    "GET /health - Health check",
                    "POST / - Run agent query"
                ]
            })
        else:
            self._send_error(404, "Endpoint not found")

    def do_POST(self):
        """Handle POST requests."""
        if self.path == '/':
            self._handle_run_agent()
        else:
            self._send_error(404, "Endpoint not found")

    def _handle_run_agent(self):
        """Handle agent execution requests."""
        try:
            if not api_key:
                self._send_error(500, "Configuration Error: OPENAI_API_KEY environment variable is not set.")
                return

            content_length = int(self.headers['Content-Length'])
            post_data = self.rfile.read(content_length)
            data = json.loads(post_data.decode('utf-8'))

            query = data.get("query", "")
            thread_id = data.get("thread_id", "default_thread")
            
            print(f"Received query: {query} (thread_id: {thread_id})")
            
            config = {"configurable": {"thread_id": thread_id}}
            
            # Invoke the agent synchronously
            result = agent_graph.invoke(
                {"messages": [HumanMessage(content=query)]},
                config=config
            )
            
            # Result contains 'messages' key with the conversation history
            # The last message is the AI's response
            last_message = result["messages"][-1]
            print(f"Agent response: {last_message.content}")

            response = {
                "response": last_message.content,
                "thread_id": thread_id,
                "agent": "math-agent"
            }

            self._send_json_response(response)

        except (json.JSONDecodeError, KeyError) as e:
            self._send_error(400, f"Invalid request: {str(e)}")
        except Exception as e:
            import traceback
            traceback.print_exc()
            self._send_error(500, f"Internal Processing Error: {str(e)}")

    def _send_json_response(self, data: Dict[str, Any], status_code: int = 200):
        """Send JSON response."""
        self.send_response(status_code)
        self.send_header('Content-type', 'application/json')
        self.send_header('Access-Control-Allow-Origin', '*')
        self.end_headers()

        response = json.dumps(data, indent=2)
        self.wfile.write(response.encode('utf-8'))

    def _send_error(self, status_code: int, message: str):
        """Send error response."""
        self.send_response(status_code)
        self.send_header('Content-type', 'application/json')
        self.end_headers()

        error_response = json.dumps({
            "error": message,
            "status_code": status_code
        }, indent=2)

        self.wfile.write(error_response.encode('utf-8'))

    def log_message(self, format, *args):
        """Override log_message to reduce noise."""
        pass


def main():
    """Main function to run the Math Agent."""
    port = int(os.environ.get('PORT', 8080))

    print(f"Starting Math Agent on port {port} at {datetime.datetime.now().strftime('%Y-%m-%d %H:%M:%S')}")

    server_address = ('', port)
    httpd = HTTPServer(server_address, MathAgentHandler)

    try:
        print(f"Math Agent is running on port {port}")
        httpd.serve_forever()
    except KeyboardInterrupt:
        print(f"\nShutting down Math Agent")
        httpd.server_close()


if __name__ == "__main__":
    main()
