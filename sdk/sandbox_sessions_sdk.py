"""
Sandbox API Python SDK - Session Management

This module provides a Python client for managing sandbox sessions
via the REST API defined in sandbox-api-spec.yaml.
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
    """Session status enumeration"""
    RUNNING = "running"
    PAUSED = "paused"


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


class Session:
    """
    Represents a sandbox session.
    """
    def __init__(self, data: Dict[str, Any]):
        self.session_id: str = data['sessionId']
        self.status: SessionStatus = SessionStatus(data['status'])
        self.created_at: datetime = datetime.fromisoformat(data['createdAt'].replace('Z', '+00:00'))
        self.expires_at: datetime = datetime.fromisoformat(data['expiresAt'].replace('Z', '+00:00'))
        self.last_activity_at: Optional[datetime] = None
        if 'lastActivityAt' in data and data['lastActivityAt']:
            self.last_activity_at = datetime.fromisoformat(data['lastActivityAt'].replace('Z', '+00:00'))
        self.metadata: Dict[str, Any] = data.get('metadata', {})
        self._raw_data = data
    
    def to_dict(self) -> Dict[str, Any]:
        """Convert session to dictionary"""
        return self._raw_data.copy()
    
    def __repr__(self) -> str:
        return f"Session(id={self.session_id}, status={self.status.value}, expires_at={self.expires_at})"


class SessionsClient:
    """
    Client for managing sandbox sessions via REST API.
    
    This client handles all /sessions endpoints including:
    - Creating new sessions
    - Listing active sessions
    - Getting session details
    - Deleting sessions
    """
    
    def __init__(
        self, 
        api_url: str,
        bearer_token: str,
        timeout: int = 30,
        verify_ssl: bool = True
    ):
        """
        Initialize the Sessions API client.
        
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
    
    def create_session(
        self,
        ttl: int = 3600,
        image: Optional[str] = None,
        metadata: Optional[Dict[str, Any]] = None
    ) -> Session:
        """
        Create a new sandbox session.
        
        Args:
            ttl: Time-to-live in seconds (default 3600, min 60, max 28800)
            image: Sandbox environment image to use
            metadata: Optional metadata to attach to the session
        
        Returns:
            Session object representing the created session
        
        Raises:
            SandboxConnectionError: If connection fails
            UnauthorizedError: If authentication fails
            RateLimitError: If rate limit is exceeded
            SandboxOperationError: For other errors
        
        Example:
            >>> client = SessionsClient(api_url='...', bearer_token='...')
            >>> session = client.create_session(
            ...     ttl=7200,
            ...     image='python:3.11',
            ...     metadata={'user': 'john', 'project': 'test'}
            ... )
            >>> print(session.session_id)
        """
        if not 60 <= ttl <= 28800:
            raise ValueError(f"TTL must be between 60 and 28800 seconds, got {ttl}")
        
        payload = {'ttl': ttl}
        if image:
            payload['image'] = image
        if metadata:
            payload['metadata'] = metadata
        
        try:
            response = self._session.post(
                f"{self.api_url}/sessions",
                json=payload,
                timeout=self.timeout,
                verify=self.verify_ssl
            )
            
            data = self._handle_response(response)
            return Session(data)
            
        except requests.exceptions.RequestException as e:
            raise SandboxConnectionError(f"Failed to connect to API: {e}")
    
    def list_sessions(
        self,
        limit: int = 50,
        offset: int = 0
    ) -> Dict[str, Any]:
        """
        List all active sessions for the authenticated user.
        
        Args:
            limit: Maximum number of sessions to return (default 50, max 100)
            offset: Number of sessions to skip (for pagination)
        
        Returns:
            Dictionary with 'sessions', 'total', 'limit', and 'offset'
        
        Raises:
            SandboxConnectionError: If connection fails
            UnauthorizedError: If authentication fails
            RateLimitError: If rate limit is exceeded
            SandboxOperationError: For other errors
        
        Example:
            >>> client = SessionsClient(api_url='...', bearer_token='...')
            >>> result = client.list_sessions(limit=10)
            >>> for session_data in result['sessions']:
            ...     session = Session(session_data)
            ...     print(session.session_id)
        """
        if not 1 <= limit <= 100:
            raise ValueError(f"Limit must be between 1 and 100, got {limit}")
        if offset < 0:
            raise ValueError(f"Offset must be non-negative, got {offset}")
        
        try:
            response = self._session.get(
                f"{self.api_url}/sessions",
                params={'limit': limit, 'offset': offset},
                timeout=self.timeout,
                verify=self.verify_ssl
            )
            
            data = self._handle_response(response)
            # Convert session data to Session objects
            if 'sessions' in data:
                data['sessions'] = [Session(s) for s in data['sessions']]
            return data
            
        except requests.exceptions.RequestException as e:
            raise SandboxConnectionError(f"Failed to connect to API: {e}")
    
    def get_session(self, session_id: str) -> Session:
        """
        Get details about a specific session.
        
        Args:
            session_id: Unique session identifier (UUID)
        
        Returns:
            Session object with session details
        
        Raises:
            SandboxConnectionError: If connection fails
            SessionNotFoundError: If session doesn't exist
            UnauthorizedError: If authentication fails
            SandboxOperationError: For other errors
        
        Example:
            >>> client = SessionsClient(api_url='...', bearer_token='...')
            >>> session = client.get_session('550e8400-e29b-41d4-a716-446655440000')
            >>> print(f"Status: {session.status}")
            >>> print(f"Expires: {session.expires_at}")
        """
        if not session_id:
            raise ValueError("session_id is required")
        
        try:
            response = self._session.get(
                f"{self.api_url}/sessions/{session_id}",
                timeout=self.timeout,
                verify=self.verify_ssl
            )
            
            data = self._handle_response(response)
            return Session(data)
            
        except requests.exceptions.RequestException as e:
            raise SandboxConnectionError(f"Failed to connect to API: {e}")
    
    def delete_session(self, session_id: str) -> None:
        """
        Delete a sandbox session.
        
        Terminates the session and removes the sandbox.
        All files and data in the sandbox will be permanently deleted.
        
        Args:
            session_id: Unique session identifier (UUID)
        
        Raises:
            SandboxConnectionError: If connection fails
            SessionNotFoundError: If session doesn't exist
            UnauthorizedError: If authentication fails
            SandboxOperationError: For other errors
        
        Example:
            >>> client = SessionsClient(api_url='...', bearer_token='...')
            >>> client.delete_session('550e8400-e29b-41d4-a716-446655440000')
        """
        if not session_id:
            raise ValueError("session_id is required")
        
        try:
            response = self._session.delete(
                f"{self.api_url}/sessions/{session_id}",
                timeout=self.timeout,
                verify=self.verify_ssl
            )
            
            # DELETE returns 200 according to updated spec
            if response.status_code not in [200, 204]:
                self._handle_response(response)
            
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
        session_id: str,
        connect_path_template: str = "/sessions/{sessionId}/tunnel",
        timeout: int = 10,
        verify_ssl: bool = True,
        extra_headers: Optional[Dict[str, str]] = None,
    ):
        self.api_url = api_url.rstrip('/')
        self.token = bearer_token
        self.session_id = session_id
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

        # Build CONNECT request; server routes by path (session-scoped)
        path = self.connect_path_template.format(sessionId=self.session_id)
        req_lines = [
            f"CONNECT {path} HTTP/1.1",
            f"Host: {host}",
            f"Authorization: Bearer {self.token}",
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


class SessionSSHClient:
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
        session_id: str,
        username: str,
        password: Optional[str] = None,
        pkey: Optional[paramiko.PKey] = None,
        timeout: int = 20,
        verify_ssl: bool = True,
        connect_path_template: str = "/sessions/{sessionId}/tunnel",
        host_key_policy: Optional[paramiko.MissingHostKeyPolicy] = None,
        get_pty: bool = False,
    ):
        self.api_url = api_url
        self.token = bearer_token
        self.session_id = session_id
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
            hostname=f"session-{self.session_id}",
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


# Example usage
if __name__ == '__main__':
    import os
    
    # Example: Basic session management
    api_url = os.environ.get('SANDBOX_API_URL', 'http://localhost:8080/v1')
    bearer_token = os.environ.get('SANDBOX_API_TOKEN', 'your-token-here')
    
    try:
        with SessionsClient(api_url=api_url, bearer_token=bearer_token) as client:
            # Create a new session
            print("Creating session...")
            session = client.create_session(
                ttl=7200,
                image='python:3.11',
                metadata={
                    'user': 'test-user',
                    'project': 'example',
                    'environment': 'development'
                }
            )
            print(f"✓ Created session: {session.session_id}")
            print(f"  Status: {session.status.value}")
            print(f"  Created: {session.created_at}")
            print(f"  Expires: {session.expires_at}")
            print(f"  Metadata: {session.metadata}")
            
            # Get session details
            print(f"\nFetching session details...")
            fetched_session = client.get_session(session.session_id)
            print(f"✓ Session status: {fetched_session.status.value}")
            
            # List all sessions
            print(f"\nListing all sessions...")
            result = client.list_sessions(limit=10)
            print(f"✓ Total sessions: {result['total']}")
            for s in result['sessions']:
                print(f"  - {s.session_id} ({s.status.value})")
            
            # Delete the session
            print(f"\nDeleting session {session.session_id}...")
            client.delete_session(session.session_id)
            print(f"✓ Session deleted")
            
            # Try to get deleted session (should fail)
            try:
                client.get_session(session.session_id)
            except SessionNotFoundError:
                print("✓ Session no longer exists (as expected)")
            
    except UnauthorizedError as e:
        print(f"❌ Authentication failed: {e.message}")
    except RateLimitError as e:
        print(f"❌ Rate limit exceeded: {e.message}")
        print(f"   Limit: {e.limit}, Remaining: {e.remaining}, Reset: {e.reset}")
    except SessionNotFoundError as e:
        print(f"❌ Session not found: {e.message}")
    except SandboxOperationError as e:
        print(f"❌ Operation failed: {e.message}")
        if e.error_code:
            print(f"   Error code: {e.error_code}")
        if e.details:
            print(f"   Details: {e.details}")
    except SandboxConnectionError as e:
        print(f"❌ Connection failed: {e.message}")
