import os
import shlex
import socket
from typing import Dict, List, Optional, Tuple
import paramiko

import agentcube.clients.constants as constants
import agentcube.utils.exceptions as exceptions

class SandboxSSHClient :
    def __init__(
        self,
        private_key: paramiko.RSAKey,
        tunnel_sock: socket.socket
    ):
        self._private_key=private_key
        self._tunnel_sock=tunnel_sock
        self._ssh_client=self.connect_ssh()
    
    def connect_ssh(
        self,

    ) -> paramiko.SSHClient:
        """Establish an SSH connection over the provided tunnel using the given private key
        
        Args:
            conn: Established tunnel socket connection
            private_key: RSA private key for authentication
            
        Returns:
            Established SSH client connection
        """
        ssh_client = paramiko.SSHClient()
        ssh_client.set_missing_host_key_policy(paramiko.AutoAddPolicy())

        try:
            ssh_client.connect(
                hostname=constants.DEFAULT_HOSTNAME,
                username=constants.DEFAULT_USER,
                pkey=self._private_key,
                sock=self._tunnel_sock,
                timeout=constants.DEFAULT_TIMEOUT,        
                banner_timeout=constants.DEFAULT_BANNER_TIMEOUT
            )
            return ssh_client
        except paramiko.SSHException as e:
            raise paramiko.SSHException(f"SSH handshake failed: {str(e)}") from e
    
    def execute_command(self, command: str, timeout: float = 30) -> str:
        """Execute a command over SSH
        
        Args:
            command: Command to execute
            ssh_client: SSH client instance (uses current connection if not provided)
            timeout: Command execution timeout in seconds
        Returns:
            Command output
        """
    
        if self._ssh_client is None:
            self._ssh_client = self.connect_ssh()

        ssh_client = self._ssh_client 
        if not ssh_client:
            raise ConnectionError("No SSH connection established. Please call connect_ssh first.")
        
        stdin, stdout, stderr = ssh_client.exec_command(command, timeout)
        exit_status = stdout.channel.recv_exit_status()
        
        output = stdout.read().decode().strip()
        error = stderr.read().decode().strip()
        
        if exit_status != 0:
            raise Exception(f"Command execution failed (exit code {exit_status}): {error}")
        
        return output
    
    def execute_commands(self, commands: List[str]) -> Dict[str, str]:
        """Execute multiple commands over SSH
        
        Args:
            commands: List of commands to execute
            
        Returns:
            Dictionary mapping commands to their outputs
        """
        results = {}
        for cmd in commands:
            results[cmd] = self.execute_command(cmd)
        return results
    
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
            Tuple of (stdout, stderr) from code execution. 
            If execution fails, stdout is None and stderr contains error info.
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
            raise ValueError(f"Unsupported language: {language}. Supported: {list(lang_aliases.keys())}")

        quoted_code = shlex.quote(code)
        if target_lang == "python":
            command = f"python3 -c {quoted_code}"
        elif target_lang == "bash":
            command = f"bash -c {quoted_code}"

        return self.execute_command(command, timeout)

    def write_file(
        self,
        content: str,
        remote_path: str
    ) -> None:
        """Upload file content to remote server via SFTP
        
        Args:
            content: Content to write to remote file
            remote_path: Path on remote server to upload to
            ssh_client: SSH client instance (uses current connection if not provided)
        """
        
        ssh_client = self._ssh_client or self.connect_ssh()
        if not ssh_client:
            raise Exception("No SSH connection established. Please call connect_ssh first.")
        
        sftp = ssh_client.open_sftp()
        try:
            # Create remote directory if needed
            remote_dir = os.path.dirname(remote_path)
            self._sftp_mkdir_p(sftp, remote_dir)
            
            # Write file content
            with sftp.file(remote_path, 'w') as remote_file:
                remote_file.write(content)
        finally:
            sftp.close()
    
    def upload_file(
        self,
        local_path: str,
        remote_path: str
    ) -> None:
        """Upload a file to remote server via SFTP
        
        Args:
            local_path: Path to local file to upload
            remote_path: Path on remote server to upload to
        """

        ssh_client = self._ssh_client or self.connect_ssh()
        if not ssh_client:
            raise Exception("No SSH connection established. Please call connect_ssh first.")

        sftp = ssh_client.open_sftp()
        try:
            remote_dir = os.path.dirname(remote_path)
            self._sftp_mkdir_p(sftp, remote_dir)

            if not os.path.exists(local_path):
                raise FileNotFoundError(f"Local file not found: {local_path}")
            sftp.put(local_path, remote_path)
        finally:
            sftp.close()

    def download_file(
        self,
        remote_path: str,
        local_path: str
    ) -> None:
        """Download a file from remote server via SFTP
        
        Args:
            remote_path: Path on remote server to download from
            local_path: Local path to save the downloaded file
            ssh_client: SSH client instance (uses current connection if not provided)
        """

        ssh_client = self._ssh_client or self.connect_ssh()
        if not ssh_client:
            raise Exception("No SSH connection established. Please call connect_ssh first.")
        
        sftp = ssh_client.open_sftp()
        try:
            # Ensure local directory exists
            local_dir = os.path.dirname(local_path)
            os.makedirs(local_dir, exist_ok=True)
            
            # Download file
            sftp.get(remote_path, local_path)
        finally:
            sftp.close()
    
    def cleanup(self) -> None:
        """Clean up all resources (SSH connections and tunnels)"""
        if self._ssh_client:
            self._ssh_client.close()
            self._ssh_client = None
        if self._tunnel_sock:
            self._tunnel_sock.close()
            self._tunnel_sock = None
    
    @staticmethod
    def _sftp_mkdir_p(sftp: paramiko.SFTPClient, remote_dir: str) -> None:
        """Recursively create remote directories (similar to mkdir -p)
        
        Args:
            sftp: SFTP client instance
            remote_dir: Remote directory path to create
        """
        dirs = remote_dir.split('/')
        current_dir = ''
        
        for dir in dirs:
            if not dir:
                current_dir += '/'
                continue
                
            current_dir = os.path.join(current_dir, dir)
            try:
                sftp.stat(current_dir)
            except FileNotFoundError:
                sftp.mkdir(current_dir)
    
    @staticmethod
    def generate_ssh_key_pair() -> Tuple[str, paramiko.RSAKey]:
        private_key = paramiko.RSAKey.generate(2048)
        public_key = f"{private_key.get_name()} {private_key.get_base64()}"
        return public_key, private_key