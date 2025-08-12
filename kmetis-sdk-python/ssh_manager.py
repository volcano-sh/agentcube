"""
SSH session management for Kubernetes Sandbox SDK
"""
import logging
import paramiko
import threading
import time
from typing import Dict, Optional, Tuple
from exceptions import SSHConnectionError


class SSHManager:
    """
    Manages SSH connections to sandboxes with connection pooling
    """
    
    def __init__(self, max_sessions: int = 10, session_timeout: int = 300):
        """
        Initialize SSH manager
        
        Args:
            max_sessions: Maximum number of concurrent SSH sessions
            session_timeout: Timeout for SSH sessions in seconds
        """
        self.max_sessions = max_sessions
        self.session_timeout = session_timeout
        self.sessions: Dict[str, Tuple[paramiko.SSHClient, float]] = {}
        self.lock = threading.Lock()
        self.logger = logging.getLogger(__name__)
    
    def get_session(self, ip_address: str, port: str, username: str, private_key: str) -> paramiko.SSHClient:
        """
        Get or create an SSH session for a sandbox
        
        Args:
            ip_address: IP address of the sandbox
            username: SSH username
            private_key: SSH private key for authentication
            
        Returns:
            paramiko.SSHClient: SSH client instance
            
        Raises:
            SSHConnectionError: If connection fails
        """
        session_key = f"{ip_address}:{username}"
        
        with self.lock:
            # Check if we have an existing valid session
            if session_key in self.sessions:
                ssh_client, timestamp = self.sessions[session_key]
                # Check if session is still valid
                if time.time() - timestamp < self.session_timeout:
                    try:
                        # Test if session is still alive
                        ssh_client.exec_command('echo test', timeout=5)
                        self.logger.debug(f"Reusing existing SSH session for {session_key}")
                        return ssh_client
                    except Exception:
                        self.logger.debug(f"Existing SSH session for {session_key} is dead, creating new one")
                        # Remove dead session
                        del self.sessions[session_key]
                else:
                    # Session timed out
                    self.logger.debug(f"SSH session for {session_key} timed out, creating new one")
                    del self.sessions[session_key]
            
            # Create new session
            if len(self.sessions) >= self.max_sessions:
                # Clean up oldest session if we're at the limit
                oldest_key = min(self.sessions.keys(), key=lambda k: self.sessions[k][1])
                oldest_client, _ = self.sessions[oldest_key]
                try:
                    oldest_client.close()
                except Exception:
                    pass
                del self.sessions[oldest_key]
                self.logger.debug(f"Removed oldest SSH session {oldest_key}")
            
            try:
                ssh_client = self._create_ssh_client(ip_address, port, username, private_key)
                self.sessions[session_key] = (ssh_client, time.time())
                self.logger.debug(f"Created new SSH session for {session_key}")
                return ssh_client
            except Exception as e:
                raise SSHConnectionError(f"Failed to establish SSH connection to {ip_address}: {str(e)}")
    
    def _create_ssh_client(self, ip_address: str, port: str, username: str, private_key: str) -> paramiko.SSHClient:
        """
        Create a new SSH client connection
        
        Args:
            ip_address: IP address of the sandbox
            username: SSH username
            private_key: SSH private key for authentication
            
        Returns:
            paramiko.SSHClient: New SSH client instance
        """
        ssh_client = paramiko.SSHClient()
        ssh_client.set_missing_host_key_policy(paramiko.AutoAddPolicy())
        
        # Load private key
        if private_key.startswith('-----BEGIN'):
            # Private key is in string format
            pkey = paramiko.RSAKey.from_private_key(private_key.encode())
        else:
            # Private key is a file path
            pkey = paramiko.RSAKey.from_private_key_file(private_key)
        
        # Connect to the sandbox
        ssh_client.connect(
            hostname=ip_address,
            port=port,
            username=username,
            pkey=pkey,
            timeout=30,
            look_for_keys=False,
            allow_agent=False
        )
        
        return ssh_client
    
    def close_session(self, ip_address: str, username: str) -> None:
        """
        Close and remove a specific SSH session
        
        Args:
            ip_address: IP address of the sandbox
            username: SSH username
        """
        session_key = f"{ip_address}:{username}"
        
        with self.lock:
            if session_key in self.sessions:
                ssh_client, _ = self.sessions[session_key]
                try:
                    ssh_client.close()
                except Exception:
                    pass
                del self.sessions[session_key]
                self.logger.debug(f"Closed SSH session for {session_key}")
    
    def cleanup_expired_sessions(self) -> None:
        """Clean up expired SSH sessions"""
        current_time = time.time()
        expired_keys = []
        
        with self.lock:
            for session_key, (_, timestamp) in self.sessions.items():
                if current_time - timestamp >= self.session_timeout:
                    expired_keys.append(session_key)
            
            for key in expired_keys:
                ssh_client, _ = self.sessions[key]
                try:
                    ssh_client.close()
                except Exception:
                    pass
                del self.sessions[key]
                self.logger.debug(f"Cleaned up expired SSH session {key}")
    
    def close_all_sessions(self) -> None:
        """Close all SSH sessions"""
        with self.lock:
            for ssh_client, _ in self.sessions.values():
                try:
                    ssh_client.close()
                except Exception:
                    pass
            self.sessions.clear()
            self.logger.debug("Closed all SSH sessions")
