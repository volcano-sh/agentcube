import os
import base64
import logging
from typing import Optional
from cryptography.hazmat.primitives import serialization
from cryptography.hazmat.primitives.asymmetric import rsa
from cryptography.hazmat.backends import default_backend

from agentcube.clients.control_plane import ControlPlaneClient
from agentcube.clients.data_plane import DataPlaneClient
from agentcube.utils.log import get_logger

class CodeInterpreterClient:
    """
    AgentCube Code Interpreter Client.
    
    Manages the lifecycle of a Code Interpreter session and provides methods
    to execute code and manage files within it.
    """
    
    def __init__(
        self,
        name: str = "simple-codeinterpreter",
        namespace: str = "default",
        ttl: int = 3600,
        workload_manager_url: Optional[str] = None,
        router_url: Optional[str] = None,
        auth_token: Optional[str] = None,
        verbose: bool = False
    ):
        """
        Initialize the Code Interpreter Client.
        
        Args:
            name: Name of the CodeInterpreter template (CRD name).
            namespace: Kubernetes namespace.
            ttl: Time to live (seconds).
            workload_manager_url: URL of WorkloadManager (Control Plane).
            router_url: URL of Router (Data Plane).
            auth_token: Auth token for Kubernetes/WorkloadManager.
            verbose: Enable debug logging.
        """
        self.name = name
        self.namespace = namespace
        self.ttl = ttl
        self.verbose = verbose
        
        # Configure Logger
        level = logging.DEBUG if verbose else logging.INFO
        self.logger = get_logger(__name__, level=level)
        
        # Clients
        self.cp_client = ControlPlaneClient(workload_manager_url, auth_token)
        if verbose:
            self.cp_client.logger.setLevel(logging.DEBUG)

        router_url = router_url or os.getenv("ROUTER_URL")
        if not router_url:
            raise ValueError("Router URL for Data Plane communication must be provided via 'router_url' argument or 'ROUTER_URL' environment variable.")
        self.router_url = router_url
        
        self.dp_client: Optional[DataPlaneClient] = None
        self.session_id: Optional[str] = None
        self.private_key: Optional[rsa.RSAPrivateKey] = None
        self.public_key_pem: Optional[str] = None

    def start(self):
        """Start the session (if not already started)."""
        if self.session_id:
            return

        self.logger.info("Generating keys...")
        self._generate_keys()

        # Picod expects the public key to be passed as a base64-encoded PEM string, with padding characters removed.
        public_key_b64 = base64.b64encode(self.public_key_pem.encode('utf-8')).decode('utf-8').rstrip('=')

        self.session_id = self.cp_client.create_session(
            name=self.name,
            namespace=self.namespace,
            public_key=public_key_b64,
            ttl=self.ttl
        )
        
        self.logger.info(f"Session created: {self.session_id}")
        
        # Initialize Data Plane
        self.dp_client = DataPlaneClient(
            cr_name=self.name,
            router_url=self.router_url,
            namespace=self.namespace,
            session_id=self.session_id,
            private_key=self.private_key
        )
        if self.verbose:
            self.dp_client.logger.setLevel(logging.DEBUG)

    def _ensure_started(self):
        """Lazy initialization of the session."""
        if not self.session_id:
            self.start()

    def _generate_keys(self):
        """Generate RSA 2048 key pair."""
        self.private_key = rsa.generate_private_key(
            public_exponent=65537,
            key_size=2048,
            backend=default_backend()
        )
        
        pub = self.private_key.public_key()
        self.public_key_pem = pub.public_bytes(
            encoding=serialization.Encoding.PEM,
            format=serialization.PublicFormat.SubjectPublicKeyInfo
        ).decode('utf-8')

    def __enter__(self):
        self.start()
        return self

    def __exit__(self, exc_type, exc_val, exc_tb):
        self.stop()

    def stop(self):
        """Stop the session."""
        if self.dp_client:
            self.dp_client.close()
            
        if self.session_id:
            self.logger.info(f"Deleting session {self.session_id}...")
            self.cp_client.delete_session(self.session_id)
            self.session_id = None
            self.dp_client = None

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
        self._ensure_started()
        if not self.dp_client:
             raise RuntimeError("Data Plane client not initialized.")
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
        self._ensure_started()
        if not self.dp_client:
             raise RuntimeError("Data Plane client not initialized.")
        return self.dp_client.run_code(language, code, timeout)

    def write_file(self, content: str, remote_path: str):
        """
        Write content to a file in the remote environment.

        Args:
            content: The string content to write to the file.
            remote_path: The destination path of the file in the remote environment.
                         This path is relative to the session's working directory.
        Raises:
            RuntimeError: If the data plane client is not initialized.
        """
        self._ensure_started()
        if not self.dp_client:
             raise RuntimeError("Data Plane client not initialized.")
        self.dp_client.write_file(content, remote_path)

    def upload_file(self, local_path: str, remote_path: str):
        """
        Upload a local file to the remote environment.

        Args:
            local_path: The path to the file on the local filesystem.
            remote_path: The destination path of the file in the remote environment.
                         This path is relative to the session's working directory.
        Raises:
            RuntimeError: If the data plane client is not initialized.
        """
        self._ensure_started()
        if not self.dp_client:
             raise RuntimeError("Data Plane client not initialized.")
        self.dp_client.upload_file(local_path, remote_path)

    def download_file(self, remote_path: str, local_path: str):
        """
        Download a file from the remote environment to the local filesystem.

        Args:
            remote_path: The path to the file in the remote environment.
                         This path is relative to the session's working directory.
            local_path: The destination path on the local filesystem to save the file.
        Returns:
            The content of the downloaded file as a string.
        Raises:
            RuntimeError: If the data plane client is not initialized.
        """
        self._ensure_started()
        if not self.dp_client:
             raise RuntimeError("Data Plane client not initialized.")
        return self.dp_client.download_file(remote_path, local_path)

    def list_files(self, path: str = "."):
        """
        List files and directories in a specified path in the remote environment.

        Args:
            path: The directory path to list. Defaults to ".". This path is relative
                  to the session's working directory.
        Returns:
            A list of strings, where each string is a file or directory name.
        Raises:
            RuntimeError: If the data plane client is not initialized.
        """
        self._ensure_started()
        if not self.dp_client:
             raise RuntimeError("Data Plane client not initialized.")
        return self.dp_client.list_files(path)
