---
sidebar_position: 5
title: MCP Client Integrations
---

# Integrating AgentCube MCP Server with AI Agents

The AgentCube Python SDK provides an implementation of a Model Context Protocol (MCP) server. This server exposes the remote code execution and file management functionalities to any AI agent that supports the MCP standard, effectively giving these AI agents sandboxed execution environments, rather than having them run code directly on your local machine.

Below are examples of how to integrate the AgentCube MCP Server with popular AI agents: Claude Desktop, Cursor, and OpenHands (formerly OpenDevin).

## Prerequisites

Before configuring the agents, ensure that:
1. You have installed the AgentCube Python SDK (`pip install agentcube`).
2. You have your AgentCube environment variables ready (`WORKLOAD_MANAGER_URL`, `ROUTER_URL`, `AUTH_TOKEN`, etc.).
3. Python 3 is accessible from the command line environments of the agents.

## 1. Claude Desktop Integration

Claude Desktop natively supports reading MCP server configurations from its config file.

**Configuration File Location:**
- macOS: `~/Library/Application Support/Claude/claude_desktop_config.json`
- Windows: `%APPDATA%\Claude\claude_desktop_config.json`

Add the following configuration, replacing the environment variable values with your specific AgentCube endpoints:

```json
{
  "mcpServers": {
    "agentcube-code-interpreter": {
      "command": "python3",
      "args": [
        "-m",
        "agentcube.mcp_server",
        "--name",
        "my-claude-workspace",
        "--transport",
        "stdio"
      ],
      "env": {
        "WORKLOAD_MANAGER_URL": "http://<workload-manager-host>:<port>",
        "ROUTER_URL": "http://<router-host>:<port>",
        "AUTH_TOKEN": "your-auth-token"
      }
    }
  }
}
```

Restart Claude Desktop. You will now see tools like `run_code`, `execute_command`, `read_file`, and `write_file` available with the AgentCube icon, indicating they are running in the secure AgentCube sandbox.

## 2. Cursor Integration

Cursor integrates with MCP servers in its settings menu.

1. Open Cursor Settings.
2. Navigate to **Features** -> **MCP**.
3. Click **Add New MCP Server**.
4. Set the following fields:
   - **Name**: `AgentCube`
   - **Type**: `command`
   - **Command**: `python3 -m agentcube.mcp_server --transport stdio`
5. Since Cursor executes the command in your local environment, you need to ensure the environment variables are exposed where Cursor is launched, or wrap the startup in a shell script:

**AgentCube-Cursor Startup Script (`start_agentcube_mcp.sh`):**

```bash
#!/bin/bash
export WORKLOAD_MANAGER_URL="http://<workload-manager-host>:<port>"
export ROUTER_URL="http://<router-host>:<port>"
export AUTH_TOKEN="your-auth-token"

# Start the AgentCube MCP server
python3 -m agentcube.mcp_server --transport stdio
```

Make the script executable (`chmod +x start_agentcube_mcp.sh`), and in Cursor's **Command** field, point to this script: `/path/to/start_agentcube_mcp.sh`.

## 3. OpenHands (OpenClaw) Integration

OpenHands and other general CLI agents can interact with AgentCube's MCP server via standard configurations or initialization command line arguments.

By default, the AgentCube MCP server supports HTTP/SSE transport. You can spin up the MCP server as a standalone process and have OpenHands connect to it:

### Start the Server

```bash
export WORKLOAD_MANAGER_URL="http://<workload-manager-host>:<port>"
export ROUTER_URL="http://<router-host>:<port>"

# Start via HTTP
python3 -m agentcube.mcp_server --transport streamable-http --port 8000
```

### Connect OpenHands

You can configure OpenHands to use standard HTTP MCP connections. In your `config.toml` or via the respective MCP arguments in OpenHands, reference `http://127.0.0.1:8000`.

*Note: For `stdio` connection in OpenHands or similar CLI agents, you would configure the tool executing command similar to Claude Desktop.*

## 4. Claude Code (CLI) Integration

For the Claude CLI wrapper (`claude` tool from Anthropic):

You can configure the MCP servers using the `mcp-servers.json` or by passing the server command directly.

```bash
claude config add mcp-server agentcube "python3 -m agentcube.mcp_server --transport stdio"
```

*Ensure the environment variables (`WORKLOAD_MANAGER_URL`, etc.) are exported in the terminal where you run `claude`.*

When using Claude CLI with this configuration, you can prompt: "Write a python script to parse a CSV and execute it." The AI will utilize AgentCube rather than running code on your physical workstation.
