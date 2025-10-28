import threading
from contextlib import AbstractContextManager
from typing import Optional, Dict, Any

from models.sandbox_info import SandboxInstance, ExecutionResult
from providers.kubernetes.client import KubernetesClient
from providers.kubernetes.lifecycle import LifecycleManager
from providers.ssh.client import SSHClient
from providers.ssh.process import SSHProcessManager
from services import exceptions
from services.log import get_logger


class SandboxSDK(AbstractContextManager):
    """Thread-safe context manager for sandbox lifecycle"""
    def __init__(self):
        """Initialize with optional dependency injection"""
        self._logger = get_logger(f"{__name__}.SandboxSDK")
        self.k8s = LifecycleManager(KubernetesClient())
        self.ssh = SSHProcessManager(SSHClient())
        self._active_sandboxes: Dict[str, SandboxInstance] = {}
        self._lock = threading.RLock()

    def __enter__(self):
        return self

    def __exit__(self, exc_type, exc_val, exc_tb):
        """Ensure safe shutdown"""
        with self._lock:
            for sandbox in self._active_sandboxes.values():
                try:
                    self.delete_sandbox(sandbox)
                except Exception as e:
                    self._logger.error(f"Failed to cleanup sandbox {sandbox.id}", {
                        "error": str(e),
                        "sandbox": sandbox.id
                    })
        return False  # Don't suppress exceptions

    def create_sandbox(self, sandbox_id: str, sandbox_config: Optional[Dict[str, Any]] = None) -> SandboxInstance:
        """Atomic sandbox creation with state tracking"""
        try:
            with self._lock:
                if sandbox_id in self._active_sandboxes:
                    self._logger.info(f"Sandbox {sandbox_id} already exists", {
                        "sandbox_id": sandbox_id,
                    })
                    return self._active_sandboxes[sandbox_id]
                sandbox = self.k8s.create(sandbox_id=sandbox_id, sandbox_config=sandbox_config)
                self._active_sandboxes[sandbox.id] = sandbox
                self._logger.info(f"Created sandbox {sandbox.id}", {
                    "sandbox_id": sandbox.id,
                })
                return sandbox
        except Exception as e:
            raise exceptions.SandboxError(f"Failed to create sandbox: {str(e)}")


    def execute_command(self, sandbox: SandboxInstance, command: str, private_key: str, timeout: int = 30) -> ExecutionResult:
        """Safe command execution with validation"""
        with self._lock:
            if sandbox is None or sandbox.id not in self._active_sandboxes:
                raise exceptions.SandboxError("Sandbox not found", {"sandbox_id": sandbox.id})

            sandbox = self._active_sandboxes[sandbox.id]
            if not sandbox:
                raise exceptions.SandboxError("Sandbox is not running", {"sandbox_id": sandbox.id})

            self._logger.debug(f"Executing command on {sandbox.id}", {
                "command": command,
                "timeout": timeout
            })

            return self.ssh.execute_command(sandbox, command, private_key, timeout)

    def delete_sandbox(self, sandbox: SandboxInstance) -> bool:
        """Safe deletion with state validation"""
        with self._lock:
            if sandbox.id not in self._active_sandboxes:
                self._logger.warning(f"Attempt to delete unknown sandbox {sandbox.id}")
                return False

            return self.k8s.delete(self._active_sandboxes.pop(sandbox.id))


    def upload_file(self, sandbox: SandboxInstance, private_key: str, local_file_path: str, remote_file_path: str) -> bool:
        """Upload a file to sandbox"""
        return self.ssh.upload_file(sandbox, private_key, local_file_path, remote_file_path)

    def download_file(self, sandbox: SandboxInstance, private_key: str, local_file_path: str, remote_file_path: str) -> bool:
        """Download a file from sandbox"""
        return self.ssh.download_file(sandbox, private_key, remote_file_path, local_file_path)