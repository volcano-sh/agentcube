import os
import json
import socket

import requests
from typing import Tuple, Dict, Any, Optional, List

from agentcube.utils.log import get_logger
import agentcube.clients.constants as constants
from agentcube.utils.utils import get_env

class SandboxClient:
    """Pico API Server client class that encapsulates session management, SSH connections, 
    and file transfer functionalities"""
    
    def __init__(self, api_url: Optional[str] = None):
        """Initialize the client
        
        Args:
            api_url: Pico API server address. Defaults to environment variable API_URL 
                     or DEFAULT_API_URL if not provided
        """
        self.api_url = api_url or get_env("API_URL", constants.DEFAULT_API_URL)
        self.logger = get_logger(f"{__name__}.SandboxClient")
        
    def create_sandbox(
        self,
        ttl: int = constants.DEFAULT_TTL,
        image: str = constants.DEFAULT_IMAGE,
        ssh_public_key: Optional[str] = None,
        metadata: Optional[Dict[str, Any]] = None
    ) -> str:
        """Create a new session on the Pico server
        
        Args:
            ttl: Session timeout in seconds
            image: Container image to use
            ssh_public_key: SSH public key for authentication
            metadata: Optional session metadata
            
        Returns:
            Created session ID and session details
        """
        req_data = {
            "ttl": ttl,
            "image": image,
            "metadata": metadata or {}
        }
        if ssh_public_key :
            req_data["sshPublicKey"] = ssh_public_key

        url = f"{self.api_url}/v1/sandboxes"
        response = requests.post(
            url,
            headers={"Content-Type": "application/json"},
            data=json.dumps(req_data)
        )
        
        print(response)

        if response.status_code != 200:
            raise Exception(f"Failed to create session: {response.status_code} - {response.text}")
        
        session_data = response.json()
        return session_data.get("sandboxId")
    
    def get_sandbox(self, sandbox_id: str) -> Optional[Dict[str, Any]]:
        """Get session details by ID"""
        try:
            url = f"{self.api_url}/v1/sandboxes/{sandbox_id}"
            response = requests.get(
                url,
                headers={"Content-Type": "application/json"}
            )
            print(url)
            print(response)
            if response.status_code == 404:
                return None
            response.raise_for_status()
            return response.json()
        except requests.exceptions.RequestException as e:
            self.logger.error(f"Session query failed: {str(e)}")
    
    def list_sandboxes(self) -> List[Dict[str, Any]]:
        """List all sessions"""
        try:
            url = f"{self.api_url}/v1/sandboxes"
            response = requests.get(
                url,
                headers={"Content-Type": "application/json"}
            )
            response.raise_for_status()
            return response.json().get("sessions", [])
        except requests.exceptions.RequestException as e:
            self.logger.error(f"Session listing failed: {str(e)}")
            return []
    
    def delete_sandbox(self, sandbox_id: str) -> bool:
        """Delete a session and clean up cached SSH key"""
        try:
            url = f"{self.api_url}/v1/sandboxes/{sandbox_id}"
            response = requests.delete(
                url,
                timeout=30
            )
            if response.status_code == 404:
                return False
            response.raise_for_status()
            
            return True
        except requests.exceptions.RequestException as e:
            self.logger.error(f"Session deletion failed: {str(e)}")

    def establish_tunnel(self, sandbox_id: str, auth_token: str = "") -> socket.socket:
        # 1. Remove http:// prefix
        if self.api_url.startswith("http://"):
            host = self.api_url[7:]
        else:
            host = self.api_url

        # 2. Add default port 8080 if no port specified
        if ':' not in host or host.count('[') != host.count(']'):
            host += ":8080"

        # 3. Connect with 10s timeout
        sock = socket.socket()
        sock.settimeout(10.0)
        try:
            sock.connect((host.rsplit(':', 1)[0], int(host.rsplit(':', 1)[1])))
        except Exception as e:
            sock.close()
            raise RuntimeError(f"failed to connect: {e}")

        # 4. Build and send CONNECT request
        req = f"CONNECT /v1/sandboxes/{sandbox_id} HTTP/1.1\r\n"
        req += f"Host: {host}\r\n"
        req += "User-Agent: ssh-key-test/1.0\r\n"
        if auth_token:
            req += f"Authorization: Bearer {auth_token}\r\n"
        req += "\r\n"
        
        try:
            sock.sendall(req.encode())
        except Exception as e:
            sock.close()
            raise RuntimeError(f"failed to send CONNECT: {e}")

        # 5. Read response until headers end
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

        # 6. Return connected socket (tunnel established)
        return sock
    
    def cleanup(self):
        """Clean up resources associated with the client"""
        pass