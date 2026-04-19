#!/usr/bin/env python3
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
Unit tests for MCP Server module.

These tests verify the MCP server functionality and structure without
requiring a running AgentCube cluster.
"""

import os
import sys
import unittest
from unittest.mock import MagicMock, patch
import inspect


class TestMCPServerCreation(unittest.TestCase):
    """Tests for MCP server creation."""

    def setUp(self):
        """Set up test environment."""
        os.environ.setdefault("WORKLOAD_MANAGER_URL", "http://localhost:8080")
        os.environ.setdefault("ROUTER_URL", "http://localhost:8081")

    def test_server_creation(self):
        """Test server can be created."""
        from agentcube.mcp_server import create_mcp_server

        with patch("agentcube.mcp_server.CodeInterpreterClient"):
            mcp = create_mcp_server(name="test-ci")
            self.assertEqual(mcp.name, "test-ci")

    def test_default_server_name(self):
        """Test default server name."""
        from agentcube.mcp_server import create_mcp_server

        with patch("agentcube.mcp_server.CodeInterpreterClient"):
            mcp = create_mcp_server()
            self.assertEqual(mcp.name, "agentcube-code-interpreter")

    def test_custom_name(self):
        """Test custom server name."""
        from agentcube.mcp_server import create_mcp_server

        with patch("agentcube.mcp_server.CodeInterpreterClient"):
            mcp = create_mcp_server(name="my-server")
            self.assertEqual(mcp.name, "my-server")

    def test_create_mcp_server_signature(self):
        """Test create_mcp_server function signature."""
        from agentcube.mcp_server import create_mcp_server

        sig = inspect.signature(create_mcp_server)
        params = list(sig.parameters.keys())

        self.assertIn("name", params)
        self.assertIn("workload_manager_url", params)
        self.assertIn("router_url", params)
        self.assertIn("auth_token", params)
        self.assertIn("namespace", params)
        self.assertIn("ttl", params)


class TestMCPServerToolsRegistered(unittest.TestCase):
    """Tests to verify tools are registered on the server."""

    def setUp(self):
        """Set up test environment."""
        os.environ.setdefault("WORKLOAD_MANAGER_URL", "http://localhost:8080")
        os.environ.setdefault("ROUTER_URL", "http://localhost:8081")

    def test_server_has_list_tools_method(self):
        """Test server has list_tools method."""
        from agentcube.mcp_server import create_mcp_server

        with patch("agentcube.mcp_server.CodeInterpreterClient"):
            mcp = create_mcp_server(name="test")
            self.assertTrue(hasattr(mcp, "list_tools"))
            self.assertTrue(callable(mcp.list_tools))

    @patch("agentcube.mcp_server.CodeInterpreterClient")
    def test_list_tools_returns_tool_list(self, mock_class):
        """Test list_tools returns a list of tools."""
        mock_client = MagicMock()
        mock_class.return_value = mock_client

        from agentcube.mcp_server import create_mcp_server
        mcp = create_mcp_server(name="test")

        async def get_tools():
            result = await mcp.list_tools()
            return result

        import asyncio
        tools = asyncio.get_event_loop().run_until_complete(get_tools())

        tool_names = [t.name for t in tools]
        self.assertIn("run_code", tool_names)
        self.assertIn("execute_command", tool_names)
        self.assertIn("write_file", tool_names)
        self.assertIn("list_files", tool_names)
        self.assertIn("upload_file", tool_names)
        self.assertIn("download_file", tool_names)

    @patch("agentcube.mcp_server.CodeInterpreterClient")
    def test_run_code_tool_schema(self, mock_class):
        """Test run_code tool has correct schema."""
        mock_client = MagicMock()
        mock_class.return_value = mock_client

        from agentcube.mcp_server import create_mcp_server
        mcp = create_mcp_server(name="test")

        async def get_tools():
            result = await mcp.list_tools()
            return result

        import asyncio
        tools = asyncio.get_event_loop().run_until_complete(get_tools())

        run_code_tool = next((t for t in tools if t.name == "run_code"), None)
        self.assertIsNotNone(run_code_tool)
        self.assertIn("language", run_code_tool.inputSchema.get("properties", {}))
        self.assertIn("code", run_code_tool.inputSchema.get("properties", {}))


class TestMCPToolInvocations(unittest.TestCase):
    """Test tool invocations work correctly."""

    def setUp(self):
        """Set up test environment."""
        os.environ.setdefault("WORKLOAD_MANAGER_URL", "http://localhost:8080")
        os.environ.setdefault("ROUTER_URL", "http://localhost:8081")

    @patch("agentcube.mcp_server.CodeInterpreterClient")
    def test_run_code_invocation(self, mock_class):
        """Test run_code can be invoked via call_tool."""
        mock_client = MagicMock()
        mock_client.run_code.return_value = "42\n"
        mock_class.return_value = mock_client

        from agentcube.mcp_server import create_mcp_server
        mcp = create_mcp_server(name="test")

        async def call_tool():
            result = await mcp.call_tool("run_code", {"language": "python", "code": "print(42)"})
            return result

        import asyncio
        asyncio.get_event_loop().run_until_complete(call_tool())

        mock_client.run_code.assert_called_once_with("python", "print(42)", None)

    @patch("agentcube.mcp_server.CodeInterpreterClient")
    def test_execute_command_invocation(self, mock_class):
        """Test execute_command can be invoked."""
        mock_client = MagicMock()
        mock_client.execute_command.return_value = "hello\n"
        mock_class.return_value = mock_client

        from agentcube.mcp_server import create_mcp_server
        mcp = create_mcp_server(name="test")

        import asyncio
        asyncio.get_event_loop().run_until_complete(
            mcp.call_tool("execute_command", {"command": "echo hello"})
        )

        mock_client.execute_command.assert_called_once_with("echo hello", None)

    @patch("agentcube.mcp_server.CodeInterpreterClient")
    def test_write_file_invocation(self, mock_class):
        """Test write_file can be invoked."""
        mock_client = MagicMock()
        mock_class.return_value = mock_client

        from agentcube.mcp_server import create_mcp_server
        mcp = create_mcp_server(name="test")

        import asyncio
        asyncio.get_event_loop().run_until_complete(
            mcp.call_tool("write_file", {"content": "test", "remote_path": "test.txt"})
        )

        mock_client.write_file.assert_called_once_with("test", "test.txt")

    @patch("agentcube.mcp_server.CodeInterpreterClient")
    def test_list_files_invocation(self, mock_class):
        """Test list_files can be invoked."""
        mock_client = MagicMock()
        mock_client.list_files.return_value = [
            {"name": "file1.py", "size": 1024, "is_dir": False}
        ]
        mock_class.return_value = mock_client

        from agentcube.mcp_server import create_mcp_server
        mcp = create_mcp_server(name="test")

        import asyncio
        result = asyncio.get_event_loop().run_until_complete(
            mcp.call_tool("list_files", {"path": "."})
        )

        mock_client.list_files.assert_called_once_with(".")
        self.assertIn("file1.py", str(result))


class TestMCPServerCLI(unittest.TestCase):
    """Tests for MCP server CLI."""

    def test_cli_import(self):
        """Test CLI can be imported."""
        from agentcube import mcp_server

        self.assertTrue(hasattr(mcp_server, "main"))
        self.assertTrue(hasattr(mcp_server, "create_mcp_server"))

    def test_main_exists(self):
        """Test main function exists."""
        from agentcube.mcp_server import main

        self.assertTrue(callable(main))


if __name__ == "__main__":
    print("Starting MCP Server Unit Tests...")

    if "--verbose" not in sys.argv:
        sys.argv.append("--verbose")

    unittest.main(verbosity=2)
