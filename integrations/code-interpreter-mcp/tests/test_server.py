# Copyright The Volcano Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.

import asyncio
import os
import unittest
from unittest.mock import patch

from agentcube.exceptions import CommandExecutionError
from mcp import Client

from agentcube_code_interpreter_mcp.server import create_mcp_server


class _FakeClient:
    error = None

    def __init__(self, **_kwargs):
        self.session_id = "test-session"

    def run_code(self, _language, _code, timeout=None):
        del timeout
        raise self.error

    def execute_command(self, _command, timeout=None):
        del timeout
        raise self.error

    def stop(self):
        self.session_id = None


class TestCommandToolErrors(unittest.TestCase):
    def _call_tool(self, name, arguments, error):
        async def call():
            _FakeClient.error = error
            env = {
                "ROUTER_URL": "http://router.test",
                "WORKLOAD_MANAGER_URL": "http://manager.test",
            }
            with (
                patch.dict(os.environ, env),
                patch(
                    "agentcube_code_interpreter_mcp.server._import_client",
                    return_value=_FakeClient,
                ),
            ):
                server = create_mcp_server()
                async with Client(server) as client:
                    return await client.call_tool(name, arguments)

        return asyncio.run(call())

    def test_run_code_exposes_expected_execution_error(self):
        result = self._call_tool(
            "run_code",
            {"language": "python", "code": "print(x)"},
            CommandExecutionError(1, "NameError: name 'x' is not defined"),
        )

        self.assertTrue(result.is_error)
        self.assertIn("NameError: name 'x' is not defined", result.content[0].text)

    def test_execute_command_exposes_expected_execution_error(self):
        result = self._call_tool(
            "execute_command",
            {"command": "exit 7"},
            CommandExecutionError(7, "command failed"),
        )

        self.assertTrue(result.is_error)
        self.assertIn("Command failed (exit 7): command failed", result.content[0].text)


if __name__ == "__main__":
    unittest.main()
