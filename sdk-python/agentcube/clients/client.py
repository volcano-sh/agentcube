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

        url = f"{self.api_url}/v1/sessions"
        response = requests.post(
            url,
            headers={"Content-Type": "application/json"},
            data=json.dumps(req_data)
        )
        
        if response.status_code != 200:
            raise Exception(f"Failed to create session: {response.status_code} - {response.text}")
        
        session_data = response.json()
        return session_data.get("sessionId")
    
    def get_sandbox(self, session_id: str) -> Optional[Dict[str, Any]]:
        """Get session details by ID"""
        try:
            url = f"{self.api_url}/v1/sessions/{session_id}"
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
            url = f"{self.api_url}/v1/sessions"
            response = requests.get(
                url,
                headers={"Content-Type": "application/json"}
            )
            response.raise_for_status()
            return response.json().get("sessions", [])
        except requests.exceptions.RequestException as e:
            self.logger.error(f"Session listing failed: {str(e)}")
            return []
    
    def delete_sandbox(self, session_id: str) -> bool:
        """Delete a session and clean up cached SSH key"""
        try:
            url = f"{self.api_url}/v1/sessions/{session_id}"
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

    def establish_tunnel(self, session_id: str) -> socket.socket:
        """Establish an HTTP CONNECT tunnel to the session
        
        Args:
            session_id: Session ID to connect to (uses current session if not provided)
            
        Returns:
            Established tunnel socket connection
        """
        session_id = session_id
        if not session_id:
            raise Exception("No session ID specified. Please create a session first.")
        
        # Parse API address
        if self.api_url.startswith("http://"):
            host_part = self.api_url[7:]
        else:
            host_part = self.api_url
        
        # Handle port number
        if ":" in host_part:
            host, port_str = host_part.split(":", 1)
            port = int(port_str)
        else:
            host = host_part
            port = 8080
        
        # Establish TCP connection
        sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        sock.settimeout(10.0)
        sock.connect((host, port))
        
        # Send CONNECT request
        connect_path = f"/v1/sessions/{session_id}/tunnel"
        request = (
            f"CONNECT {connect_path} HTTP/1.1\r\n"
            f"Host: {host}:{port}\r\n"
            "User-Agent: pico-client/1.0 (python)\r\n"
            "\r\n"
        )
        sock.sendall(request.encode())
        
        # Verify response
        response = sock.recv(4096).decode()
        if not response.startswith("HTTP/1.1 200"):
            sock.close()
            raise Exception(f"Tunnel establishment failed: {response}")
        
        self.tunnel_sock = sock
        return sock
    
    def cleanup(self):
        """Clean up resources associated with the client"""
        pass