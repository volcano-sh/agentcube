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

"""E2E: MCP stdio transport — stdio_client spawns agentcube_code_interpreter_mcp (stdio)."""

from __future__ import annotations

import asyncio
import json
import os
import sys
import unittest
from typing import Any


def _tool_result_text(result: Any) -> str:
    parts: list[str] = []
    for block in getattr(result, "content", []) or []:
        t = getattr(block, "text", None)
        if isinstance(t, str):
            parts.append(t)
    return "\n".join(parts)


def _run_async(coro: Any) -> Any:
    return asyncio.run(coro)


class TestMCPCodeInterpreterStdioE2E(unittest.TestCase):
    """Requires ROUTER_URL, WORKLOAD_MANAGER_URL (same as other CodeInterpreter E2E)."""

    def test_stdio_initialize_list_tools_run_code(self):
        router = os.environ.get("ROUTER_URL")
        wm = os.environ.get("WORKLOAD_MANAGER_URL")
        if not router or not wm:
            raise unittest.SkipTest("ROUTER_URL and WORKLOAD_MANAGER_URL are required")

        ns = os.environ.get("AGENTCUBE_NAMESPACE", "agentcube")
        token = os.environ.get("API_TOKEN", "")
        env: dict[str, str] = {
            "ROUTER_URL": router,
            "WORKLOAD_MANAGER_URL": wm,
            "AGENTCUBE_NAMESPACE": ns,
            "CODE_INTERPRETER_NAME": "e2e-code-interpreter",
        }
        if token:
            env["API_TOKEN"] = token

        async def body():
            from mcp import ClientSession
            from mcp.client.stdio import StdioServerParameters, stdio_client

            params = StdioServerParameters(
                command=sys.executable,
                args=["-m", "agentcube_code_interpreter_mcp", "--transport", "stdio"],
                env=env,
            )
            async with stdio_client(params) as (read_stream, write_stream):
                async with ClientSession(read_stream, write_stream) as session:
                    init = await session.initialize()
                    self.assertIsNotNone(init.protocolVersion)
                    print(f"[MCP stdio E2E] initialize ok protocolVersion={init.protocolVersion!r}")

                    tools = await session.list_tools()
                    names = {t.name for t in tools.tools}
                    self.assertIn("run_code", names)
                    self.assertIn("write_file", names)
                    print(f"[MCP stdio E2E] list_tools ok count={len(tools.tools)}")

                    res = await session.call_tool(
                        "run_code",
                        {
                            "language": "python",
                            "code": "print(3+1)",
                            "session_reuse": False,
                        },
                    )
                    self.assertFalse(res.isError, _tool_result_text(res))
                    data = json.loads(_tool_result_text(res))
                    self.assertIn("4", (data.get("output") or ""), data)
                    print(f"[MCP stdio E2E] run_code ok output={data.get('output')!r}")

        _run_async(body())


if __name__ == "__main__":
    unittest.main(verbosity=2)
