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

import logging
import os
from typing import Any, List, Optional

from agentcube.clients.async_control_plane import AsyncControlPlaneClient
from agentcube.clients.async_code_interpreter_data_plane import (
    AsyncCodeInterpreterDataPlaneClient,
)
from agentcube.utils.log import get_logger


class AsyncCodeInterpreterClient:
    """
    Async AgentCube Code Interpreter Client.

    Manages the lifecycle of a Code Interpreter session and exposes async
    methods to execute code and manage files within it.

    Because session creation involves network I/O, this client **must** be
    initialised with ``await AsyncCodeInterpreterClient.create(...)`` or used
    as an async context manager::

        # Async context manager (recommended)
        async with AsyncCodeInterpreterClient.create(...) as client:
            await client.run_code("python", "print('hello')")

        # Manual lifecycle management
        client = await AsyncCodeInterpreterClient.create(...)
        try:
            await client.run_code("python", "print('hello')")
        finally:
            await client.stop()
    """

    def __init__(
        self,
        name: str = "my-interpreter",
        namespace: str = "default",
        ttl: int = 3600,
        workload_manager_url: Optional[str] = None,
        router_url: Optional[str] = None,
        auth_token: Optional[str] = None,
        verbose: bool = False,
        session_id: Optional[str] = None,
    ):
        """Store configuration; does *not* create a session.

        Call ``await AsyncCodeInterpreterClient.create(...)`` instead of
        constructing this class directly.
        """
        self.name = name
        self.namespace = namespace
        self.ttl = ttl
        self.verbose = verbose

        level = logging.DEBUG if verbose else logging.INFO
        self.logger = get_logger(__name__, level=level)

        self.cp_client = AsyncControlPlaneClient(workload_manager_url, auth_token)
        if verbose:
            self.cp_client.logger.setLevel(logging.DEBUG)

        router_url = router_url or os.getenv("ROUTER_URL")
        if not router_url:
            raise ValueError(
                "Router URL for Data Plane communication must be provided via "
                "'router_url' argument or 'ROUTER_URL' environment variable."
            )
        self.router_url = router_url

        self.session_id: Optional[str] = session_id
        self.dp_client: Optional[AsyncCodeInterpreterDataPlaneClient] = None

    # ------------------------------------------------------------------
    # Async factory / context-manager helpers
    # ------------------------------------------------------------------

    @classmethod
    async def create(
        cls,
        name: str = "my-interpreter",
        namespace: str = "default",
        ttl: int = 3600,
        workload_manager_url: Optional[str] = None,
        router_url: Optional[str] = None,
        auth_token: Optional[str] = None,
        verbose: bool = False,
        session_id: Optional[str] = None,
    ) -> "AsyncCodeInterpreterClient":
        """Create and fully initialise an AsyncCodeInterpreterClient.

        This is the preferred way to create a client when you are not using
        the async context manager.
        """
        instance = cls(
            name=name,
            namespace=namespace,
            ttl=ttl,
            workload_manager_url=workload_manager_url,
            router_url=router_url,
            auth_token=auth_token,
            verbose=verbose,
            session_id=session_id,
        )
        await instance._async_init()
        return instance

    async def _async_init(self) -> None:
        """Perform async initialisation (session creation / reuse)."""
        if self.session_id:
            self.logger.info(f"Reusing existing session: {self.session_id}")
            self._init_data_plane()
        else:
            self.logger.info("Creating new session...")
            self.session_id = await self.cp_client.create_session(
                name=self.name,
                namespace=self.namespace,
                ttl=self.ttl,
            )
            self.logger.info(f"Session created: {self.session_id}")
            try:
                self._init_data_plane()
            except Exception:
                self.logger.warning(
                    f"Failed to initialize data plane client, "
                    f"deleting session {self.session_id} to prevent resource leak"
                )
                await self.cp_client.delete_session(self.session_id)
                self.session_id = None
                raise

    def _init_data_plane(self) -> None:
        """Initialise the async Data Plane client (sync — no network I/O)."""
        self.dp_client = AsyncCodeInterpreterDataPlaneClient(
            cr_name=self.name,
            router_url=self.router_url,
            namespace=self.namespace,
            session_id=self.session_id,
        )
        if self.verbose:
            self.dp_client.logger.setLevel(logging.DEBUG)

    async def __aenter__(self) -> "AsyncCodeInterpreterClient":
        await self._async_init()
        return self

    async def __aexit__(self, exc_type, exc_val, exc_tb) -> None:
        await self.stop()

    # ------------------------------------------------------------------
    # Lifecycle
    # ------------------------------------------------------------------

    async def stop(self) -> None:
        """Stop and delete the session, releasing all resources."""
        if self.dp_client:
            await self.dp_client.close()
            self.dp_client = None

        if self.session_id:
            self.logger.info(f"Deleting session {self.session_id}...")
            await self.cp_client.delete_session(self.session_id)
            self.session_id = None

        await self.cp_client.close()

    # ------------------------------------------------------------------
    # Data Plane methods
    # ------------------------------------------------------------------

    async def execute_command(
        self, command: str, timeout: Optional[float] = None
    ) -> str:
        """Execute a shell command.

        Args:
            command: The shell command to execute.
            timeout: Maximum time in seconds to allow for command execution.

        Returns:
            The stdout output of the command.
        """
        if not self.dp_client:
            raise RuntimeError("Data Plane client is not initialized.")
        return await self.dp_client.execute_command(command, timeout)

    async def run_code(
        self, language: str, code: str, timeout: Optional[float] = None
    ) -> str:
        """Execute a code snippet in the remote environment.

        Args:
            language: The programming language (e.g. ``"python"``, ``"bash"``).
            code: The code snippet to execute.
            timeout: Optional maximum execution time in seconds.

        Returns:
            The stdout generated by the code execution.
        """
        if not self.dp_client:
            raise RuntimeError("Data Plane client is not initialized.")
        return await self.dp_client.run_code(language, code, timeout)

    async def write_file(self, content: str, remote_path: str) -> None:
        """Write content to a file in the remote environment.

        Args:
            content: The string content to write.
            remote_path: Destination path in the remote environment.
        """
        if not self.dp_client:
            raise RuntimeError("Data Plane client is not initialized.")
        await self.dp_client.write_file(content, remote_path)

    async def upload_file(self, local_path: str, remote_path: str) -> None:
        """Upload a local file to the remote environment.

        Args:
            local_path: Path to the file on the local filesystem.
            remote_path: Destination path in the remote environment.
        """
        if not self.dp_client:
            raise RuntimeError("Data Plane client is not initialized.")
        await self.dp_client.upload_file(local_path, remote_path)

    async def download_file(self, remote_path: str, local_path: str) -> None:
        """Download a file from the remote environment to the local filesystem.

        Args:
            remote_path: Path to the file in the remote environment.
            local_path: Destination path on the local filesystem.
        """
        if not self.dp_client:
            raise RuntimeError("Data Plane client is not initialized.")
        await self.dp_client.download_file(remote_path, local_path)

    async def list_files(self, path: str = ".") -> List[Any]:
        """List files and directories in a path in the remote environment.

        Args:
            path: Directory path to list (default ``"."``).

        Returns:
            A list of file/directory information dicts.
        """
        if not self.dp_client:
            raise RuntimeError("Data Plane client is not initialized.")
        return await self.dp_client.list_files(path)
