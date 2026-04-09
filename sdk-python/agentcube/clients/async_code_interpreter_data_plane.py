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

import ast
import asyncio
import base64
import json
import os
import shlex
import uuid
from typing import Any, List, Optional, Union
from urllib.parse import urljoin

import httpx

from agentcube.exceptions import CommandExecutionError
from agentcube.utils.async_http import create_async_session
from agentcube.utils.log import get_logger

# Extra seconds added to the HTTP read timeout on top of the PicoD command
# timeout.  This gives PicoD time to finish and return its JSON response
# before httpx gives up waiting.
_TIMEOUT_BUFFER_SECONDS = 2.0


def _write_bytes(path: str, data: bytes) -> None:
    """Write bytes to a file; intended to be called via asyncio.to_thread."""
    with open(path, "wb") as f:
        f.write(data)


class AsyncCodeInterpreterDataPlaneClient:
    """Async client for AgentCube Data Plane (Router -> PicoD).
    Handles command execution and file operations via the Router.
    """

    def __init__(
        self,
        session_id: str,
        router_url: Optional[str] = None,
        namespace: Optional[str] = None,
        cr_name: Optional[str] = None,
        base_url: Optional[str] = None,
        timeout: int = 120,
        connect_timeout: float = 5.0,
        connector_limit: int = 100,
        connector_limit_per_host: int = 10,
    ):
        """Initialize the async Data Plane client.

        Args:
            session_id: Session ID (for x-agentcube-session-id header).
            router_url: Base URL of the Router service (optional if base_url is provided).
            namespace: Kubernetes namespace (optional if base_url is provided).
            cr_name: Code Interpreter resource name (optional if base_url is provided).
            base_url: Direct base URL for invocations (overrides router logic).
            timeout: Default request timeout in seconds (default: 120).
            connect_timeout: Connection timeout in seconds (default: 5).
            connector_limit: Total simultaneous connections (default: 100).
            connector_limit_per_host: Max keepalive connections per host (default: 10).
        """
        self.session_id = session_id
        self.timeout = timeout
        self.connect_timeout = connect_timeout
        self.logger = get_logger(f"{__name__}.AsyncCodeInterpreterDataPlaneClient")

        if base_url:
            self.base_url = base_url
            self.cr_name = cr_name
        elif router_url and namespace and cr_name:
            self.cr_name = cr_name
            base_path = f"/v1/namespaces/{namespace}/code-interpreters/{cr_name}/invocations/"
            self.base_url = urljoin(router_url, base_path)
        else:
            raise ValueError(
                "Either 'base_url' or all of 'router_url', 'namespace', 'cr_name' must be provided."
            )

        self._http_session = create_async_session(
            connector_limit=connector_limit,
            connector_limit_per_host=connector_limit_per_host,
        )
        self._http_session.headers.update({"x-agentcube-session-id": self.session_id})

    def _make_timeout(self, read_timeout: Optional[float] = None) -> httpx.Timeout:
        """Build an httpx.Timeout with the given read timeout."""
        return httpx.Timeout(
            read_timeout if read_timeout is not None else self.timeout,
            connect=self.connect_timeout,
        )

    async def _request(
        self,
        method: str,
        endpoint: str,
        body: Optional[bytes] = None,
        timeout: Optional[httpx.Timeout] = None,
        **kwargs,
    ) -> httpx.Response:
        """Make a request to the Data Plane via Router."""
        url = urljoin(self.base_url, endpoint)
        if timeout is None:
            timeout = self._make_timeout()

        extra_headers = kwargs.pop("headers", {})
        if body:
            extra_headers.setdefault("Content-Type", "application/json")

        self.logger.debug(f"{method} {url}")

        return await self._http_session.request(
            method=method,
            url=url,
            content=body,
            headers=extra_headers,
            timeout=timeout,
            **kwargs,
        )

    async def execute_command(
        self, command: Union[str, List[str]], timeout: Optional[float] = None
    ) -> str:
        """Execute a shell command.

        Args:
            command: The command to execute, as a string or list of arguments.
            timeout: Optional timeout for the command execution.

        Returns:
            The stdout output of the command.
        """
        timeout_value = timeout if timeout is not None else self.timeout
        timeout_str = (
            f"{timeout_value}s" if isinstance(timeout_value, (int, float)) else str(timeout_value)
        )

        cmd_list = shlex.split(command, posix=True) if isinstance(command, str) else command

        payload = {"command": cmd_list, "timeout": timeout_str}
        body = json.dumps(payload).encode("utf-8")

        # Add a buffer so httpx doesn't time out before PicoD returns the JSON response
        read_timeout = (
            timeout_value + _TIMEOUT_BUFFER_SECONDS
            if isinstance(timeout_value, (int, float))
            else timeout_value
        )
        t = self._make_timeout(read_timeout)

        resp = await self._request("POST", "api/execute", body=body, timeout=t)
        resp.raise_for_status()
        result = resp.json()

        if result["exit_code"] != 0:
            raise CommandExecutionError(
                exit_code=result["exit_code"],
                stderr=result["stderr"],
                command=command,
            )

        return result["stdout"]

    async def run_code(
        self, language: str, code: str, timeout: Optional[float] = None
    ) -> str:
        """Run a code snippet (python or bash)."""
        lang = language.lower()
        if lang in ["python", "py", "python3"]:
            try:
                ast.parse(code)
            except SyntaxError:
                fixed_code = code.replace("\\n", "\n")
                try:
                    ast.parse(fixed_code)
                    self.logger.warning(
                        "Detected and fixed double-escaped newlines in Python code."
                    )
                    code = fixed_code
                except SyntaxError:
                    pass
            except Exception as e:
                self.logger.debug(f"AST parsing fallback error: {e}", exc_info=True)

            filename = f"script-{uuid.uuid4()}.py"
            await self.write_file(code, filename)
            cmd: List[str] = ["python3", filename]
        elif lang in ["bash", "sh"]:
            filename = f"script-{uuid.uuid4()}.sh"
            await self.write_file(code, filename)
            cmd = ["bash", filename]
        else:
            raise ValueError(f"Unsupported language: {language}")

        return await self.execute_command(cmd, timeout)

    async def write_file(self, content: str, remote_path: str) -> None:
        """Write text content to a file."""
        content_b64 = base64.b64encode(content.encode("utf-8")).decode("utf-8")
        payload = {"path": remote_path, "content": content_b64, "mode": "0644"}
        body = json.dumps(payload).encode("utf-8")

        resp = await self._request("POST", "api/files", body=body)
        resp.raise_for_status()

    async def upload_file(self, local_path: str, remote_path: str) -> None:
        """Upload a local file using multipart/form-data."""
        if not os.path.exists(local_path):
            raise FileNotFoundError(f"Local file not found: {local_path}")

        url = urljoin(self.base_url, "api/files")
        self.logger.debug(f"Uploading file {local_path} to {remote_path}")

        with open(local_path, "rb") as f:
            resp = await self._http_session.post(
                url,
                files={"file": (os.path.basename(local_path), f)},
                data={"path": remote_path, "mode": "0644"},
                timeout=self._make_timeout(),
            )
        resp.raise_for_status()

    async def download_file(self, remote_path: str, local_path: str) -> None:
        """Download a file."""
        clean_path = remote_path.lstrip("/")
        async with self._http_session.stream(
            "GET",
            urljoin(self.base_url, f"api/files/{clean_path}"),
            timeout=self._make_timeout(),
        ) as resp:
            resp.raise_for_status()
            content = await resp.aread()

        if os.path.dirname(local_path):
            os.makedirs(os.path.dirname(local_path), exist_ok=True)
        # Run the blocking file write in a thread so the event loop is not blocked
        await asyncio.to_thread(_write_bytes, local_path, content)

    async def list_files(self, path: str = ".") -> Any:
        """List files in a directory."""
        resp = await self._request("GET", "api/files", params={"path": path})
        resp.raise_for_status()
        return resp.json().get("files", [])

    async def close(self) -> None:
        """Close the underlying HTTP session."""
        await self._http_session.aclose()

    async def __aenter__(self) -> "AsyncCodeInterpreterDataPlaneClient":
        return self

    async def __aexit__(self, exc_type, exc_val, exc_tb) -> None:
        await self.close()
