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
            auth_token: Optional[str] = None
        ):
        """Initialize a sandbox instance
        
        Args:
            ttl: Time-to-live in seconds for the sandbox
            image: Container image to use for the sandbox
            api_url: API server URL (defaults to environment variable API_URL or DEFAULT_API_URL)
        """
        self.ttl = ttl
        self.image = image
        self.api_url = api_url or get_env(constants.API_URL_ENV, constants.DEFAULT_API_URL)
        self.auth_token = auth_token or get_env(constants.API_TOKEN_ENV, read_token_from_file(constants.API_TOKEN_PATH))
        self._client = SandboxClient(api_url=self.api_url, auth_token=self.auth_token)
        self.id = self._client.create_sandbox(
            ttl=self.ttl, 
            image=self.image
        )
    
    def __exit__(self):   
        self.cleanup()

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


class CodeInterpreterClient(Sandbox):
    """Code interpreter client that provides dataplane operations for code execution and file management"""
    
    def __init__(
            self,
            ttl: int = constants.DEFAULT_TTL,
            image: str = constants.DEFAULT_IMAGE,
            api_url: Optional[str] = None
        ):
        """Initialize a code interpreter sandbox instance
        
        Args:
            ttl: Time-to-live in seconds for the sandbox
            image: Container image to use for the sandbox
            api_url: API server URL (defaults to environment variable API_URL or DEFAULT_API_URL)
        """
        # Initialize base class without creating sandbox yet
        self.ttl = ttl
        self.image = image
        self.api_url = api_url or get_env("API_URL", constants.DEFAULT_API_URL)

        self._client = SandboxClient(api_url=self.api_url)
        
        # Generate SSH key pair for secure connection
        public_key, private_key = SandboxSSHClient.generate_ssh_key_pair()
        
        # Create sandbox with SSH public key
        self.id = self._client.create_sandbox(
            ttl=self.ttl, 
            image=self.image, 
            ssh_public_key=public_key
        )

        # Establish tunnel and SSH connection for dataplane operations
        sock = self._client.establish_tunnel(self.id)
        self._executor = SandboxSSHClient(
            private_key=private_key, 
            tunnel_sock=sock
        )
    
    def execute_command(self, command: str) -> str:
        """Execute a command over SSH
        
        Args:
            command: Command to execute
            
        Returns:
            Command output
            
        Raises:
            SandboxNotReadyError: If sandbox is not running
        """
        if not self.is_running():
            raise exceptions.SandboxNotReadyError(f"Sandbox {self.id} is not running")
        return self._executor.execute_command(command)
    
    def execute_commands(self, commands: List[str]) -> Dict[str, str]:
        """Execute multiple commands over SSH
        
        Args:
            commands: List of commands to execute
            
        Returns:
            Dictionary mapping commands to their outputs
            
        Raises:
            SandboxNotReadyError: If sandbox is not running
        """
        if not self.is_running():
            raise exceptions.SandboxNotReadyError(f"Sandbox {self.id} is not running")
        return self._executor.execute_commands(commands)

    def run_code(
        self,
        language: str,
        code: str,
        timeout: float = 30
    ) -> str:
        """Run code snippet in the specified language over SSH
        
        Args:
            language: Programming language of the code snippet (e.g., "python", "bash")
            code: Code snippet to execute
            timeout: Execution timeout in seconds
            
        Returns:
            Output from code execution
            
        Raises:
            SandboxNotReadyError: If sandbox is not running
        """
        if not self.is_running():
            raise exceptions.SandboxNotReadyError(f"Sandbox {self.id} is not running")
        return self._executor.run_code(language, code)

    def write_file(
        self,
        content: str,
        remote_path: str
    ) -> None:
        """Upload file content to remote server via SFTP
        
        Args:
            content: Content to write to remote file
            remote_path: Path on remote server to upload to
            
        Raises:
            SandboxNotReadyError: If sandbox is not running
        """
        if not self.is_running():
            raise exceptions.SandboxNotReadyError(f"Sandbox {self.id} is not running")
        self._executor.write_file(content, remote_path)

    def upload_file(
        self,
        local_path: str,
        remote_path: str
    ) -> None:
        """Upload file from local path to remote server via SFTP
        
        Args:
            local_path: Path on local machine to upload from
            remote_path: Path on remote server to upload to
            
        Raises:
            SandboxNotReadyError: If sandbox is not running
        """
        if not self.is_running():
            raise exceptions.SandboxNotReadyError(f"Sandbox {self.id} is not running")
        self._executor.upload_file(local_path, remote_path)

    def download_file(
        self,
        remote_path: str,
        local_path: str
    ) -> str:
        """Download file content from remote server via SFTP
        
        Args:
            remote_path: Path on remote server to download from
            local_path: Path on local machine to download to
            
        Returns:
            Content of the downloaded file
            
        Raises:
            SandboxNotReadyError: If sandbox is not running
        """
        if not self.is_running():
            raise exceptions.SandboxNotReadyError(f"Sandbox {self.id} is not running")
        return self._executor.download_file(remote_path, local_path)

    def cleanup(self):
        """Clean up resources associated with the sandbox"""
        if hasattr(self, '_executor'):
            self._executor.cleanup()
