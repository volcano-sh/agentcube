import threading
import time
from typing import Dict, Tuple

import paramiko

from constants import DEFAULT_SSH_TIMEOUT
from services.exceptions import SSHConnectionError
from services.log import get_logger


class SSHClient:
    def __init__(self, max_sessions: int = 100,
                 session_timeout: float = 300):
        self._max_sessions = max_sessions
        self._session_timeout = session_timeout
        self._sessions: Dict[str, Tuple[paramiko.SSHClient, float]] = {}
        self._lock = threading.Lock()
        self._logger = get_logger(f"{__name__}.SSHClient")

    def __enter__(self):
        return self

    def __exit__(self, exc_type, exc_val, exc_tb):
        """Ensure safe shutdown"""
        self.close_all_sessions()

    def get_session(self, ip_addr: str, port: int, username: str, private_key: str) -> paramiko.SSHClient:
        session_key = f"{ip_addr}:{port}:{username}"
        with self._lock:
            # Check if we have an existing valid session
            if session_key in self._sessions:
                ssh_client, timestamp = self._sessions[session_key]
                # Check if session is still valid
                if time.time() - timestamp < self._session_timeout:
                    try:
                        # Test if session is still alive
                        ssh_client.exec_command('echo test', timeout=5)
                        self._logger.debug(f"Reusing existing SSH session for {session_key}")
                        return ssh_client
                    except Exception:
                        self._logger.debug(f"Existing SSH session for {session_key} is dead, creating new one")
                        # Remove expired session
                        del self._sessions[session_key]
                else:
                    # Session timed out
                    self._logger.debug(f"SSH session for {session_key} timed out, creating new one")
                    del self._sessions[session_key]

            # Create new session
            if len(self._sessions) >= self._max_sessions:
                # Clean up oldest session if we're at the limit
                oldest_key = min(self._sessions.keys(), key=lambda k: self._sessions[k][1])
                oldest_client, _ = self._sessions[oldest_key]
                try:
                    oldest_client.close()
                except Exception:
                    pass
                del self._sessions[oldest_key]
                self._logger.debug(f"Removed oldest SSH session {oldest_key}")

            try:
                ssh_client = paramiko.SSHClient()
                ssh_client.set_missing_host_key_policy(paramiko.AutoAddPolicy())
                ssh_client.connect(ip_addr, port, username, private_key, timeout=DEFAULT_SSH_TIMEOUT)
                self._sessions[session_key] = (ssh_client, time.time())
                return ssh_client
            except Exception as e:
                raise SSHConnectionError(f"Failed to establish SSH connection to {ip_addr}: {str(e)}")

    def close_session(self, ip_addr: str, port: int, username: str):
        session_key = f"{ip_addr}:{port}:{username}"
        with self._lock:
            if session_key in self._sessions:
                ssh_client, _ = self._sessions[session_key]
                try:
                    ssh_client.close()
                except Exception:
                    pass
                del self._sessions[session_key]
                self._logger.debug(f"Closed SSH session for {session_key}")

    def cleanup_expired_sessions(self) -> None:
        """Clean up expired SSH sessions"""
        current_time = time.time()
        expired_keys = []

        with self._lock:
            for session_key, (_, timestamp) in self._sessions.items():
                if current_time - timestamp >= self._session_timeout:
                    expired_keys.append(session_key)

            for key in expired_keys:
                ssh_client, _ = self._sessions[key]
                try:
                    ssh_client.close()
                except Exception:
                    pass
                del self._sessions[key]
                self._logger.debug(f"Cleaned up expired SSH session {key} at time: {current_time}")

    def close_all_sessions(self) -> None:
        """Close all SSH sessions"""
        with self._lock:
            for ssh_client, _ in self._sessions.values():
                try:
                    ssh_client.close()
                except Exception:
                    pass
            self._sessions.clear()
            self._logger.debug("Closed all SSH sessions")
