"""
PicoD REST API Client

This is a lightweight REST API client for interacting with the PicoD daemon.
PicoD provides basic sandbox capabilities through simple HTTP endpoints.

This client provides the same interface as SSHClient for easy migration.
"""

import os
import shlex
import base64
from typing import Dict, List, Optional

try:
    import requests
except ImportError:
    raise ImportError(
        "requests library is required for PicoDClient. "
        "Install it with: pip install requests"
    )


class PicoDClient:
    """Client for interacting with PicoD daemon via REST API

    This client provides the same interface as SandboxSSHClient,
    making it an alternative to SSH-based communication.

    Example:
        >>> client = PicoDClient(host="localhost", port=9527, access_token="secret")
        >>> result = client.execute_command("echo 'Hello World'")
        >>> print(result)
        Hello World
        
        >>> client.write_file(content="test data", remote_path="/tmp/test.txt")
        >>> client.download_file(remote_path="/tmp/test.txt", local_path="./test.txt")
    """
    
    def __init__(
        self,
        host: str,
        port: int = 9527,
        timeout: int = 30,
    ):
        """Initialize PicoD client connection parameters

        Args:
            host: PicoD server hostname or IP address
            port: PicoD server port (default: 9527)
            timeout: Default request timeout in seconds
        """
        self.base_url = f"http://{host}:{port}"
        self.timeout = timeout
        self.session = requests.Session()
    
    def execute_command(
        self,
        command: str,
        timeout: Optional[float] = None,
    ) -> str:
        """Execute command in sandbox and return stdout

        Compatible with SandboxSSHClient.execute_command()

        Args:
            command: Command to execute
            timeout: Command execution timeout in seconds

        Returns:
            Command stdout output

        Raises:
            Exception: If command execution fails (non-zero exit code)
        """
        payload = {
            "command": command,
            "timeout": timeout or self.timeout,
        }
        
        response = self.session.post(
            f"{self.base_url}/api/execute",
            json=payload,
            timeout=timeout or self.timeout,
        )
        response.raise_for_status()
        
        result = response.json()
        
        if result["exit_code"] != 0:
            raise Exception(
                f"Command execution failed (exit code {result['exit_code']}): {result['stderr']}"
            )
        
        return result["stdout"]
    
    def execute_commands(self, commands: List[str]) -> Dict[str, str]:
        """Execute multiple commands in sandbox
        
        Compatible with SandboxSSHClient.execute_commands()
        
        Args:
            commands: List of commands to execute
            
        Returns:
            Dictionary mapping commands to outputs
        """
        results = {}
        for cmd in commands:
            results[cmd] = self.execute_command(cmd)
        return results
    
    def run_code(
        self,
        language: str,
        code: str,
        timeout: Optional[float] = None
    ) -> str:
        """Run code snippet in specified language
        
        Compatible with SandboxSSHClient.run_code()
        
        Args:
            language: Programming language (e.g., "python", "bash")
            code: Code snippet to execute
            timeout: Execution timeout in seconds
            
        Returns:
            Code execution output
            
        Raises:
            ValueError: If language is not supported
        """
        lang = language.lower()
        lang_aliases = {
            "python": ["python", "py", "python3"],
            "bash": ["bash", "sh", "shell"]
        }

        target_lang = None
        for std_lang, aliases in lang_aliases.items():
            if lang in aliases:
                target_lang = std_lang
                break
        
        if not target_lang:
            raise ValueError(
                f"Unsupported language: {language}. Supported: {list(lang_aliases.keys())}"
            )

        quoted_code = shlex.quote(code)
        if target_lang == "python":
            command = f"python3 -c {quoted_code}"
        elif target_lang == "bash":
            command = f"bash -c {quoted_code}"

        return self.execute_command(command, timeout)
    
    def write_file(
        self,
        content: str,
        remote_path: str,
    ) -> None:
        """Write content to file in sandbox (JSON/base64)
        
        Compatible with SandboxSSHClient.write_file()
        
        Args:
            content: Content to write to remote file
            remote_path: Write path on remote server
        """
        # Encode to base64
        if isinstance(content, str):
            content_bytes = content.encode('utf-8')
        else:
            content_bytes = content
        
        content_b64 = base64.b64encode(content_bytes).decode('utf-8')
        
        payload = {
            "path": remote_path,
            "content": content_b64,
            "mode": "0644"
        }
        
        response = self.session.post(
            f"{self.base_url}/api/files",
            json=payload,
            timeout=self.timeout,
        )
        response.raise_for_status()
    
    def upload_file(
        self,
        local_path: str,
        remote_path: str,
    ) -> None:
        """Upload local file to sandbox (multipart/form-data)
        
        Compatible with SandboxSSHClient.upload_file()
        
        Args:
            local_path: Local file path to upload
            remote_path: Upload path on remote server
            
        Raises:
            FileNotFoundError: If local file does not exist
        """
        if not os.path.exists(local_path):
            raise FileNotFoundError(f"Local file not found: {local_path}")
        
        with open(local_path, 'rb') as f:
            files = {'file': f}
            data = {'path': remote_path, 'mode': '0644'}
            
            response = requests.post(
                f"{self.base_url}/api/files",
                files=files,
                data=data,
                timeout=self.timeout,
            )
            response.raise_for_status()
    
    def download_file(
        self,
        remote_path: str,
        local_path: str
    ) -> None:
        """Download file from sandbox
        
        Compatible with SandboxSSHClient.download_file()
        
        Args:
            remote_path: Download path on remote server
            local_path: Local path to save downloaded file
        """
        # Ensure local directory exists
        local_dir = os.path.dirname(local_path)
        if local_dir:
            os.makedirs(local_dir, exist_ok=True)
        
        # Handle path: remove leading /
        clean_remote_path = remote_path.lstrip('/')
        
        response = self.session.get(
            f"{self.base_url}/api/files/{clean_remote_path}",
            stream=True,
            timeout=self.timeout,
        )
        response.raise_for_status()
        
        with open(local_path, 'wb') as f:
            for chunk in response.iter_content(chunk_size=8192):
                if chunk:
                    f.write(chunk)
    
    def cleanup(self) -> None:
        """Clean up resources (close HTTP session)
        
        Compatible with SandboxSSHClient.cleanup()
        """
        if self.session:
            self.session.close()
    
    def __enter__(self):
        """Context manager entry"""
        return self
    
    def __exit__(self, exc_type, exc_val, exc_tb):
        """Context manager exit"""
        self.cleanup()
    
    @staticmethod
    def generate_ssh_key_pair():
        """Generate SSH key pair (not applicable for PicoD)

        This method is kept for API compatibility with SandboxSSHClient,
        but throws NotImplementedError as PicoD does not use SSH authentication.
        """
        raise NotImplementedError(
            "PicoD does not use SSH authentication. "
            "This method is not applicable for PicoD client."
        )

