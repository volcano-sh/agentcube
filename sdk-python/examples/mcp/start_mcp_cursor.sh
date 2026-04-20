#!/bin/bash
# 
# AgentCube MCP Server Startup Script for generic CLI agents (e.g., Cursor, OpenHands)
# 
# Usage: 
# Point your agent's MCP setup to this script as a "command" type.
# e.g., command: /path/to/agentcube/sdk-python/examples/mcp/start_mcp_cursor.sh

# 1. Provide your environment variables
export WORKLOAD_MANAGER_URL="http://127.0.0.1:8080"
export ROUTER_URL="http://127.0.0.1:8081"
export AUTH_TOKEN="optional-auth-token-if-needed"

# 2. Run the MCP server
# Make sure python3 is in your path and agentcube package is installed (`pip install agentcube`)
exec python3 -m agentcube.mcp_server --transport stdio --name agentcube-sandbox
