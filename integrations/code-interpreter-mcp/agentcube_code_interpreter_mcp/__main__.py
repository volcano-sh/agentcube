# Copyright The Volcano Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.

"""CLI entry for AgentCube Code Interpreter MCP server."""

from __future__ import annotations

import argparse
import os

from agentcube_code_interpreter_mcp.server import create_mcp_server


def main() -> None:
    parser = argparse.ArgumentParser(description="AgentCube Code Interpreter MCP server")
    parser.add_argument(
        "--transport",
        choices=["stdio", "streamable-http"],
        default=os.environ.get("MCP_TRANSPORT", "stdio"),
        help="MCP transport (default: stdio, or MCP_TRANSPORT)",
    )
    parser.add_argument(
        "--host",
        default=os.environ.get("MCP_HOST", "127.0.0.1"),
        help="Listen host for streamable-http (default: MCP_HOST or 127.0.0.1)",
    )
    parser.add_argument(
        "--port",
        type=int,
        default=int(os.environ.get("MCP_PORT", "8000")),
        help="Listen port for streamable-http (default: MCP_PORT or 8000)",
    )
    args = parser.parse_args()
    mcp = create_mcp_server()
    if args.transport == "streamable-http":
        mcp.run(
            transport="streamable-http",
            host=args.host,
            port=args.port,
            stateless_http=True,
            json_response=True,
        )
    else:
        mcp.run(transport="stdio")


if __name__ == "__main__":
    main()
