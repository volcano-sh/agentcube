class SandboxError(Exception):
    """Base exception for all sandbox operations"""
    pass

class SandboxNotFoundError(SandboxError):
    """Raised when sandbox does not exist"""
    pass

class SandboxNotReadyError(SandboxError):
    """Raised when sandbox is not in 'running' state"""
    pass

class OperationTimeoutError(SandboxError):
    """Raised when operation exceeds timeout"""
    pass

class ProviderError(SandboxError):
    """Raised when agentcube-apiserver returns an error"""
    pass