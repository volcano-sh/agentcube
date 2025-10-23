"""
Python SDK for Remote Sandbox REST API

This provides a similar interface to the SSH-based sandbox,
but uses HTTP REST API instead of SSH/SFTP.
"""

import requests
import os
from typing import Optional, Dict, Any, BinaryIO
from datetime import datetime


class SandboxAPIError(Exception):
    """Base exception for Sandbox API errors"""
    pass


class SandboxConnectionError(SandboxAPIError):
    """Raised when connection to API fails"""
    pass


class SandboxOperationError(SandboxAPIError):
    """Raised when an operation fails"""
    pass


class RemoteSandboxAPI:
    """
    Manages a remote sandbox environment over REST API.
    
    Similar interface to SSH-based RemoteSandbox but uses HTTP APIs.
    """
    
    def __init__(
        self, 
        api_url: str,
        api_key: Optional[str] = None,
        bearer_token: Optional[str] = None,
        ttl: int = 3600,
        metadata: Optional[Dict[str, Any]] = None,
        timeout: int = 30
    ):
        """
        Initialize the REST API sandbox client.
        
        Args:
            api_url: Base URL of the sandbox API (e.g., "https://api.sandbox.example.com/v1")
            api_key: API key for authentication (alternative to bearer token)
            bearer_token: JWT bearer token for authentication
            ttl: Time-to-live for the session in seconds (default 3600)
            metadata: Optional metadata to attach to the session
            timeout: Default timeout for HTTP requests in seconds
        """
        self.api_url = api_url.rstrip('/')
        self.ttl = ttl
        self.metadata = metadata or {}
        self.timeout = timeout
        
        # Authentication
        self.headers = {
            'Content-Type': 'application/json'
        }
        if bearer_token:
            self.headers['Authorization'] = f'Bearer {bearer_token}'
        elif api_key:
            self.headers['X-API-Key'] = api_key
        else:
            raise ValueError("Either api_key or bearer_token must be provided")
        
        # Session state
        self.session_id: Optional[str] = None
        self.sandbox_path: Optional[str] = None
        self.is_active = False
        self._session = requests.Session()
        self._session.headers.update(self.headers)
    
    def __enter__(self):
        """
        Creates a new sandbox session via REST API.
        """
        try:
            # Create session
            response = self._session.post(
                f"{self.api_url}/sessions",
                json={
                    'ttl': self.ttl,
                    'metadata': self.metadata
                },
                timeout=self.timeout
            )
            
            if response.status_code != 201:
                error_msg = response.json().get('message', 'Failed to create session')
                raise SandboxConnectionError(f"Failed to create session: {error_msg}")
            
            session_data = response.json()
            self.session_id = session_data['sessionId']
            self.sandbox_path = session_data['sandboxPath']
            self.is_active = True
            
            return self
            
        except requests.exceptions.RequestException as e:
            raise SandboxConnectionError(f"Failed to connect to API: {e}")
    
    def __exit__(self, exc_type, exc_val, exc_tb):
        """
        Deletes the sandbox session and cleans up.
        """
        if self.is_active and self.session_id:
            try:
                self._session.delete(
                    f"{self.api_url}/sessions/{self.session_id}",
                    timeout=self.timeout
                )
            except:
                pass  # Best effort cleanup
        
        self.is_active = False
        self.session_id = None
        self.sandbox_path = None
        self._session.close()
    
    def run_command(self, command: str, timeout: int = 60, env: Optional[Dict[str, str]] = None) -> dict:
        """
        Execute a shell command in the sandbox.
        
        Args:
            command: Shell command to execute
            timeout: Execution timeout in seconds
            env: Optional environment variables
        
        Returns:
            Dictionary with 'stdout', 'stderr', and 'exit_code'
        """
        if not self.is_active:
            raise SandboxOperationError("Sandbox is not active")
        
        try:
            response = self._session.post(
                f"{self.api_url}/sessions/{self.session_id}/commands",
                json={
                    'command': command,
                    'timeout': timeout,
                    'env': env or {},
                    'async': False
                },
                timeout=timeout + 10  # Add buffer to HTTP timeout
            )
            
            if response.status_code != 200:
                error_msg = response.json().get('message', 'Command execution failed')
                raise SandboxOperationError(error_msg)
            
            result = response.json()
            return {
                'stdout': result.get('stdout', ''),
                'stderr': result.get('stderr', ''),
                'exit_code': result.get('exitCode', -1)
            }
            
        except requests.exceptions.Timeout:
            raise SandboxOperationError(f"Command timed out after {timeout} seconds")
        except requests.exceptions.RequestException as e:
            raise SandboxOperationError(f"Failed to execute command: {e}")
    
    def run_python_code(self, code: str, timeout: int = 60) -> dict:
        """
        Execute Python code in the sandbox.
        
        Args:
            code: Python code to execute
            timeout: Execution timeout in seconds
        
        Returns:
            Dictionary with 'stdout', 'stderr', and 'exit_code'
        """
        if not self.is_active:
            raise SandboxOperationError("Sandbox is not active")
        
        try:
            response = self._session.post(
                f"{self.api_url}/sessions/{self.session_id}/code/python",
                json={
                    'code': code,
                    'timeout': timeout
                },
                timeout=timeout + 10
            )
            
            if response.status_code != 200:
                error_msg = response.json().get('message', 'Code execution failed')
                raise SandboxOperationError(error_msg)
            
            result = response.json()
            return {
                'stdout': result.get('stdout', ''),
                'stderr': result.get('stderr', ''),
                'exit_code': result.get('exitCode', -1)
            }
            
        except requests.exceptions.Timeout:
            raise SandboxOperationError(f"Code execution timed out after {timeout} seconds")
        except requests.exceptions.RequestException as e:
            raise SandboxOperationError(f"Failed to execute code: {e}")
    
    def upload_file(self, local_path: str, remote_name: Optional[str] = None, overwrite: bool = False):
        """
        Upload a file to the sandbox.
        
        Args:
            local_path: Path to local file
            remote_name: Destination filename in sandbox (defaults to basename of local_path)
            overwrite: Whether to overwrite existing file
        """
        if not self.is_active:
            raise SandboxOperationError("Sandbox is not active")
        
        if not os.path.exists(local_path):
            raise ValueError(f"Local path does not exist: {local_path}")
        
        if remote_name is None:
            remote_name = os.path.basename(local_path)
        
        try:
            with open(local_path, 'rb') as f:
                files = {'file': (remote_name, f)}
                response = self._session.post(
                    f"{self.api_url}/sessions/{self.session_id}/files",
                    files=files,
                    params={'path': remote_name, 'overwrite': overwrite},
                    headers={k: v for k, v in self.headers.items() if k != 'Content-Type'},  # Remove Content-Type for multipart
                    timeout=self.timeout
                )
            
            if response.status_code == 409:
                raise SandboxOperationError(f"File {remote_name} already exists (use overwrite=True)")
            elif response.status_code not in [200, 201]:
                error_msg = response.json().get('message', 'File upload failed')
                raise SandboxOperationError(error_msg)
                
        except requests.exceptions.RequestException as e:
            raise SandboxOperationError(f"Failed to upload file: {e}")
    
    def download_file(self, remote_name: str, local_path: Optional[str] = None):
        """
        Download a file from the sandbox.
        
        Args:
            remote_name: Filename in sandbox to download
            local_path: Local destination path (defaults to remote_name)
        """
        if not self.is_active:
            raise SandboxOperationError("Sandbox is not active")
        
        if local_path is None:
            local_path = remote_name
        
        try:
            response = self._session.get(
                f"{self.api_url}/sessions/{self.session_id}/files/{remote_name}",
                timeout=self.timeout,
                stream=True
            )
            
            if response.status_code != 200:
                if response.status_code == 404:
                    raise SandboxOperationError(f"File not found: {remote_name}")
                error_msg = response.json().get('message', 'File download failed')
                raise SandboxOperationError(error_msg)
            
            # Write file in chunks
            with open(local_path, 'wb') as f:
                for chunk in response.iter_content(chunk_size=8192):
                    if chunk:
                        f.write(chunk)
                        
        except requests.exceptions.RequestException as e:
            raise SandboxOperationError(f"Failed to download file: {e}")
    
    def list_files(self, path: str = ".") -> list:
        """
        List files in the sandbox directory.
        
        Args:
            path: Subdirectory path (relative to sandbox root)
        
        Returns:
            List of file information dictionaries
        """
        if not self.is_active:
            raise SandboxOperationError("Sandbox is not active")
        
        try:
            response = self._session.get(
                f"{self.api_url}/sessions/{self.session_id}/files",
                params={'path': path},
                timeout=self.timeout
            )
            
            if response.status_code != 200:
                error_msg = response.json().get('message', 'Failed to list files')
                raise SandboxOperationError(error_msg)
            
            return response.json().get('files', [])
            
        except requests.exceptions.RequestException as e:
            raise SandboxOperationError(f"Failed to list files: {e}")
    
    def delete_file(self, remote_name: str):
        """
        Delete a file from the sandbox.
        
        Args:
            remote_name: Filename in sandbox to delete
        """
        if not self.is_active:
            raise SandboxOperationError("Sandbox is not active")
        
        try:
            response = self._session.delete(
                f"{self.api_url}/sessions/{self.session_id}/files/{remote_name}",
                timeout=self.timeout
            )
            
            if response.status_code == 404:
                raise SandboxOperationError(f"File not found: {remote_name}")
            elif response.status_code != 204:
                error_msg = response.json().get('message', 'File deletion failed')
                raise SandboxOperationError(error_msg)
                
        except requests.exceptions.RequestException as e:
            raise SandboxOperationError(f"Failed to delete file: {e}")
    
    def get_session_info(self) -> Dict[str, Any]:
        """
        Get information about the current session.
        
        Returns:
            Session information dictionary
        """
        if not self.is_active:
            raise SandboxOperationError("Sandbox is not active")
        
        try:
            response = self._session.get(
                f"{self.api_url}/sessions/{self.session_id}",
                timeout=self.timeout
            )
            
            if response.status_code != 200:
                error_msg = response.json().get('message', 'Failed to get session info')
                raise SandboxOperationError(error_msg)
            
            return response.json()
            
        except requests.exceptions.RequestException as e:
            raise SandboxOperationError(f"Failed to get session info: {e}")
    
    def extend_session(self, additional_ttl: int):
        """
        Extend the session time-to-live.
        
        Args:
            additional_ttl: Additional time in seconds to add to TTL
        """
        if not self.is_active:
            raise SandboxOperationError("Sandbox is not active")
        
        try:
            response = self._session.patch(
                f"{self.api_url}/sessions/{self.session_id}",
                json={'ttl': additional_ttl},
                timeout=self.timeout
            )
            
            if response.status_code != 200:
                error_msg = response.json().get('message', 'Failed to extend session')
                raise SandboxOperationError(error_msg)
                
        except requests.exceptions.RequestException as e:
            raise SandboxOperationError(f"Failed to extend session: {e}")


# Example usage
if __name__ == '__main__':
    # Using the REST API sandbox
    try:
        with RemoteSandboxAPI(
            api_url='http://localhost:8080/v1',
            api_key='your-api-key-here',
            ttl=3600,
            metadata={'project': 'test'}
        ) as sandbox:
            # Run a command
            result = sandbox.run_command('ls -la')
            print(f"Output: {result['stdout']}")
            
            # Execute Python code
            code_result = sandbox.run_python_code('''
import sys
print(f"Python version: {sys.version}")
print("Hello from sandbox!")
            ''')
            print(f"Python output: {code_result['stdout']}")
            
            # Upload a file
            # sandbox.upload_file('local_script.py', 'remote_script.py')
            
            # Download a file
            # sandbox.download_file('results.txt', 'local_results.txt')
            
            # List files
            files = sandbox.list_files()
            print(f"Files in sandbox: {files}")
            
            # Get session info
            info = sandbox.get_session_info()
            print(f"Session expires at: {info['expiresAt']}")
            
    except (SandboxConnectionError, SandboxOperationError) as e:
        print(f"Error: {e}")
