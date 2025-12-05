import os
from typing import Optional, Dict, Any
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
        image: str = "simple-codeinterpreter",
        namespace: str = "default",
        ttl: int = 3600,
        workload_manager_url: Optional[str] = None,
        router_url: Optional[str] = None,
        auth_token: Optional[str] = None,
        verbose: bool = False
    ):
        """
        Initialize the Code Interpreter Client and start a new session.
        
        Args:
            image: Name of the CodeInterpreter template (CRD).
            namespace: Kubernetes namespace.
            ttl: Time to live (seconds).
            workload_manager_url: URL of WorkloadManager (Control Plane).
            router_url: URL of Router (Data Plane).
            auth_token: Auth token for Kubernetes/WorkloadManager.
        """
        self.image = image
        self.namespace = namespace
        self.ttl = ttl
        self.logger = get_logger(__name__)
        
        # Clients
        self.cp_client = ControlPlaneClient(workload_manager_url, auth_token)
        # Default Router URL localhost:8080 if not provided via args or env
        self.router_url = router_url or os.getenv("ROUTER_URL", "http://localhost:8080")
        
        self.dp_client: Optional[DataPlaneClient] = None
        self.session_id: Optional[str] = None
        self.private_key: Optional[rsa.RSAPrivateKey] = None
        self.public_key_pem: Optional[str] = None

        # Start the session immediately
        self._start_session_internal()

    def _start_session_internal(self):
        """Internal method to generate keys and create session."""
        self.logger.info("Generating keys...")
        self._generate_keys()
        
        self.logger.info(f"Creating Code Interpreter session '{self.image}' in '{self.namespace}'...")
        self.session_id = self.cp_client.create_session(
            name=self.image,
            namespace=self.namespace,
            public_key=self.public_key_pem
        )
        
        self.logger.info(f"Session created: {self.session_id}")
        
        # Initialize Data Plane
        self.dp_client = DataPlaneClient(
            router_url=self.router_url,
            namespace=self.namespace,
            session_id=self.session_id,
            private_key=self.private_key
        )

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
        # Session is already started in __init__
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

    # --- Data Plane Methods ---

    def execute_command(self, command: str, timeout: Optional[float] = None) -> str:
        """Execute a shell command."""
        if not self.dp_client:
            raise RuntimeError("Data Plane client not initialized.")
        return self.dp_client.execute_command(command, timeout)

    def run_code(self, language: str, code: str, timeout: Optional[float] = None) -> str:
        """Run code (python/bash)."""
        if not self.dp_client:
            raise RuntimeError("Data Plane client not initialized.")
        return self.dp_client.run_code(language, code, timeout)

    def write_file(self, content: str, remote_path: str):
        """Write content to a file."""
        if not self.dp_client:
            raise RuntimeError("Data Plane client not initialized.")
        self.dp_client.write_file(content, remote_path)

    def upload_file(self, local_path: str, remote_path: str):
        """Upload a local file."""
        if not self.dp_client:
            raise RuntimeError("Data Plane client not initialized.")
        self.dp_client.upload_file(local_path, remote_path)

    def download_file(self, remote_path: str, local_path: str):
        """Download a file."""
        if not self.dp_client:
            raise RuntimeError("Data Plane client not initialized.")
        return self.dp_client.download_file(remote_path, local_path)