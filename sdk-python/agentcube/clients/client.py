import os
import json
import socket

import requests
from typing import Tuple, Dict, Any, Optional, List
from urllib.parse import urlparse

from agentcube.utils.log import get_logger
import agentcube.clients.constants as constants
from agentcube.utils.utils import get_env, read_token_from_file

class SandboxClient:
    """Pico API Server client class that encapsulates sandbox management, SSH connections, 
    and file transfer functionalities"""
    
    def __init__(
        self, 
        api_url: Optional[str] = None, 
        auth_token: Optional[str] = None
    ):
        """Initialize the client
        
        Args:
            api_url: Pico API server address. Defaults to environment variable API_URL 
                     or DEFAULT_API_URL if not provided
        """
        self.api_url = api_url or get_env(constants.API_URL_ENV, constants.DEFAULT_API_URL)
        self.auth_token = auth_token or get_env("API_TOKEN", read_token_from_file(constants.API_TOKEN_PATH))
        self.headers = {
            "Content-Type": "application/json",
            "Authorization": f"Bearer {self.auth_token}"
        }
        self.logger = get_logger(f"{__name__}.SandboxClient")
        
    def create_sandbox(
        self,
        ttl: int = constants.DEFAULT_TTL,
        template_name: str = constants.DEFAULT_IMAGE,
        ssh_public_key: Optional[str] = None,
        metadata: Optional[Dict[str, Any]] = None,
        kind: str = "CodeInterpreter",
        namespace: str = "default"
    ) -> str:
        """Create a new sandbox on the Pico server

        Args:
            ttl: sandbox timeout in seconds
            template_name: Name of the CodeInterpreter CRD to use as template
            ssh_public_key: SSH public key for authentication (or Session Public Key for PicoD)
            metadata: Optional sandbox metadata
            kind: Sandbox kind (AgentRuntime or CodeInterpreter)
            namespace: Kubernetes namespace

        Returns:
            Created sandbox ID and sandbox details
        """
        req_data = {
            "kind": kind,
            "name": template_name,  # CodeInterpreter CRD template name
            "namespace": namespace,
            "metadata": metadata or {}
        }
        if ssh_public_key :
            req_data["publicKey"] = ssh_public_key # Map to publicKey field expected by backend
            
        url = f"{self.api_url}/v1/sandboxes"
        response = requests.post(
            url,
            headers=self.headers,
            data=json.dumps(req_data)
        )

        if response.status_code != 200:
            raise Exception(f"Failed to create sandbox: {response.status_code} - {response.text}")
        
        sandbox_data = response.json()
        return sandbox_data.get("sessionId") # Backend returns sessionId (which is sandboxId)
    
    def get_sandbox(self, sandbox_id: str) -> Optional[Dict[str, Any]]:
        """Get sandbox details by ID"""
        try:
            url = f"{self.api_url}/v1/sandboxes/{sandbox_id}"
            response = requests.get(
                url,
                headers=self.headers
            )
            if response.status_code == 404:
                return None
            response.raise_for_status()
            return response.json()
        except requests.exceptions.RequestException as e:
            self.logger.error(f"sandbox query failed: {str(e)}")
    
    def list_sandboxes(self) -> List[Dict[str, Any]]:
        """List all sandboxs"""
        try:
            url = f"{self.api_url}/v1/sandboxes"
            response = requests.get(
                url,
                headers=self.headers
            )
            response.raise_for_status()
            return response.json().get("sandboxes", [])
        except requests.exceptions.RequestException as e:
            self.logger.error(f"sandbox listing failed: {str(e)}")
            return []
    
    def delete_sandbox(self, sandbox_id: str) -> bool:
        """Delete a sandbox and clean up cached SSH key"""
        try:
            url = f"{self.api_url}/v1/sandboxes/{sandbox_id}"
            response = requests.delete(
                url,
                timeout=30,
                headers=self.headers
            )
            if response.status_code == 404:
                return False
            response.raise_for_status()
            
            return True
        except requests.exceptions.RequestException as e:
            self.logger.error(f"sandbox deletion failed: {str(e)}")

    def establish_tunnel(self, sandbox_id: str) -> socket.socket:
        """Establish an HTTP CONNECT tunnel to the sandbox.

        Args:
            sandbox_id: The ID of the sandbox to connect to.
            auth_token: Optional authentication token for the CONNECT request.

        Returns:
            The socket for the established tunnel.

        Raises:
            RuntimeError: If the tunnel establishment fails at any step.
        """
        if not sandbox_id:
            raise ValueError("sandbox_id cannot be empty")

        parsed_url = urlparse(self.api_url)
        hostname = parsed_url.hostname
        port = parsed_url.port or 8080

        # Connect with 10s timeout
        sock = socket.socket()
        sock.settimeout(10.0)
        try:
            sock.connect((hostname, port))
        except Exception as e:
            sock.close()
            raise RuntimeError(f"failed to connect: {e}")

        # Build and send CONNECT request
        req = f"CONNECT /v1/sandboxes/{sandbox_id} HTTP/1.1\r\n"
        req += f"Host: {hostname}:{port}\r\n"
        req += "User-Agent: agentcube-sdk-python/1.0\r\n"
        if self.auth_token:
            req += f"Authorization: Bearer {self.auth_token}\r\n"
        req += "\r\n"
        
        try:
            sock.sendall(req.encode())
        except Exception as e:
            sock.close()
            raise RuntimeError(f"failed to send CONNECT: {e}")

        # Read response until headers end
        response = b""
        try:
            while b"\r\n\r\n" not in response:
                chunk = sock.recv(4096)
                if not chunk:
                    break
                response += chunk
            
            # Parse status code from first line
            first_line = response.split(b"\r\n", 1)[0]
            status_code = int(first_line.split()[1])
            
            if status_code != 200:
                sock.close()
                raise RuntimeError(f"CONNECT failed with status {status_code}")
                
        except Exception as e:
            sock.close()
            raise RuntimeError(f"failed to read response: {e}")

        # Return connected socket (tunnel established)
        return sock
    
    def cleanup(self):
        """Clean up resources associated with the client"""
        pass