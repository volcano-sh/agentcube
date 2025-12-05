from enum import Enum

from typing import Dict, Any, List, Optional, Tuple
from agentcube.clients import SandboxClient, SandboxSSHClient

import agentcube.clients.constants as constants
import agentcube.utils.exceptions as exceptions
from agentcube.utils.utils import get_env, read_token_from_file

class SandboxStatus(Enum):
    """Immutable state enum with transition validation"""
    RUNNING = "running"
    PAUSED = "paused"

class Sandbox:
    """Base class for sandbox lifecycle management (control plane operations)"""
    
    def __init__(
            self,
            ttl: int = constants.DEFAULT_TTL,
            image: str = constants.DEFAULT_IMAGE,
            api_url: Optional[str] = None,
            auth_token: Optional[str] = None,
            ssh_public_key: Optional[str] = None,
            skip_creation: bool = False, # New flag
        ):
        """Initialize a sandbox instance
        
        Args:
            ttl: Time-to-live in seconds for the sandbox
            image: Container image to use for the sandbox
            api_url: API server URL (defaults to environment variable API_URL or DEFAULT_API_URL)
            ssh_public_key: Optional SSH public key for secure connection
            skip_creation: If True, do not create sandbox immediately.
        """
        self.ttl = ttl
        self.image = image
        self.api_url = api_url or get_env(constants.API_URL_ENV, constants.DEFAULT_API_URL)
        self.auth_token = auth_token or get_env(constants.API_TOKEN_ENV, read_token_from_file(constants.API_TOKEN_PATH))
        self._client = SandboxClient(api_url=self.api_url, auth_token=self.auth_token)
        
        self.id = None # Initialize id as None
        if not skip_creation: # Create only if not skipped
            self.id = self._client.create_sandbox(
                ttl=self.ttl,
                template_name=self.image,
                ssh_public_key=ssh_public_key
            )
    
    def _create_initial_sandbox(self, ssh_public_key: Optional[str] = None):
        """Internal method to create the sandbox after initialization"""
        if self.id:
            raise exceptions.SandboxError("Sandbox already created.")
        self.id = self._client.create_sandbox(
            ttl=self.ttl,
            template_name=self.image,
            ssh_public_key=ssh_public_key
        )

    
    def __enter__(self):
        """Context manager entry
        
        Returns:
            self: The sandbox instance
        """
        return self
    
    def __exit__(self, exc_type, exc_val, exc_tb):
        """Context manager exit - ensures cleanup is called
        
        Args:
            exc_type: Exception type if an exception occurred, None otherwise
            exc_val: Exception value if an exception occurred, None otherwise
            exc_tb: Exception traceback if an exception occurred, None otherwise
            
        Returns:
            None to propagate any exception that occurred
        """
        self.stop()
        return None

    def is_running(self) -> bool:
        """Check if the sandbox is in running state
        
        Returns:
            True if sandbox is running, False otherwise
            
        Raises:
            SandboxNotFoundError: If sandbox does not exist
        """
        sandbox_info = self._client.get_sandbox(self.id)
        if sandbox_info:
            return sandbox_info["status"].lower() == SandboxStatus.RUNNING.value
        else:
            raise exceptions.SandboxNotFoundError(f"Sandbox {self.id} not found")
    
    def get_info(self) -> Dict[str, Any]:
        """Retrieve the latest sandbox information from the server
        
        Returns:
            Dictionary containing sandbox information
            
        Raises:
            SandboxNotFoundError: If sandbox does not exist
        """
        sandbox_info = self._client.get_sandbox(self.id)
        if sandbox_info:
            return sandbox_info
        else:
            raise exceptions.SandboxNotFoundError(f"Sandbox {self.id} not found")
    
    def list_sandboxes(self) -> List[Dict[str, Any]]:
        """List all sandboxes from the server
        
        Returns:
            List of dictionaries containing sandbox information
        """
        return self._client.list_sandboxes()
    
    def stop(self) -> bool:
        """Stop and delete the sandbox
        
        Returns:
            True if sandbox was successfully deleted, False otherwise
        """
        self.cleanup()
        return self._client.delete_sandbox(self.id)
    
    def cleanup(self):
        """Clean up resources associated with the sandbox
        
        Subclasses should override this method to perform additional cleanup
        """
        pass
