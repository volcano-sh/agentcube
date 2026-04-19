# Copyright The Volcano Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""
MCP Server for AgentCube Code Interpreter.

This module provides an MCP (Model Context Protocol) server that exposes
AgentCube Code Interpreter functionality as MCP tools.

Usage:
    # Run as stdio server
    python -m agentcube.mcp_server

    # Run as HTTP server
    python -m agentcube.mcp_server --transport streamable-http --port 8000

    # Or programmatically
    from agentcube.mcp_server import create_mcp_server
    server = create_mcp_server()
    server.run(transport="streamable-http", port=8000)
"""

from __future__ import annotations

import json
import os
import tempfile
from typing import Optional

from mcp.server.fastmcp import FastMCP

from agentcube.code_interpreter import CodeInterpreterClient


def create_mcp_server(
    name: str = "agentcube-code-interpreter",
    workload_manager_url: Optional[str] = None,
    router_url: Optional[str] = None,
    auth_token: Optional[str] = None,
    namespace: str = "default",
    ttl: int = 3600,
) -> FastMCP:
    """
    Create an MCP server for AgentCube Code Interpreter.

    Args:
        name: Name of the CodeInterpreter template (CRD name).
        workload_manager_url: URL of WorkloadManager (Control Plane).
        router_url: URL of Router (Data Plane).
        auth_token: Auth token for Kubernetes/WorkloadManager.
        namespace: Kubernetes namespace.
        ttl: Time to live (seconds) for sessions.

    Returns:
        A FastMCP server instance.
    """
    mcp = FastMCP(name, json_response=True)

    workload_manager_url = workload_manager_url or os.getenv("WORKLOAD_MANAGER_URL")
    router_url = router_url or os.getenv("ROUTER_URL")
    auth_token = auth_token or os.getenv("AUTH_TOKEN")

    _client: Optional[CodeInterpreterClient] = None

    def get_client() -> CodeInterpreterClient:
        nonlocal _client
        if _client is None:
            _client = CodeInterpreterClient(
                name=name,
                namespace=namespace,
                ttl=ttl,
                workload_manager_url=workload_manager_url,
                router_url=router_url,
                auth_token=auth_token,
            )
        return _client

    @mcp.tool()
    def run_code(language: str, code: str, timeout: Optional[float] = None) -> str:
        """
        Execute a code snippet in the remote code interpreter environment.

        Args:
            language: Programming language (python, bash, sh).
            code: The code to execute.
            timeout: Optional execution timeout in seconds.

        Returns:
            The stdout output from the code execution.
        """
        client = get_client()
        return client.run_code(language, code, timeout)

    @mcp.tool()
    def execute_command(command: str, timeout: Optional[float] = None) -> str:
        """
        Execute a shell command in the remote code interpreter environment.

        Args:
            command: The shell command to execute.
            timeout: Optional execution timeout in seconds.

        Returns:
            The stdout output from the command.
        """
        client = get_client()
        return client.execute_command(command, timeout)

    @mcp.tool()
    def write_file(content: str, remote_path: str) -> str:
        """
        Write content to a file in the remote code interpreter environment.

        Args:
            content: The content to write to the file.
            remote_path: The destination path in the remote environment.

        Returns:
            Success message with the file path.
        """
        client = get_client()
        client.write_file(content, remote_path)
        return f"Successfully wrote to {remote_path}"

    @mcp.tool()
    def upload_file(local_path: str, remote_path: str) -> str:
        """
        Upload a local file to the remote code interpreter environment.

        Args:
            local_path: Path to the local file.
            remote_path: Destination path in the remote environment.

        Returns:
            Success message with the file paths.
        """
        client = get_client()
        client.upload_file(local_path, remote_path)
        return f"Successfully uploaded {local_path} to {remote_path}"

    @mcp.tool()
    def download_file(remote_path: str, local_path: str) -> str:
        """
        Download a file from the remote code interpreter environment.

        Args:
            remote_path: Path to the file in the remote environment.
            local_path: Local destination path.

        Returns:
            Success message with the file paths.
        """
        client = get_client()
        client.download_file(remote_path, local_path)
        return f"Successfully downloaded {remote_path} to {local_path}"

    @mcp.tool()
    def list_files(path: str = ".") -> str:
        """
        List files and directories in the remote code interpreter environment.

        Args:
            path: The directory path to list. Defaults to ".".

        Returns:
            JSON string of file entries.
        """
        client = get_client()
        files = client.list_files(path)
        return json.dumps(files, default=str)

    @mcp.resource("workspace://{path}")
    def get_file(path: str) -> str:
        """
        Read a file from the remote code interpreter environment.

        Args:
            path: The file path to read.

        Returns:
            The file content as a string.
        """
        client = get_client()
        with tempfile.NamedTemporaryFile(mode='w', delete=False, suffix='.txt') as tmp:
            tmp_path = tmp.name
        try:
            client.download_file(path, tmp_path)
            with open(tmp_path, 'r') as f:
                return f.read()
        finally:
            if os.path.exists(tmp_path):
                os.remove(tmp_path)

    return mcp


def main():
    import argparse

    parser = argparse.ArgumentParser(description="AgentCube Code Interpreter MCP Server")
    parser.add_argument(
        "--transport",
        choices=["stdio", "streamable-http"],
        default="stdio",
        help="Transport type (default: stdio)",
    )
    parser.add_argument(
        "--port",
        type=int,
        default=8000,
        help="Port for HTTP transport (default: 8000)",
    )
    parser.add_argument(
        "--host",
        default="127.0.0.1",
        help="Host for HTTP transport (default: 127.0.0.1)",
    )
    parser.add_argument(
        "--name",
        default="agentcube-code-interpreter",
        help="MCP server name",
    )
    parser.add_argument(
        "--namespace",
        default="default",
        help="Kubernetes namespace",
    )
    parser.add_argument(
        "--ttl",
        type=int,
        default=3600,
        help="Session TTL in seconds",
    )

    args = parser.parse_args()

    mcp = create_mcp_server(
        name=args.name,
        namespace=args.namespace,
        ttl=args.ttl,
    )

    if args.transport == "streamable-http":
        mcp.run(transport="streamable-http", host=args.host, port=args.port)
    else:
        mcp.run(transport="stdio")


if __name__ == "__main__":
    main()
