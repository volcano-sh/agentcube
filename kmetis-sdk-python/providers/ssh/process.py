from typing import Optional

from constants import DEFAULT_SSH_USER
from models.sandbox_info import SandboxInstance, ExecutionResult
from services.exceptions import SSHExecutionError, FileTransferError
from services.log import get_logger


class SSHProcessManager:
    def __init__(self, ssh_client):
        self._ssh = ssh_client
        self._logger = get_logger(f"{__name__}.SSHProcessManager")

    def execute_command(
            self,
            sandbox: SandboxInstance,
            command: str,
            private_key: str = None,
            timeout: int = 30
    ) -> ExecutionResult:
        try:
            ssh_client = self._ssh.get_session(
                sandbox.ip_address,
                sandbox.port,
                DEFAULT_SSH_USER,
                private_key
            )
            self._logger.info(f"Execute command: {command} in sandbox {sandbox.id}")
            stdin, stdout, stderr = ssh_client.exec_command(command, timeout=timeout)
            return ExecutionResult(
                stdout=stdout.read().decode(),
                stderr=stderr.read().decode(),
                return_code=stdout.channel.recv_exit_status()
            )

        except Exception as e:
            raise SSHExecutionError(f"Command execution failed: {str(e)}")

    def upload_file(
            self,
            sandbox: SandboxInstance,
            private_key: str,
            local_path: str,
            remote_path: str,
            timeout: Optional[int] = 60
    ) -> bool:
        """
        Upload a file to a sandbox via SSH

        Args:
            sandbox: the sandbox
            private_key: SSH private key for authentication
            local_path: Local file path
            remote_path: Remote file path
            timeout: Transfer timeout in seconds (default: 60)

        Returns:
            True if upload was successful, False otherwise

        Raises:
            SandboxNotFoundError: If sandbox doesn't exist
            FileTransferError: If file transfer fails
        """
        try:
            # Get SSH session
            ssh_client = self._ssh.get_session(sandbox.ip_address, sandbox.port, sandbox.username, private_key)

            # Create SFTP client
            sftp = ssh_client.open_sftp()

            # Upload file
            sftp.put(local_path, remote_path)
            sftp.close()

            self._logger.info(f"Uploaded {local_path} to {sandbox.id}:{remote_path}")
            return True

        except Exception as e:
            raise FileTransferError(f"Failed to upload file to sandbox {sandbox.id}: {e}")

    def download_file(
            self,
            sandbox: SandboxInstance,
            private_key: str,
            remote_path: str,
            local_path: str,
            timeout: Optional[int] = 60
    ) -> bool:
        """
        Download a file from a sandbox via SSH

        Args:
            sandbox: the sandbox
            private_key: SSH private key for authentication
            remote_path: Remote file path
            local_path: Local file path
            timeout: Transfer timeout in seconds (default: 60)

        Returns:
            True if download was successful, False otherwise

        Raises:
            SandboxNotFoundError: If sandbox doesn't exist
            FileTransferError: If file transfer fails
        """

        try:
            # Get SSH session
            ssh_client = self._ssh.get_session(sandbox.ip_address, sandbox.port, sandbox.username, private_key)

            # Create SFTP client
            sftp = ssh_client.open_sftp()

            # Download file
            sftp.get(remote_path, local_path)
            sftp.close()

            self._logger.info(f"Downloaded {sandbox.id}:{remote_path} to {local_path}")
            return True

        except Exception as e:
            self._logger.error(f"Failed to download file from sandbox {sandbox.id}: {e}")
            raise FileTransferError(f"File download failed: {str(e)}")