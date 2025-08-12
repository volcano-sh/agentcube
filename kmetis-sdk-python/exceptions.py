"""
Custom exceptions for the Kubernetes Sandbox SDK
"""


class SandboxError(Exception):
    """Base exception for all sandbox-related errors"""
    pass


class SandboxNotFoundError(SandboxError):
    """Raised when a sandbox is not found"""
    pass


class SSHConnectionError(SandboxError):
    """Raised when SSH connection fails"""
    pass


class SandboxCreationError(SandboxError):
    """Raised when sandbox creation fails"""
    pass


class SandboxDeletionError(SandboxError):
    """Raised when sandbox deletion fails"""
    pass


class CommandExecutionError(SandboxError):
    """Raised when command execution fails"""
    pass


class FileTransferError(SandboxError):
    """Raised when file transfer fails"""
    pass