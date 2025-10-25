"""
Sandbox API Python SDK - Sandbox Management (with session compatibility)

This module provides a Python client for managing sandboxes via REST and
connecting over HTTP CONNECT + SSH/SFTP. It now uses sandbox-first naming
(/sandboxes, sandboxId) with backward-compatible aliases for the older
session-based API.
"""

import requests
from typing import Optional, Dict, Any, List
from datetime import datetime
from enum import Enum
import socket
import ssl
from urllib.parse import urlparse
import paramiko


class SessionStatus(Enum):
    """Session status enumeration (alias for SandboxStatus)"""
    RUNNING = "running"
    PAUSED = "paused"

# Backward/forward alias
SandboxStatus = SessionStatus


class SandboxAPIError(Exception):
    """Base exception for Sandbox API errors"""
    def __init__(self, message: str, error_code: Optional[str] = None, 
                 details: Optional[Dict[str, Any]] = None, status_code: Optional[int] = None):
        super().__init__(message)
        self.message = message
        self.error_code = error_code
        self.details = details or {}
        self.status_code = status_code


class SandboxConnectionError(SandboxAPIError):
    """Raised when connection to API fails"""
    pass


class SandboxOperationError(SandboxAPIError):
    """Raised when an operation fails"""
    pass


class SessionNotFoundError(SandboxAPIError):
    """Raised when a session is not found"""
    pass


class UnauthorizedError(SandboxAPIError):
    """Raised when authentication fails"""
    pass


class RateLimitError(SandboxAPIError):
    """Raised when rate limit is exceeded"""
    def __init__(self, message: str, limit: Optional[int] = None, 
                 remaining: Optional[int] = None, reset: Optional[int] = None, **kwargs):
        super().__init__(message, **kwargs)
        self.limit = limit
        self.remaining = remaining
        self.reset = reset


class Sandbox:
    """
    Represents a sandbox instance. Compatible with legacy session objects.
    """
    def __init__(self, data: Dict[str, Any]):
        # Accept either sandboxId (new) or sessionId (legacy)
        sid = data.get('sandboxId') or data.get('sessionId')
        if not sid:
            raise ValueError("Missing sandboxId/sessionId in response data")
        self.sandbox_id: str = sid
        # Backward-compat alias
        self.session_id: str = sid

        self.status: SessionStatus = SessionStatus(data['status'])
        self.created_at: datetime = datetime.fromisoformat(data['createdAt'].replace('Z', '+00:00'))
        self.expires_at: datetime = datetime.fromisoformat(data['expiresAt'].replace('Z', '+00:00'))
        self.last_activity_at: Optional[datetime] = None
        if 'lastActivityAt' in data and data['lastActivityAt']:
            self.last_activity_at = datetime.fromisoformat(data['lastActivityAt'].replace('Z', '+00:00'))
        self.metadata: Dict[str, Any] = data.get('metadata', {})
        self._raw_data = data

    def to_dict(self) -> Dict[str, Any]:
        """Convert sandbox to dictionary"""
        return self._raw_data.copy()

    def __repr__(self) -> str:
        return f"Sandbox(id={self.sandbox_id}, status={self.status.value}, expires_at={self.expires_at})"

# Backward-compat alias
Session = Sandbox


class SandboxClient:
    """
    Client for managing sandboxes via REST API (/sandboxes endpoints).
    Backward-compatible wrapper methods are available via SessionsClient.
    """
    
    def __init__(
        self, 
        api_url: str,
        bearer_token: str,
        timeout: int = 30,
        verify_ssl: bool = True
    ):
        """
        Initialize the Sandbox API client.
        
        Args:
            api_url: Base URL of the sandbox API (e.g., "https://api.sandbox.example.com/v1")
            bearer_token: JWT bearer token for authentication
            timeout: Default timeout for HTTP requests in seconds
            verify_ssl: Whether to verify SSL certificates
        """
        self.api_url = api_url.rstrip('/')
        self.timeout = timeout
        self.verify_ssl = verify_ssl
        
        # Setup session with authentication
        self._session = requests.Session()
        self._session.headers.update({
            'Authorization': f'Bearer {bearer_token}',
            'Content-Type': 'application/json',
            'Accept': 'application/json'
        })
    
    def _handle_response(self, response: requests.Response) -> Dict[str, Any]:
        """
        Handle API response and raise appropriate exceptions.
        
        Args:
            response: The requests Response object
            
        Returns:
            Parsed JSON response
            
        Raises:
            UnauthorizedError: For 401 responses
            SessionNotFoundError: For 404 responses
            RateLimitError: For 429 responses
            SandboxOperationError: For other error responses
        """
        try:
            response_data = response.json()
        except ValueError:
            response_data = {}
        
        if response.status_code == 401:
            raise UnauthorizedError(
                message=response_data.get('message', 'Authentication failed'),
                error_code=response_data.get('error'),
                details=response_data.get('details'),
                status_code=response.status_code
            )
        elif response.status_code == 404:
            raise SessionNotFoundError(
                message=response_data.get('message', 'Session not found'),
                error_code=response_data.get('error'),
                details=response_data.get('details'),
                status_code=response.status_code
            )
        elif response.status_code == 429:
            raise RateLimitError(
                message=response_data.get('message', 'Rate limit exceeded'),
                error_code=response_data.get('error'),
                details=response_data.get('details'),
                status_code=response.status_code,
                limit=int(response.headers.get('X-RateLimit-Limit', 0)),
                remaining=int(response.headers.get('X-RateLimit-Remaining', 0)),
                reset=int(response.headers.get('X-RateLimit-Reset', 0))
            )
        elif response.status_code >= 400:
            raise SandboxOperationError(
                message=response_data.get('message', f'Request failed with status {response.status_code}'),
                error_code=response_data.get('error'),
                details=response_data.get('details'),
                status_code=response.status_code
            )
        
        return response_data
    
    # New sandbox-first API
    def create_sandbox(
        self,
        ttl: int = 3600,
        image: Optional[str] = None,
        metadata: Optional[Dict[str, Any]] = None,
        ssh_public_key: Optional[str] = None,
    ) -> Sandbox:
        """
        Create a new sandbox.
        
        Args:
            ttl: Time-to-live in seconds (default 3600, min 60, max 28800)
            image: Sandbox environment image to use
            metadata: Optional metadata to attach to the session
            ssh_public_key: Optional OpenSSH-formatted public key (e.g., ssh-ed25519, ecdsa-sha2-nistp256, ssh-rsa)
        
        Returns:
            Sandbox object representing the created sandbox
        
        Raises:
            SandboxConnectionError: If connection fails
            UnauthorizedError: If authentication fails
            RateLimitError: If rate limit is exceeded
            SandboxOperationError: For other errors
        
        Example:
            >>> client = SandboxClient(api_url='...', bearer_token='...')
            >>> sandbox = client.create_sandbox(
            ...     ttl=7200,
            ...     image='python:3.11',
            ...     metadata={'user': 'john', 'project': 'test'}
            ... )
            >>> print(sandbox.sandbox_id)
        """
        if not 60 <= ttl <= 28800:
            raise ValueError(f"TTL must be between 60 and 28800 seconds, got {ttl}")
        
        payload = {'ttl': ttl}
        if image:
            payload['image'] = image
        if metadata:
            payload['metadata'] = metadata
        if ssh_public_key:
            if not isinstance(ssh_public_key, str) or not ssh_public_key.strip():
                raise ValueError("ssh_public_key must be a non-empty string when provided")
            payload['sshPublicKey'] = ssh_public_key
        
        try:
            response = self._session.post(
                f"{self.api_url}/sandboxes",
                json=payload,
                timeout=self.timeout,
                verify=self.verify_ssl
            )
            
            data = self._handle_response(response)
            return Sandbox(data)
            
        except requests.exceptions.RequestException as e:
            raise SandboxConnectionError(f"Failed to connect to API: {e}")
    
    def list_sandboxes(
        self,
        limit: int = 50,
        offset: int = 0
    ) -> Dict[str, Any]:
        """
        List all active sandboxes for the authenticated user.
        
        Args:
            limit: Maximum number of sessions to return (default 50, max 100)
            offset: Number of sessions to skip (for pagination)
        
        Returns:
            Dictionary with 'sandboxes', 'total', 'limit', and 'offset'
        
        Raises:
            SandboxConnectionError: If connection fails
            UnauthorizedError: If authentication fails
            RateLimitError: If rate limit is exceeded
            SandboxOperationError: For other errors
        
        Example:
            >>> client = SandboxClient(api_url='...', bearer_token='...')
            >>> result = client.list_sandboxes(limit=10)
            >>> for sbox in result['sandboxes']:
            ...     print(sbox.sandbox_id)
        """
        if not 1 <= limit <= 100:
            raise ValueError(f"Limit must be between 1 and 100, got {limit}")
        if offset < 0:
            raise ValueError(f"Offset must be non-negative, got {offset}")
        
        try:
            response = self._session.get(
                f"{self.api_url}/sandboxes",
                params={'limit': limit, 'offset': offset},
                timeout=self.timeout,
                verify=self.verify_ssl
            )
            
            data = self._handle_response(response)
            # Convert sandbox data to Sandbox objects, support legacy key
            if 'sandboxes' in data:
                data['sandboxes'] = [Sandbox(s) for s in data['sandboxes']]
            elif 'sessions' in data:  # legacy
                data = {
                    'sandboxes': [Sandbox(s) for s in data.get('sessions', [])],
                    'total': data.get('total'),
                    'limit': data.get('limit'),
                    'offset': data.get('offset'),
                }
            return data
            
        except requests.exceptions.RequestException as e:
            raise SandboxConnectionError(f"Failed to connect to API: {e}")
    
    def get_sandbox(self, sandbox_id: str) -> Sandbox:
        """
        Get details about a specific sandbox.
        
        Args:
            sandbox_id: Unique sandbox identifier (UUID)
        
        Returns:
            Sandbox object with details
        
        Raises:
            SandboxConnectionError: If connection fails
            SessionNotFoundError: If session doesn't exist
            UnauthorizedError: If authentication fails
            SandboxOperationError: For other errors
        
        Example:
            >>> client = SandboxClient(api_url='...', bearer_token='...')
            >>> sbox = client.get_sandbox('550e8400-e29b-41d4-a716-446655440000')
            >>> print(f"Status: {sbox.status}")
            >>> print(f"Expires: {sbox.expires_at}")
        """
        if not sandbox_id:
            raise ValueError("sandbox_id is required")
        
        try:
            response = self._session.get(
                f"{self.api_url}/sandboxes/{sandbox_id}",
                timeout=self.timeout,
                verify=self.verify_ssl
            )
            
            data = self._handle_response(response)
            return Sandbox(data)
            
        except requests.exceptions.RequestException as e:
            raise SandboxConnectionError(f"Failed to connect to API: {e}")
    
    def delete_sandbox(self, sandbox_id: str) -> None:
        """
        Delete a sandbox.
        
        Terminates the sandbox and removes all its resources.
        All files and data in the sandbox will be permanently deleted.
        
        Args:
            sandbox_id: Unique sandbox identifier (UUID)
        
        Raises:
            SandboxConnectionError: If connection fails
            SessionNotFoundError: If session doesn't exist
            UnauthorizedError: If authentication fails
            SandboxOperationError: For other errors
        
        Example:
            >>> client = SandboxClient(api_url='...', bearer_token='...')
            >>> client.delete_sandbox('550e8400-e29b-41d4-a716-446655440000')
        """
        if not sandbox_id:
            raise ValueError("sandbox_id is required")
        
        try:
            response = self._session.delete(
                f"{self.api_url}/sandboxes/{sandbox_id}",
                timeout=self.timeout,
                verify=self.verify_ssl
            )
            
            # DELETE returns 200 according to updated spec
            if response.status_code not in [200, 204]:
                self._handle_response(response)
            
        except requests.exceptions.RequestException as e:
            raise SandboxConnectionError(f"Failed to connect to API: {e}")
    
    def pause_sandbox(self, sandbox_id: str) -> Sandbox:
        """
        Pause a running sandbox. Returns the updated Sandbox.

        Raises 409 Conflict if the sandbox is not in a pausable state.
        """
        if not sandbox_id:
            raise ValueError("sandbox_id is required")
        try:
            response = self._session.post(
                f"{self.api_url}/sandboxes/{sandbox_id}/pause",
                timeout=self.timeout,
                verify=self.verify_ssl,
            )
            data = self._handle_response(response)
            return Sandbox(data)
        except requests.exceptions.RequestException as e:
            raise SandboxConnectionError(f"Failed to connect to API: {e}")

    def resume_sandbox(self, sandbox_id: str) -> Sandbox:
        """
        Resume a paused sandbox. Returns the updated Sandbox.

        Raises 409 Conflict if the sandbox is not in a resumable state.
        """
        if not sandbox_id:
            raise ValueError("sandbox_id is required")
        try:
            response = self._session.post(
                f"{self.api_url}/sandboxes/{sandbox_id}/resume",
                timeout=self.timeout,
                verify=self.verify_ssl,
            )
            data = self._handle_response(response)
            return Sandbox(data)
        except requests.exceptions.RequestException as e:
            raise SandboxConnectionError(f"Failed to connect to API: {e}")
    def run_code(
        self,
        sandbox_id: str,
        code: str,
        language: str = "python",
        timeout: int = 60,
    ) -> Dict[str, Any]:
        """
        Execute code in the sandbox via the REST API.

        This uses the /sandboxes/{sandboxId}/code endpoint defined in the API spec.

        Args:
            sandbox_id: Unique sandbox identifier (UUID)
            code: Code snippet to execute (required)
            language: Programming language (one of: 'python', 'javascript', 'bash'). Default: 'python'
            timeout: Execution timeout in seconds (min 1, max 300). Default: 60

        Returns:
            A dictionary matching CommandResult schema:
            { 'status': 'completed'|'failed'|'timeout', 'exitCode': int|None, 'stdout': str, 'stderr': str }

        Raises:
            ValueError: If parameters are invalid
            SandboxConnectionError: On network/TLS failures
            UnauthorizedError: If authentication fails
            SessionNotFoundError: If the sandbox doesn't exist
            SandboxOperationError: For other API errors

        Example:
            >>> client = SandboxClient(api_url='...', bearer_token='...')
            >>> result = client.run_code(sandbox_id, code='print("hi")', language='python', timeout=30)
            >>> print(result['stdout'])
        """
        if not sandbox_id:
            raise ValueError("sandbox_id is required")
        if not isinstance(code, str) or code.strip() == "":
            raise ValueError("code must be a non-empty string")
        allowed_languages = {"python", "javascript", "bash"}
        if language not in allowed_languages:
            raise ValueError(f"language must be one of {sorted(allowed_languages)}, got {language}")
        if not (1 <= int(timeout) <= 300):
            raise ValueError(f"timeout must be between 1 and 300 seconds, got {timeout}")

        payload = {
            "language": language,
            "code": code,
            "timeout": int(timeout),
        }

        try:
            response = self._session.post(
                f"{self.api_url}/sandboxes/{sandbox_id}/code",
                json=payload,
                timeout=self.timeout,
                verify=self.verify_ssl,
            )
            data = self._handle_response(response)
            return data
        except requests.exceptions.RequestException as e:
            raise SandboxConnectionError(f"Failed to connect to API: {e}")
    
    def close(self):
        """Close the HTTP session"""
        self._session.close()
    
    def __enter__(self):
        """Context manager entry"""
        return self
    
    def __exit__(self, exc_type, exc_val, exc_tb):
        """Context manager exit"""
        self.close()


class HTTPConnectTunnel:
    """
    Establishes an HTTP CONNECT tunnel to the API server, which acts as a proxy
    and transparently redirects the TCP stream to the sandbox backend for a given session.

    This does NOT introduce another gateway; it uses the same API server (api_url)
    to initiate the CONNECT and tunnels SSH/SFTP through it.
    """

    def __init__(
        self,
        api_url: str,
        bearer_token: str,
        session_id: Optional[str] = None,
        sandbox_id: Optional[str] = None,
        connect_path_template: str = "/sandboxes/{sandboxId}",
        timeout: int = 10,
        verify_ssl: bool = True,
        extra_headers: Optional[Dict[str, str]] = None,
    ):
        self.api_url = api_url.rstrip('/')
        self.token = bearer_token
        self.session_id = session_id
        self.sandbox_id = sandbox_id or session_id
        self.connect_path_template = connect_path_template
        self.timeout = timeout
        self.verify_ssl = verify_ssl
        self.extra_headers = extra_headers or {}

        self._socket = None  # Underlying socket (may be SSL wrapped if https)

    def open(self) -> socket.socket:
        """
        Open the CONNECT tunnel and return a socket-like object suitable
        for passing to libraries like Paramiko as the 'sock' parameter.
        """
        parsed = urlparse(self.api_url)
        scheme = parsed.scheme or 'https'
        host = parsed.hostname
        port = parsed.port or (443 if scheme == 'https' else 80)

        if not host:
            raise SandboxConnectionError("Invalid api_url: missing host")

        # Create TCP connection to the API server (acts as CONNECT proxy)
        base_sock = socket.create_connection((host, port), timeout=self.timeout)

        # If https, wrap with TLS for the CONNECT request
        if scheme == 'https':
            if self.verify_ssl:
                context = ssl.create_default_context()
            else:
                context = ssl.SSLContext(ssl.PROTOCOL_TLS_CLIENT)
                context.check_hostname = False
                context.verify_mode = ssl.CERT_NONE
            sock = context.wrap_socket(base_sock, server_hostname=host)
        else:
            sock = base_sock

        # Build CONNECT request; server routes by path (sandbox-scoped)
        # Support both placeholders for compatibility
        path = self.connect_path_template.format(sessionId=self.sandbox_id, sandboxId=self.sandbox_id)
        req_lines = [
            f"CONNECT {path} HTTP/1.1",
            f"Host: {host}",
            f"Proxy-Authorization: Bearer {self.token}",
            "Proxy-Connection: keep-alive",
        ]
        for k, v in self.extra_headers.items():
            req_lines.append(f"{k}: {v}")
        req_lines.append("")
        req_lines.append("")
        request_bytes = "\r\n".join(req_lines).encode()
        sock.sendall(request_bytes)

        # Read response headers
        response = b""
        sock.settimeout(self.timeout)
        while b"\r\n\r\n" not in response:
            chunk = sock.recv(1024)
            if not chunk:
                break
            response += chunk
            if len(response) > 16 * 1024:  # 16KB safety cap
                break

        try:
            header_text = response.decode(errors='ignore')
        except Exception:
            header_text = ""

        status_line = header_text.split("\r\n", 1)[0] if header_text else ""
        if not status_line.startswith("HTTP/1.1 200") and not status_line.startswith("HTTP/1.0 200"):
            # Try to extract a short reason
            reason = status_line or header_text[:200]
            try:
                sock.close()
            except Exception:
                pass
            raise SandboxConnectionError(f"HTTP CONNECT failed: {reason}")

        # From here on, the socket is a raw tunnel to the backend; return it
        self._socket = sock
        return sock

    def close(self):
        if self._socket:
            try:
                self._socket.close()
            finally:
                self._socket = None


class SandboxSSHClient:
    """
    SSH/SFTP client that connects to the sandbox via the API server using
    an HTTP CONNECT tunnel. The API server transparently redirects traffic
    to the correct sandbox backend for the given session.

    Usage:
        with SessionSSHClient(api_url, token, session_id, username=...) as ssh:
            out = ssh.run_command("ls -la")
            ssh.upload_file("local.txt", "remote.txt")
            ssh.download_file("remote.txt", "local-copy.txt")
    """

    def __init__(
        self,
        api_url: str,
        bearer_token: str,
        username: str,
        session_id: Optional[str] = None,
        sandbox_id: Optional[str] = None,
        password: Optional[str] = None,
        pkey: Optional[paramiko.PKey] = None,
        timeout: int = 20,
        verify_ssl: bool = True,
        connect_path_template: str = "/sandboxes/{sandboxId}",
        host_key_policy: Optional[paramiko.MissingHostKeyPolicy] = None,
        get_pty: bool = False,
    ):
        self.api_url = api_url
        self.token = bearer_token
        self.session_id = session_id
        self.sandbox_id = sandbox_id or session_id
        self.username = username
        self.password = password
        self.pkey = pkey
        self.timeout = timeout
        self.verify_ssl = verify_ssl
        self.connect_path_template = connect_path_template
        self.host_key_policy = host_key_policy or paramiko.AutoAddPolicy()
        self.get_pty = get_pty

        self._tunnel: Optional[socket.socket] = None
        self._ssh: Optional[paramiko.SSHClient] = None
        self._sftp: Optional[paramiko.SFTPClient] = None

    def __enter__(self):
        self.connect()
        return self

    def __exit__(self, exc_type, exc, tb):
        self.close()

    def connect(self):
        # 1) Establish HTTP CONNECT tunnel to the API server for this session
        tunnel = HTTPConnectTunnel(
            api_url=self.api_url,
            bearer_token=self.token,
            session_id=self.session_id,
            sandbox_id=self.sandbox_id,
            connect_path_template=self.connect_path_template,
            timeout=self.timeout,
            verify_ssl=self.verify_ssl,
        )
        sock = tunnel.open()

        # 2) Create SSH client over the tunnel
        ssh = paramiko.SSHClient()
        ssh.set_missing_host_key_policy(self.host_key_policy)

        # Note: hostname is not used for network connection when sock is provided,
        # but Paramiko still requires a value for host key policies. We pass the
        # session ID as a logical hostname for known_hosts separation if needed.
        ssh.connect(
            hostname=f"sandbox-{self.sandbox_id}",
            username=self.username,
            password=self.password,
            pkey=self.pkey,
            sock=sock,
            timeout=self.timeout,
            allow_agent=False,
            look_for_keys=False,
            banner_timeout=self.timeout,
            auth_timeout=self.timeout,
        )

        self._tunnel = sock
        self._ssh = ssh

    def open_sftp(self) -> paramiko.SFTPClient:
        if not self._ssh:
            raise SandboxOperationError("SSH connection is not established")
        if not self._sftp:
            self._sftp = self._ssh.open_sftp()
        return self._sftp

    def run_command(self, command: str, timeout: int = 60) -> Dict[str, Any]:
        if not self._ssh:
            raise SandboxOperationError("SSH connection is not established")
        stdin, stdout, stderr = self._ssh.exec_command(command, timeout=timeout, get_pty=self.get_pty)
        out = stdout.read().decode(errors='ignore')
        err = stderr.read().decode(errors='ignore')
        exit_code = stdout.channel.recv_exit_status()
        return {"stdout": out, "stderr": err, "exit_code": exit_code}

    def upload_file(self, local_path: str, remote_path: str):
        sftp = self.open_sftp()
        sftp.put(local_path, remote_path)

    def download_file(self, remote_path: str, local_path: str):
        sftp = self.open_sftp()
        sftp.get(remote_path, local_path)

    def close(self):
        # Close SFTP, SSH, and tunnel in that order
        if self._sftp:
            try:
                self._sftp.close()
            except Exception:
                pass
            finally:
                self._sftp = None
        if self._ssh:
            try:
                self._ssh.close()
            except Exception:
                pass
            finally:
                self._ssh = None
        if self._tunnel:
            try:
                self._tunnel.close()
            except Exception:
                pass
            finally:
                self._tunnel = None

# Backward-compatible alias for SSH client
SessionSSHClient = SandboxSSHClient


class SessionsClient(SandboxClient):
    """Backward-compatible session-based client.

    Delegates to SandboxClient but preserves legacy method names and return shapes.
    """

    # Legacy create
    def create_session(
        self,
        ttl: int = 3600,
        image: Optional[str] = None,
        metadata: Optional[Dict[str, Any]] = None,
        ssh_public_key: Optional[str] = None,
    ) -> Session:
        return self.create_sandbox(ttl=ttl, image=image, metadata=metadata, ssh_public_key=ssh_public_key)

    def list_sessions(self, limit: int = 50, offset: int = 0) -> Dict[str, Any]:
        res = self.list_sandboxes(limit=limit, offset=offset)
        # Map back to legacy shape with 'sessions'
        return {
            'sessions': [Session(s.to_dict()) if isinstance(s, Sandbox) else s for s in res.get('sandboxes', [])],
            'total': res.get('total'),
            'limit': res.get('limit'),
            'offset': res.get('offset'),
        }

    def get_session(self, session_id: str) -> Session:
        return self.get_sandbox(session_id)

    def delete_session(self, session_id: str) -> None:
        return self.delete_sandbox(session_id)

    def run_code(
        self,
        session_id: str,
        code: str,
        language: str = "python",
        timeout: int = 60,
    ) -> Dict[str, Any]:
        return super().run_code(sandbox_id=session_id, code=code, language=language, timeout=timeout)

    def pause_session(self, session_id: str) -> Session:
        return self.pause_sandbox(session_id)

    def resume_session(self, session_id: str) -> Session:
        return self.resume_sandbox(session_id)


# Example usage
if __name__ == '__main__':
    import os
    
    # Example: Basic session management
    api_url = os.environ.get('SANDBOX_API_URL', 'http://localhost:8080/v1')
    bearer_token = os.environ.get('SANDBOX_API_TOKEN', 'your-token-here')
    
    try:
        with SandboxClient(api_url=api_url, bearer_token=bearer_token) as client:
            # Create a new session
            print("Creating sandbox...")
            sandbox = client.create_sandbox(
                ttl=7200,
                image='python:3.11',
                metadata={
                    'user': 'test-user',
                    'project': 'example',
                    'environment': 'development'
                }
            )
            print(f"✓ Created sandbox: {sandbox.sandbox_id}")
            print(f"  Status: {sandbox.status.value}")
            print(f"  Created: {sandbox.created_at}")
            print(f"  Expires: {sandbox.expires_at}")
            print(f"  Metadata: {sandbox.metadata}")
            
            # Get session details
            print(f"\nFetching sandbox details...")
            fetched_sandbox = client.get_sandbox(sandbox.sandbox_id)
            print(f"✓ Sandbox status: {fetched_sandbox.status.value}")
            
            # List all sessions
            print(f"\nListing all sandboxes...")
            result = client.list_sandboxes(limit=10)
            print(f"✓ Total sandboxes: {result['total']}")
            for s in result['sandboxes']:
                print(f"  - {s.sandbox_id} ({s.status.value})")
            
            # Delete the session
            print(f"\nDeleting sandbox {sandbox.sandbox_id}...")
            client.delete_sandbox(sandbox.sandbox_id)
            print(f"✓ Sandbox deleted")
            
            # Try to get deleted session (should fail)
            try:
                client.get_sandbox(sandbox.sandbox_id)
            except SessionNotFoundError:
                print("✓ Sandbox no longer exists (as expected)")
            
    except UnauthorizedError as e:
        print(f"❌ Authentication failed: {e.message}")
    except RateLimitError as e:
        print(f"❌ Rate limit exceeded: {e.message}")
        print(f"   Limit: {e.limit}, Remaining: {e.remaining}, Reset: {e.reset}")
    except SessionNotFoundError as e:
        print(f"❌ Sandbox not found: {e.message}")
    except SandboxOperationError as e:
        print(f"❌ Operation failed: {e.message}")
        if e.error_code:
            print(f"   Error code: {e.error_code}")
        if e.details:
            print(f"   Details: {e.details}")
    except SandboxConnectionError as e:
        print(f"❌ Connection failed: {e.message}")
