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

"""LangChain integration for AgentCube Code Interpreter."""

from __future__ import annotations

import os
import tempfile
import asyncio
from typing import Optional

from agentcube.code_interpreter import CodeInterpreterClient
from agentcube.exceptions import CommandExecutionError

# Internal types for base class compliance
try:
    from deepagents.backends.protocol import (
        ExecuteResponse,
        FileDownloadResponse,
        FileUploadResponse,
    )
    from deepagents.backends.sandbox import BaseSandbox
except ModuleNotFoundError as e:
    # Catching only if deepagents itself is missing.
    # If deepagents is installed but has internal import errors, we should let them bubble up.
    if e.name and (e.name.startswith("deepagents") or "deepagents" in e.name):
        # Define fallback classes if deepagents is not installed
        # This allows the module to be imported even if the optional integration
        # dependencies are missing.
        class BaseSandbox: # type: ignore
            """Fallback BaseSandbox."""
            pass

        class ExecuteResponse: # type: ignore
            """Fallback ExecuteResponse."""
            def __init__(self, output: str, exit_code: int, truncated: bool = False):
                self.output = output
                self.exit_code = exit_code
                self.truncated = truncated

        class FileUploadResponse: # type: ignore
            """Fallback FileUploadResponse."""
            def __init__(self, path: str, error: Optional[str] = None):
                self.path = path
                self.error = error

        class FileDownloadResponse: # type: ignore
            """Fallback FileDownloadResponse."""
            def __init__(self, path: str, content: bytes, error: Optional[str] = None):
                self.path = path
                self.content = content
                self.error = error
    else:
        raise
except ImportError as e:
    # Re-raise with more context if it's an import error within deepagents
    raise ImportError(f"Failed to import deepagents: {e}") from e


class AgentCubeSandbox(BaseSandbox):
    """AgentCube implementation of the LangChain Sandbox integration.

    This class allows AgentCube to be used as a backend for autonomous agents
    and code execution tools within the LangChain / DeepAgents ecosystem.
    """

    def __init__(self, client: CodeInterpreterClient) -> None:
        """Initialize the sandbox with an AgentCube CodeInterpreterClient.

        Args:
            client: An instance of AgentCube's CodeInterpreterClient.
        """
        self._client = client

    @property
    def id(self) -> str:
        """Return the unique session ID of the sandbox instance."""
        return self._client.session_id or "unknown"

    def execute(
        self,
        command: str,
        *,
        timeout: int | None = None,
    ) -> ExecuteResponse:
        """Execute a shell command in the AgentCube sandbox.

        Args:
            command: The command to execute.
            timeout: Optional execution timeout in seconds.

        Returns:
            An ExecuteResponse containing stdout, exit_code and truncated status.
        """
        try:
            # Map AgentCube output to ExecuteResponse
            # execute_command now returns combined stdout and stderr
            output = self._client.execute_command(command, timeout=timeout)
            return ExecuteResponse(
                output=output,
                exit_code=0,
                truncated=False,
            )
        except CommandExecutionError as e:
            # Map AgentCube execution error
            # Combine stdout and stderr for the agent
            output = e.stdout
            if e.stderr:
                output = f"{output}\n{e.stderr}".strip() if output else e.stderr

            return ExecuteResponse(
                output=output,
                exit_code=e.exit_code,
                truncated=False,
            )

    def upload_files(
        self,
        files: list[tuple[str, bytes]],
    ) -> list[FileUploadResponse]:
        """Upload multiple files to the AgentCube sandbox.

        Args:
            files: A list of (remote_path, content_bytes) tuples.

        Returns:
            A list of FileUploadResponse objects in the same order as input.
        """
        results = []
        for path, content in files:
            try:
                # If bytes, try to decode to string as write_file currently only supports str
                # SDK support for raw bytes will be added in a separate PR.
                if isinstance(content, bytes):
                    content = content.decode("utf-8")
                self._client.write_file(content, path)
                results.append(FileUploadResponse(path=path, error=None))
            except Exception as e:
                results.append(FileUploadResponse(path=path, error=str(e)))
        return results

    def download_files(self, paths: list[str]) -> list[FileDownloadResponse]:
        """Download multiple files from the AgentCube sandbox.

        Args:
            paths: A list of remote file paths to download.

        Returns:
            A list of FileDownloadResponse objects containing file contents.
        """
        results = []
        for path in paths:
            try:
                # AgentCube download_file writes to a local path
                # We use a temp file to read it into memory for the response
                with tempfile.NamedTemporaryFile(delete=False) as tmp:
                    tmp_path = tmp.name

                try:
                    self._client.download_file(path, tmp_path)
                    with open(tmp_path, "rb") as f:
                        content = f.read()
                    results.append(FileDownloadResponse(path=path, content=content, error=None))
                finally:
                    if os.path.exists(tmp_path):
                        os.remove(tmp_path)
            except Exception as e:
                results.append(FileDownloadResponse(path=path, content=b"", error=str(e)))
        return results

    # --- Async Support ---

    async def aexecute(
        self,
        command: str,
        *,
        timeout: int | None = None,
    ) -> ExecuteResponse:
        """Async version of execute. Offloaded to thread pool."""
        return await asyncio.to_thread(self.execute, command, timeout=timeout)

    async def aupload_files(
        self,
        files: list[tuple[str, bytes]],
    ) -> list[FileUploadResponse]:
        """Async version of upload_files. Offloaded to thread pool."""
        return await asyncio.to_thread(self.upload_files, files)

    async def adownload_files(self, paths: list[str]) -> list[FileDownloadResponse]:
        """Async version of download_files. Offloaded to thread pool."""
        return await asyncio.to_thread(self.download_files, paths)
