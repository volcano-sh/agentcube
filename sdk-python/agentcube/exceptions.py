class AgentCubeError(Exception):
    """Base exception for AgentCube SDK"""
    pass

class CommandExecutionError(AgentCubeError):
    """Raised when a command execution fails (exit code != 0)"""
    def __init__(self, exit_code, stderr, command=None):
        self.exit_code = exit_code
        self.stderr = stderr
        self.command = command
        super().__init__(f"Command failed (exit {exit_code}): {stderr}")

class SessionError(AgentCubeError):
    """Raised when session creation or management fails"""
    pass

class DataPlaneError(AgentCubeError):
    """Raised when Data Plane operations fail"""
    pass
