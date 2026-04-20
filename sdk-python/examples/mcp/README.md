# AgentCube MCP Server Integration

This folder contains examples to integrate the AgentCube MCP Server with general agent interfaces (Claude Desktop, Cursor, OpenHands, etc.).

By using the MCP Server, agents can interact with AgentCube's remote, sandboxed environments.

## Files

- `claude_desktop_config.example.json`: Sample configuration snippet for [Claude Desktop App](https://claude.ai/download). Place this in your `~/Library/Application Support/Claude/claude_desktop_config.json` (Mac) or `%APPDATA%\Claude\claude_desktop_config.json` (Windows).
- `start_mcp_cursor.sh`: A wrapper shell script to define environment variables and launch the MCP server. Useful for general IDEs like [Cursor](https://www.cursor.com/) or CLI agents like [OpenHands](https://github.com/All-Hands-AI/OpenHands) where you configure a "command" MCP source.

## Documentation

For a full tutorial, please visit our Docusaurus documentation node: `docs/agentcube/docs/tutorials/mcp-integration.md` or check the main Python SDK `README.md`.
