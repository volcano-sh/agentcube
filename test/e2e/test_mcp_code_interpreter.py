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

"""E2E: AgentCube Code Interpreter MCP server (streamable-http) against live Router/WM.

Mirrors ``test_codeinterpreter.py`` themes: stateless runs, file write/list/read (session reuse + ``stop_session``).
Also exercises MCP lifecycle: initialize, Initialized notification path, ping, tools/list and schemas.
Simple ``run_code`` smoke is covered by ``test_mcp_code_interpreter_stdio.py`` and ``test_mcp_code_interpreter_k8s.py``.
"""

from __future__ import annotations

import asyncio
import json
import os
import socket
import subprocess
import sys
import tempfile
import time
import unittest
from contextlib import asynccontextmanager
from typing import Any, AsyncIterator

# Must match tools registered in integrations/code-interpreter-mcp/server.py
EXPECTED_MCP_TOOLS = frozenset(
    {
        "run_code",
        "execute_command",
        "write_file",
        "list_files",
        "upload_file",
        "download_file",
        "stop_session",
    }
)


def _wait_tcp(host: str, port: int, timeout_s: float = 45.0) -> None:
    deadline = time.monotonic() + timeout_s
    last_err: OSError | None = None
    while time.monotonic() < deadline:
        try:
            with socket.create_connection((host, port), timeout=1.0):
                return
        except OSError as e:
            last_err = e
            time.sleep(0.2)
    raise RuntimeError(f"timeout waiting for {host}:{port}: {last_err}")


def _tool_result_text(result: Any) -> str:
    parts: list[str] = []
    for block in getattr(result, "content", []) or []:
        t = getattr(block, "text", None)
        if isinstance(t, str):
            parts.append(t)
    return "\n".join(parts)


@asynccontextmanager
async def _mcp_client_session(
    host: str,
    port: int,
    *,
    request_timeout: float = 120.0,
) -> AsyncIterator[tuple[Any, Any]]:
    """MCP Streamable HTTP client session; closes cleanly on exit."""
    import httpx
    from mcp import ClientSession
    from mcp.client.streamable_http import streamable_http_client

    url = f"http://{host}:{port}/mcp"
    async with httpx.AsyncClient(timeout=request_timeout) as http_client:
        async with streamable_http_client(url, http_client=http_client) as (
            read_stream,
            write_stream,
            _,
        ):
            async with ClientSession(read_stream, write_stream) as session:
                init_result = await session.initialize()
                yield session, init_result


def _run_async(coro: Any) -> Any:
    return asyncio.run(coro)


class TestMCPCodeInterpreterE2E(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.host = os.environ.get("MCP_E2E_HOST", "127.0.0.1")
        cls.port = int(os.environ.get("MCP_E2E_PORT", "19245"))
        cls.router = os.environ["ROUTER_URL"]
        cls.wm = os.environ["WORKLOAD_MANAGER_URL"]
        cls.token = os.environ.get("API_TOKEN", "")
        cls.ns = os.environ.get("AGENTCUBE_NAMESPACE", "agentcube")

        print(
            "\n[MCP E2E] environment: "
            f"namespace={cls.ns}, router={cls.router}, workload_manager={cls.wm}, "
            f"mcp_listen=http://{cls.host}:{cls.port}/mcp\n"
        )

        env = os.environ.copy()
        env.update(
            {
                "ROUTER_URL": cls.router,
                "WORKLOAD_MANAGER_URL": cls.wm,
                "AGENTCUBE_NAMESPACE": cls.ns,
                "CODE_INTERPRETER_NAME": "e2e-code-interpreter",
                "MCP_TRANSPORT": "streamable-http",
                "MCP_HOST": cls.host,
                "MCP_PORT": str(cls.port),
            }
        )
        if cls.token:
            env["API_TOKEN"] = cls.token

        cls.proc = subprocess.Popen(
            [
                sys.executable,
                "-m",
                "agentcube_code_interpreter_mcp",
                "--transport",
                "streamable-http",
                "--host",
                cls.host,
                "--port",
                str(cls.port),
            ],
            env=env,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )
        try:
            _wait_tcp(cls.host, cls.port, timeout_s=60.0)
        except Exception as e:
            cls.proc.kill()
            raise RuntimeError(
                f"MCP server failed to bind {cls.host}:{cls.port} (process exit code {cls.proc.poll()})"
            ) from e
        print(f"[MCP E2E] subprocess MCP server started (pid={cls.proc.pid})\n")

    @classmethod
    def tearDownClass(cls):
        if getattr(cls, "proc", None) is not None:
            cls.proc.terminate()
            try:
                cls.proc.wait(timeout=15)
            except subprocess.TimeoutExpired:
                cls.proc.kill()
            print("[MCP E2E] MCP server subprocess stopped\n")

    def test_mcp_protocol_initialize_ping_list_tools(self):
        """MCP: initialize, ping, tools/list; assert full tool surface and inputSchema."""

        async def body():
            async with _mcp_client_session(self.host, self.port) as (session, init_result):
                self.assertIsNotNone(init_result.protocolVersion, "initialize must return protocolVersion")
                print(f"[MCP E2E] initialize protocolVersion={init_result.protocolVersion!r}")
                si = init_result.serverInfo
                if si is not None:
                    print(f"[MCP E2E] serverInfo.name={getattr(si, 'name', None)!r}")

                ping = await session.send_ping()
                self.assertIsNotNone(ping)
                print("[MCP E2E] ping ok")

                tools_resp = await session.list_tools()
                names = {t.name for t in tools_resp.tools}
                self.assertEqual(
                    names,
                    EXPECTED_MCP_TOOLS,
                    f"list_tools must expose exactly {EXPECTED_MCP_TOOLS}, got {names}",
                )
                for t in tools_resp.tools:
                    self.assertTrue(
                        (t.description or "").strip(),
                        f"tool {t.name!r} must have a non-empty description",
                    )
                    schema = getattr(t, "inputSchema", None) or {}
                    self.assertEqual(
                        schema.get("type"),
                        "object",
                        f"tool {t.name!r} inputSchema.type must be object",
                    )
                    props = schema.get("properties")
                    if not isinstance(props, dict):
                        self.fail(f"tool {t.name!r} inputSchema.properties must be dict, got {props!r}")
                    print(f"[MCP E2E] tool {t.name!r} input properties: {list(props.keys())}")

        _run_async(body())

    def test_mcp_execute_command(self):
        """Covers execute_command tool."""

        async def body():
            async with _mcp_client_session(self.host, self.port) as (session, _):
                res = await session.call_tool(
                    "execute_command",
                    {
                        "command": "echo mcp-cmd-ok",
                        "session_reuse": False,
                    },
                )
                self.assertFalse(res.isError, _tool_result_text(res))
                data = json.loads(_tool_result_text(res))
                self.assertIn("mcp-cmd-ok", data.get("output", ""), data)
                print(f"[MCP E2E] execute_command ok: {data!r}")

        _run_async(body())

    def test_mcp_stateless_execution_nameerror(self):
        """SDK case2 equivalent: two run_code calls on same session; second fails (isError)."""

        async def body():
            async with _mcp_client_session(self.host, self.port) as (session, _):
                r1 = await session.call_tool(
                    "run_code",
                    {
                        "language": "python",
                        "code": "x = 10\nprint('defined')",
                        "session_reuse": True,
                    },
                )
                self.assertFalse(r1.isError, _tool_result_text(r1))
                sid = json.loads(_tool_result_text(r1))["session_id"]
                print(f"[MCP E2E] stateless step1 session_id={sid!r}")

                r2 = await session.call_tool(
                    "run_code",
                    {
                        "language": "python",
                        "code": "print(x)",
                        "session_id": sid,
                        "session_reuse": True,
                    },
                )
                self.assertTrue(r2.isError, "stateless print(x) must surface as MCP tool error")
                err_text = _tool_result_text(r2)
                self.assertTrue(
                    "NameError" in err_text or "name 'x' is not defined" in err_text or "not defined" in err_text,
                    err_text,
                )
                print(f"[MCP E2E] stateless step2 error as expected: {err_text[:200]!r}...")

                r3 = await session.call_tool("stop_session", {"session_id": sid})
                self.assertFalse(r3.isError, _tool_result_text(r3))

        _run_async(body())

    def test_mcp_write_list_run_code_file_workflow(self):
        """SDK case3 light: write_file -> list_files -> run_code reads file; cross-tool session_reuse + stop_session."""

        async def body():
            marker = f"mcp-list-{int(time.time())}"
            async with _mcp_client_session(self.host, self.port) as (session, _):
                w = await session.call_tool(
                    "write_file",
                    {
                        "content": marker,
                        "remote_path": "mcp_marker.txt",
                        "session_reuse": True,
                    },
                )
                self.assertFalse(w.isError, _tool_result_text(w))
                sid = json.loads(_tool_result_text(w))["session_id"]

                lst = await session.call_tool(
                    "list_files",
                    {"path": ".", "session_id": sid, "session_reuse": True},
                )
                self.assertFalse(lst.isError, _tool_result_text(lst))
                payload = json.loads(_tool_result_text(lst))
                files = payload.get("entries") or []
                names = []
                for item in files:
                    if isinstance(item, dict):
                        names.append(item.get("name") or item.get("path") or "")
                    else:
                        names.append(str(item))
                joined = " ".join(names)
                self.assertIn("mcp_marker.txt", joined, payload)
                print(f"[MCP E2E] list_files saw mcp_marker.txt in {joined!r}")

                r = await session.call_tool(
                    "run_code",
                    {
                        "language": "python",
                        "code": "print(open('mcp_marker.txt').read())",
                        "session_id": sid,
                        "session_reuse": True,
                    },
                )
                self.assertFalse(r.isError, _tool_result_text(r))
                out = json.loads(_tool_result_text(r)).get("output", "")
                self.assertIn(marker, out, out)
                print(f"[MCP E2E] read marker via run_code ok: {out!r}")

                st = await session.call_tool("stop_session", {"session_id": sid})
                self.assertFalse(st.isError, _tool_result_text(st))
                d_stop = json.loads(_tool_result_text(st))
                self.assertEqual(d_stop.get("status"), "stopped")

        _run_async(body())

    def test_mcp_upload_download_roundtrip(self):
        """upload_file (local temp) -> download_file; paths are on the MCP server host (E2E subprocess)."""

        async def body():
            payload_bytes = f"upload-dl-{int(time.time())}\n".encode("utf-8")
            remote = f"mcp_roundtrip_{int(time.time())}.txt"
            sid: str | None = None
            src_path: str | None = None
            out_path: str | None = None
            async with _mcp_client_session(self.host, self.port) as (session, _):
                try:
                    with tempfile.NamedTemporaryFile(delete=False) as src:
                        src.write(payload_bytes)
                        src.flush()
                        src_path = src.name
                    assert src_path is not None
                    out_path = f"{src_path}.downloaded"

                    u = await session.call_tool(
                        "upload_file",
                        {
                            "local_path": src_path,
                            "remote_path": remote,
                            "session_reuse": True,
                        },
                    )
                    self.assertFalse(u.isError, _tool_result_text(u))
                    sid = json.loads(_tool_result_text(u))["session_id"]

                    d = await session.call_tool(
                        "download_file",
                        {
                            "remote_path": remote,
                            "local_path": out_path,
                            "session_id": sid,
                            "session_reuse": True,
                        },
                    )
                    self.assertFalse(d.isError, _tool_result_text(d))

                    with open(out_path, "rb") as f:
                        self.assertEqual(f.read(), payload_bytes)
                    print(f"[MCP E2E] upload_file -> download_file roundtrip ok remote={remote!r}")
                finally:
                    if src_path:
                        try:
                            os.unlink(src_path)
                        except OSError:
                            pass
                    if out_path:
                        try:
                            os.unlink(out_path)
                        except OSError:
                            pass
                    if sid:
                        st = await session.call_tool("stop_session", {"session_id": sid})
                        self.assertFalse(st.isError, _tool_result_text(st))

        _run_async(body())


if __name__ == "__main__":
    unittest.main(verbosity=2)
