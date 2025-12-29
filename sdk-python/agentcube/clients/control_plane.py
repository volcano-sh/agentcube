import os
import requests
from requests.adapters import HTTPAdapter
from urllib3.util.retry import Retry
from typing import Dict, Any, Optional

from agentcube.utils.log import get_logger
from agentcube.utils.utils import read_token_from_file

class ControlPlaneClient:
    """Client for AgentCube Control Plane (WorkloadManager).
    Handles creation and deletion of Code Interpreter sessions.
    """
    
    def __init__(
        self,
        workload_manager_url: Optional[str] = None,
        auth_token: Optional[str] = None,
        timeout: int = 120,
        connect_timeout: float = 10.0,
        pool_connections: int = 10,
        pool_maxsize: int = 10,
    ):
        """Initialize the Control Plane client.
        
        Args:
            workload_manager_url: URL of the WorkloadManager service.
            auth_token: Kubernetes Service Account Token for authentication.
            timeout: Default request timeout in seconds (default: 120).
            connect_timeout: Connection timeout in seconds (default: 10).
            pool_connections: Number of connection pools to cache (default: 10).
            pool_maxsize: Maximum connections per pool (default: 10).
        """
        # Prioritize argument -> env var
        self.base_url = workload_manager_url or os.getenv("WORKLOAD_MANAGER_URL")
        if not self.base_url:
            raise ValueError("Workload Manager URL must be provided via 'workload_manager_url' argument or 'WORKLOAD_MANAGER_URL' environment variable.")
        
        # Prioritize argument -> env var -> k8s service account token file
        token_path = "/var/run/secrets/kubernetes.io/serviceaccount/token"
        token = auth_token or os.getenv("API_TOKEN") or read_token_from_file(token_path)
        self.timeout = timeout
        self.connect_timeout = connect_timeout
        
        self.logger = get_logger(f"{__name__}.ControlPlaneClient")
        
        # Create session with connection pooling
        self.session = requests.Session()
        
        # Configure connection pool and retry strategy
        retry_strategy = Retry(
            total=3,
            backoff_factor=0.5,
            status_forcelist=[502, 503, 504],
        )
        adapter = HTTPAdapter(
            pool_connections=pool_connections,
            pool_maxsize=pool_maxsize,
            max_retries=retry_strategy,
        )
        self.session.mount("http://", adapter)
        self.session.mount("https://", adapter)
        
        # Set default headers
        self.session.headers.update({
            "Content-Type": "application/json",
        })
        if token:
            self.session.headers["Authorization"] = f"Bearer {token}"
        
    def create_session(
        self,
        name: str = "simple-codeinterpreter",
        namespace: str = "default",
        public_key: Optional[str] = None,
        metadata: Optional[Dict[str, Any]] = None,
        ttl: int = 3600,
    ) -> str:
        """Create a new Code Interpreter session.

        Args:
            name: Name of the CodeInterpreter template (CRD name).
            namespace: Kubernetes namespace.
            public_key: RSA Public Key for Data Plane authentication (Base64 encoded PEM).
            metadata: Optional metadata.
            ttl: Time to live (seconds).

        Returns:
            session_id (str): The ID of the created session.
        """
        payload = {
            "name": name,
            "namespace": namespace,
            "ttl": ttl,
            "metadata": metadata or {}
        }
        if public_key:
            payload["publicKey"] = public_key

        url = f"{self.base_url}/v1/code-interpreter"
        self.logger.debug(f"Creating session at {url} with payload: {payload}")
        
        try:
            response = self.session.post(
                url, 
                json=payload, 
                timeout=(self.connect_timeout, self.timeout)
            )
            response.raise_for_status()
            
            data = response.json()
            if "sessionId" not in data or not data["sessionId"]:
                self.logger.error("Response JSON missing 'sessionId' in create_session response.")
                self.logger.debug(f"Full response data: {data}")
                raise ValueError("Failed to create session: 'sessionId' missing from response")
            return data["sessionId"]
        except requests.exceptions.RequestException as e:
            self.logger.error(f"Failed to create session: {e}")
            if e.response is not None:
                self.logger.error(f"Server response: {e.response.text}")
            raise

    def delete_session(self, session_id: str) -> bool:
        """Delete a Code Interpreter session.

        Args:
            session_id: The session ID to delete.

        Returns:
            True if deleted successfully (or didn't exist), False on failure.
        """
        url = f"{self.base_url}/v1/code-interpreter/sessions/{session_id}"
        self.logger.debug(f"Deleting session {session_id} at {url}")
        
        try:
            response = self.session.delete(
                url, 
                timeout=(self.connect_timeout, self.timeout)
            )
            if response.status_code == 404:
                return True # Already gone
            response.raise_for_status()
            return True
        except requests.exceptions.RequestException as e:
            self.logger.error(f"Failed to delete session {session_id}: {e}")
            return False

    def close(self):
        """Close the underlying session and release connection pool resources."""
        self.session.close()

