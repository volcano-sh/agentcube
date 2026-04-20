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

"""E2E: MCP server running as a Pod in Kubernetes (streamable-http via port-forward).

run_e2e.sh sets MCP_K8S_MCP_URL (e.g. http://127.0.0.1:19446/mcp) after deploying the MCP Deployment.
"""

from __future__ import annotations

import asyncio
import json
import os
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


class TestMCPCodeInterpreterK8sDeploymentE2E(unittest.TestCase):
    def test_in_cluster_mcp_http_roundtrip(self):
        url = os.environ.get("MCP_K8S_MCP_URL", "").strip()
        if not url:
            raise unittest.SkipTest("MCP_K8S_MCP_URL not set (in-cluster MCP E2E not started)")

        async def body():
            import httpx
            from mcp import ClientSession
            from mcp.client.streamable_http import streamable_http_client

            async with httpx.AsyncClient(timeout=120.0) as http_client:
                async with streamable_http_client(url, http_client=http_client) as (
                    read_stream,
                    write_stream,
                    _,
                ):
                    async with ClientSession(read_stream, write_stream) as session:
                        await session.initialize()
                        print(f"[MCP K8s E2E] initialize ok url={url!r}")

                        tools = await session.list_tools()
                        names = {t.name for t in tools.tools}
                        self.assertIn("run_code", names)
                        self.assertIn("stop_session", names)

                        res = await session.call_tool(
                            "run_code",
                            {
                                "language": "python",
                                "code": "print(7-2)",
                                "session_reuse": False,
                            },
                        )
                        self.assertFalse(res.isError, _tool_result_text(res))
                        data = json.loads(_tool_result_text(res))
                        self.assertIn("5", (data.get("output") or ""), data)
                        print(f"[MCP K8s E2E] run_code via in-cluster pod ok output={data.get('output')!r}")

        _run_async(body())


if __name__ == "__main__":
    unittest.main(verbosity=2)
