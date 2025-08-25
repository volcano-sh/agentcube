class SandboxError(Exception):
    """Base exception class for all sandbox errors"""
    def __init__(self, message: str, context: dict = None):
        super().__init__(message)
        self.context = context or {}
        self.code = getattr(self, 'code', 500)

class ConfigurationError(SandboxError):
    """Invalid configuration"""
    code = 400

class ProviderError(SandboxError):
    """Provider-specific errors"""
    code = 502

class OperationTimeoutError(SandboxError):
    """Operation timeout"""
    code = 504

class ResourceError(SandboxError):
    """Resource management failures"""
    code = 500

class SSHConnectionError(ProviderError):
    """Base class for SSH connection errors"""

class SSHExecutionError(SSHConnectionError):
    """SSH command execution failure"""

class FileTransferError(SSHConnectionError):
    """File transfer operation failed"""