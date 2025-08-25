from dataclasses import dataclass
from enum import Enum
from typing import Optional


class PodState(Enum):
    """Immutable state enum with transition validation"""
    RUNNING = "Running"
    PENDING = "Pending"
    FAILED = "Failed"
    UNKNOWN = "Unknown"

@dataclass
class SandboxInstance:
    id: str
    ip_address: str
    port: int
    username: str
    status: Optional[str] = None
    metadata: Optional[dict] = None


@dataclass
class ExecutionResult:
    stdout: str
    stderr: str
    return_code: int
