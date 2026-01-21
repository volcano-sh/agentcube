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

import os
import logging
from typing import Optional

from agentcube.clients.control_plane import ControlPlaneClient
from agentcube.clients.data_plane import DataPlaneClient
from agentcube.utils.log import get_logger


class CodeInterpreterClient:
    """
    AgentCube Code Interpreter Client.

    Manages the lifecycle of a Code Interpreter session and provides methods
    to execute code and manage files within it.

    Session is created upon initialization (or reused if session_id is provided).
    Call stop() to delete the session, or use context manager for automatic cleanup.

    Example:
        # Basic usage with context manager (recommended)
        with CodeInterpreterClient() as client:
            client.run_code("python", "print('hello')")
        # Session automatically deleted on exit

        # Session reuse for multi-step workflows
        client1 = CodeInterpreterClient()
        session_id = client1.session_id
        client1.run_code("python", "x = 42")
        # Don't call stop() - let session persist

        client2 = CodeInterpreterClient(session_id=session_id)
        client2.run_code("python", "print(x)")  # x still exists
        client2.stop()  # Cleanup when done
    """

    def __init__(
        self,
        name: str = "simple-codeinterpreter",
        namespace: str = "default",
        ttl: int = 3600,
        workload_manager_url: Optional[str] = None,
        router_url: Optional[str] = None,
        auth_token: Optional[str] = None,
        verbose: bool = False,
        session_id: Optional[str] = None,
    ):
        """
        Initialize the Code Interpreter Client.

        Creates a new session, or reuses an existing session
        if session_id is provided.

        Args:
            name: Name of the CodeInterpreter template (CRD name).
            namespace: Kubernetes namespace.
            ttl: Time to live (seconds) for new sessions.
            workload_manager_url: URL of WorkloadManager (Control Plane).
            router_url: URL of Router (Data Plane).
            auth_token: Auth token for Kubernetes/WorkloadManager.
            verbose: Enable debug logging.
            session_id: Optional. Reuse an existing session instead of creating new one.
        """
        self.name = name
        self.namespace = namespace
        self.ttl = ttl
        self.verbose = verbose

        # Configure Logger
        level = logging.DEBUG if verbose else logging.INFO
        self.logger = get_logger(__name__, level=level)

        # Initialize Control Plane client
        self.cp_client = ControlPlaneClient(workload_manager_url, auth_token)
        if verbose:
            self.cp_client.logger.setLevel(logging.DEBUG)

        # Validate Router URL
        router_url = router_url or os.getenv("ROUTER_URL")
        if not router_url:
            raise ValueError(
                "Router URL for Data Plane communication must be provided via "
                "'router_url' argument or 'ROUTER_URL' environment variable."
            )
        self.router_url = router_url

        # Session state
        self.session_id: Optional[str] = session_id
        self.dp_client: Optional[DataPlaneClient] = None

        # Initialize session
        if session_id:
            self.logger.info(f"Reusing existing session: {session_id}")
            self._init_data_plane()
        else:
            self.logger.info("Creating new session...")
            self.session_id = self.cp_client.create_session(
                name=self.name,
                namespace=self.namespace,
                ttl=self.ttl
            )
            self.logger.info(f"Session created: {self.session_id}")
            try:
                self._init_data_plane()
            except Exception:
                # Cleanup session if DataPlaneClient initialization fails
                self.logger.warning(
                    f"Failed to initialize data plane client, "
                    f"deleting session {self.session_id} to prevent resource leak"
                )
                self.cp_client.delete_session(self.session_id)
                self.session_id = None
                raise

    def _init_data_plane(self):
        """Initialize the Data Plane client."""
        self.dp_client = DataPlaneClient(
            cr_name=self.name,
            router_url=self.router_url,
            namespace=self.namespace,
            session_id=self.session_id,
        )
        if self.verbose:
            self.dp_client.logger.setLevel(logging.DEBUG)

    def __enter__(self):
        return self

    def __exit__(self, exc_type, exc_val, exc_tb):
        self.stop()

    def stop(self):
        """
        Stop and delete the session.

        This terminates the Code Interpreter instance and releases all resources.
        After calling this, the session_id can no longer be reused.
        """
        if self.dp_client:
            self.dp_client.close()
            self.dp_client = None

        if self.session_id:
            self.logger.info(f"Deleting session {self.session_id}...")
            self.cp_client.delete_session(self.session_id)
            self.session_id = None

        self.cp_client.close()

    # --- Data Plane Methods ---

    def execute_command(self, command: str, timeout: Optional[float] = None) -> str:
        """
        Execute a shell command.

        Parameters:
            command (str): The shell command to execute.
            timeout (Optional[float]): Maximum time in seconds to allow for command execution.
                If None (default), no timeout is applied.
        Returns:
            str: The output of the command.
        """
        return self.dp_client.execute_command(command, timeout)

    def run_code(self, language: str, code: str, timeout: Optional[float] = None) -> str:
        """
        Execute a code snippet in the remote environment.

        This method supports running code in various languages (e.g., Python, Bash).
        The execution context is managed by the remote Code Interpreter session.

        Args:
            language: The programming language of the code (e.g., "python", "bash").
            code: The actual code snippet to execute.
            timeout: Optional. The maximum time (in seconds) to wait for the code
                     execution to complete. If not provided, a default timeout applies.

        Returns:
            The standard output (stdout) generated by the code execution.
        """
        return self.dp_client.run_code(language, code, timeout)

    def write_file(self, content: str, remote_path: str):
        """
        Write content to a file in the remote environment.

        Args:
            content: The string content to write to the file.
            remote_path: The destination path of the file in the remote environment.
                         This path is relative to the session's working directory.
        """
        self.dp_client.write_file(content, remote_path)

    def upload_file(self, local_path: str, remote_path: str):
        """
        Upload a local file to the remote environment.

        Args:
            local_path: The path to the file on the local filesystem.
            remote_path: The destination path of the file in the remote environment.
                         This path is relative to the session's working directory.
        """
        self.dp_client.upload_file(local_path, remote_path)

    def download_file(self, remote_path: str, local_path: str):
        """
        Download a file from the remote environment to the local filesystem.

        Args:
            remote_path: The path to the file in the remote environment.
                         This path is relative to the session's working directory.
            local_path: The destination path on the local filesystem to save the file.
        """
        self.dp_client.download_file(remote_path, local_path)

    def list_files(self, path: str = "."):
        """
        List files and directories in a specified path in the remote environment.

        Args:
            path: The directory path to list. Defaults to ".". This path is relative
                  to the session's working directory.
        Returns:
            A list of file/directory information dicts.
        """
        return self.dp_client.list_files(path)
