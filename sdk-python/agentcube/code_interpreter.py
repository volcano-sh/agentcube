from typing import Dict, List
from agentcube.clients import SandboxClient, SandboxSSHClient, PicoDClient
from agentcube.sandbox import Sandbox

import agentcube.clients.constants as constants
import agentcube.utils.exceptions as exceptions


class CodeInterpreterClient(Sandbox):
    """Code interpreter client that provides dataplane operations for code execution and file management"""
    
    def __init__(
            self,
            ttl: int = constants.DEFAULT_TTL,
            image: str = constants.DEFAULT_IMAGE,
            api_url: str = None,
            use_ssh: bool = False,
            namespace: str = "default"
        ):
        """Initialize a code interpreter sandbox instance
        
        Args:
            ttl: Time-to-live in seconds for the sandbox
            image: Container image to use for the sandbox
            api_url: API server URL (defaults to environment variable API_URL or DEFAULT_API_URL)
            use_ssh: Whether to use SSH for connection (default: False, uses PicoD)
            namespace: Kubernetes namespace (default: "default")
        """
        self._executor = None
        
        if use_ssh:
            # Generate SSH key pair for secure connection
            public_key, private_key = SandboxSSHClient.generate_ssh_key_pair()
            
            # Initialize base class with SSH public key
            super().__init__(ttl=ttl, image=image, api_url=api_url, ssh_public_key=public_key)
            
            # Establish tunnel and SSH connection for dataplane operations
            sock = self._client.establish_tunnel(self.id)
            self._executor = SandboxSSHClient(
                private_key=private_key, 
                tunnel_sock=sock
            )
        else:
            # Initialize base class without SSH key
            super().__init__(ttl=ttl, image=image, api_url=api_url, ssh_public_key=None)
            
            # Initialize PicoD client
            self._executor = PicoDClient(
                api_url=self.api_url,
                namespace=namespace,
                name=self.id
            )
            self._executor.start_session()
    
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
        if self._executor is not None:
            self._executor.cleanup()
