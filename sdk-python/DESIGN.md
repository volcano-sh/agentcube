# Agentcube SDK Architecture Design Proposal

## Table of Contents
1. [Overview](#overview)
2. [Problem Statement](#problem-statement)
3. [Design Goals](#design-goals)
4. [Architecture](#architecture)
5. [Component Design](#component-design)
6. [Implementation Details](#implementation-details)
7. [Usage Examples](#usage-examples)
8. [Future Extensions](#future-extensions)

## Overview

This document describes the architectural design for the Agentcube Python SDK refactoring that separates control plane (lifecycle management) from data plane (execution and interaction) operations.

### Version History
- **v0.1** (Current): Initial refactored architecture with Sandbox base class and CodeInterpreterClient

## BackGround

### Original Design Issues

The original `Sandbox` class combined two distinct responsibilities:

1. **Control Plane Operations**: Sandbox lifecycle management (create, delete, status checking)
2. **Data Plane Operations**: Code execution, command execution, file management

```python
# Original monolithic design
class Sandbox:
    # Control plane
    def __init__(self, ttl, image): ...
    def is_running(self): ...
    def stop(self): ...
    
    # Data plane (tightly coupled to SSH/code execution)
    def execute_command(self, cmd): ...
    def run_code(self, language, code): ...
    def upload_file(self, local, remote): ...
```

### Limitations

1. **Lack of Extensibility**: Cannot support different interaction patterns (browser automation, computer use, agent hosting)
2. **Tight Coupling**: Data plane implementation (SSH) is hardcoded into the base class
3. **Single Use Case**: Designed only for code interpreter scenarios
4. **Poor Separation of Concerns**: Mixing lifecycle and execution logic

## Design Goals

### Primary Goals

1. **Separation of Concerns**: Clearly separate control plane from data plane operations
2. **Extensibility**: Enable easy addition of new client types for different use cases
3. **Backward Compatibility**: Maintain existing functionality while improving architecture
4. **Type Safety**: Provide clear interfaces for different client types

### Non-Goals

1. Changing the underlying server API
2. Modifying existing authentication mechanisms
3. Adding new features beyond architectural refactoring

## Architecture

### High-Level Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                    Agentcube SDK                            │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  ┌────────────────────────────────────────────────────┐   │
│  │              Sandbox (Base Class)                  │   │
│  │           Control Plane Operations                  │   │
│  ├────────────────────────────────────────────────────┤   │
│  │ • create/delete sandbox                            │   │
│  │ • is_running()                                     │   │
│  │ • get_info()                                       │   │
│  │ • list_sandboxes()                                 │   │
│  │ • stop()                                           │   │
│  │ • cleanup() [extensible hook]                      │   │
│  └────────────────────────────────────────────────────┘   │
│                          ▲                                  │
│                          │                                  │
│          ┌───────────────┼───────────────┐                │
│          │               │               │                 │
│  ┌───────▼──────┐ ┌─────▼──────┐ ┌─────▼──────────┐     │
│  │CodeInterpreter│ │BrowserUse  │ │ComputerUse     │     │
│  │Client         │ │Client      │ │Client          │     │
│  ├───────────────┤ ├────────────┤ ├────────────────┤     │
│  │Data Plane:    │ │Data Plane: │ │Data Plane:     │     │
│  │• execute_cmd  │ │• navigate  │ │• mouse_move    │     │
│  │• run_code     │ │• click     │ │• keyboard_input│     │
│  │• upload_file  │ │• screenshot│ │• screen_capture│     │
│  │• download_file│ │• type      │ │• get_desktop   │     │
│  └───────────────┘ └────────────┘ └────────────────┘     │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

### Design Patterns

1. **Template Method Pattern**: Base `Sandbox` class defines lifecycle operations, subclasses implement specific data plane operations
2. **Strategy Pattern**: Different client types (CodeInterpreter, BrowserUse, etc.) provide different interaction strategies
3. **Context Manager Pattern**: Both base and derived classes support Python's `with` statement for resource management

## Component Design

### 1. Sandbox Base Class

**Purpose**: Provides sandbox lifecycle management (control plane)

**Responsibilities**:
- Create and initialize sandbox instances
- Monitor sandbox status
- Manage sandbox lifecycle (stop, cleanup)
- Provide context manager support

**Key Methods**:
```python
class Sandbox:
    def __init__(self, ttl: int, image: str, api_url: Optional[str], 
                 ssh_public_key: Optional[str])
    def __enter__(self) -> 'Sandbox'
    def __exit__(self, exc_type, exc_val, exc_tb) -> None
    def is_running(self) -> bool
    def get_info(self) -> Dict[str, Any]
    def list_sandboxes(self) -> List[Dict[str, Any]]
    def stop(self) -> bool
    def cleanup(self) -> None  # Hook for subclasses
```

**Design Decisions**:
- Accept `ssh_public_key` as optional parameter for extensibility
- Provide `cleanup()` as an extensible hook for subclass-specific cleanup
- Use type hints for better IDE support and type safety
- Implement context manager protocol for automatic resource cleanup

### 2. CodeInterpreterClient Class

**Purpose**: Extends Sandbox with SSH-based code execution capabilities

**Responsibilities**:
- Establish SSH connection for secure command execution
- Execute shell commands and code snippets
- Manage file uploads and downloads
- Clean up SSH connections

**Key Methods**:
```python
class CodeInterpreterClient(Sandbox):
    def __init__(self, ttl: int, image: str, api_url: Optional[str])
    def execute_command(self, command: str) -> str
    def execute_commands(self, commands: List[str]) -> Dict[str, str]
    def run_code(self, language: str, code: str, timeout: float) -> str
    def write_file(self, content: str, remote_path: str) -> None
    def upload_file(self, local_path: str, remote_path: str) -> None
    def download_file(self, remote_path: str, local_path: str) -> str
    def cleanup(self) -> None  # Override to clean up SSH resources
```

**Design Decisions**:
- Use `super().__init__()` to properly initialize the base class
- Generate SSH key pair internally to maintain encapsulation
- Override `cleanup()` to handle SSH-specific resource cleanup
- Validate sandbox is running before executing data plane operations

### 3. Module Structure

```
agentcube/
├── __init__.py                 # Exports Sandbox, CodeInterpreterClient
├── sandbox.py                  # Base Sandbox class, SandboxStatus enum
├── code_interpreter.py         # CodeInterpreterClient implementation
├── clients/
│   ├── __init__.py
│   ├── client.py              # SandboxClient (HTTP API client)
│   ├── ssh_client.py          # SandboxSSHClient (SSH operations)
│   └── constants.py           # Configuration constants
└── utils/
    ├── exceptions.py          # Custom exceptions
    └── utils.py               # Utility functions
```

## Implementation Details

### 1. Initialization Flow

```
CodeInterpreterClient.__init__()
  │
  ├─> Generate SSH key pair
  │
  ├─> super().__init__(ssh_public_key=public_key)
  │     │
  │     ├─> Create SandboxClient
  │     └─> Call API to create sandbox with SSH key
  │
  └─> Establish SSH tunnel and connection
```

### 2. State Management

**Sandbox States**:
- `PENDING`: Sandbox is being created
- `RUNNING`: Sandbox is active and ready
- `FAILED`: Sandbox creation or operation failed
- `UNKNOWN`: Sandbox state cannot be determined

**State Transitions**:
```
PENDING ──> RUNNING ──> (operations) ──> STOPPED
   │           │
   └──> FAILED │
               └──> FAILED
```

### 3. Error Handling

**Custom Exceptions**:
- `SandboxNotFoundError`: Sandbox doesn't exist
- `SandboxNotReadyError`: Sandbox not in RUNNING state
- Connection/timeout errors from underlying clients

**Error Handling Strategy**:
```python
# Check state before data plane operations
if not self.is_running():
    raise SandboxNotReadyError(f"Sandbox {self.id} is not running")
```

### 4. Resource Cleanup

**Cleanup Hierarchy**:
```python
# Base class cleanup (can be overridden)
def cleanup(self):
    pass

# CodeInterpreterClient cleanup (overrides base)
def cleanup(self):
    if self._executor is not None:
        self._executor.cleanup()  # Close SSH connections

# Context manager ensures cleanup
with CodeInterpreterClient() as client:
    # ... operations ...
# cleanup() called automatically on exit
```

## Usage Examples

### Example 1: Code Interpreter (Most Common)

```python
from agentcube import CodeInterpreterClient

# Using context manager (recommended)
with CodeInterpreterClient() as client:
    # Execute shell commands
    output = client.execute_command("ls -la /workspace")
    print(output)
    
    # Run Python code
    result = client.run_code(
        language="python",
        code="print('Hello'); import sys; print(sys.version)"
    )
    print(result)
    
    # Upload and execute a script
    client.write_file("print('Hello World!')", "/workspace/script.py")
    output = client.execute_command("python /workspace/script.py")
    print(output)
```

### Example 2: Lifecycle Management Only

```python
from agentcube import Sandbox

# Create sandbox for lifecycle management
sandbox = Sandbox(ttl=3600, image="python:3.9")

try:
    # Monitor sandbox
    if sandbox.is_running():
        info = sandbox.get_info()
        print(f"Sandbox {sandbox.id} is {info['status']}")
    
    # List all sandboxes
    all_sandboxes = sandbox.list_sandboxes()
    print(f"Active sandboxes: {len(all_sandboxes)}")
    
finally:
    sandbox.stop()
```

### Example 3: Multiple Commands

```python
from agentcube import CodeInterpreterClient

with CodeInterpreterClient() as client:
    # Execute multiple commands
    commands = [
        "pwd",
        "whoami",
        "python --version"
    ]
    results = client.execute_commands(commands)
    
    for cmd, output in results.items():
        print(f"{cmd}: {output}")
```

## Future Extensions

### 1. BrowserUseClient

**Purpose**: Automate browser interactions for web scraping, testing, or AI agents

```python
class BrowserUseClient(Sandbox):
    """Browser automation client"""
    ...
```

### 2. ComputerUseClient

**Purpose**: Provide desktop automation capabilities for AI agents

```python
class ComputerUseClient(Sandbox):
    """Desktop automation client"""
    ...
```

### 3. AgentHostClient

**Purpose**: Host and manage AI agents or MCP servers within sandboxes

```python
class AgentHostClient(Sandbox):
    """AI agent hosting client"""
    ...
```

**Use Case**:
```python
with AgentHostClient() as host:
    agent_id = host.deploy_agent({
        "type": "chatbot",
        "model": "gpt-4",
        "config": {...}
    })
    
    response = host.invoke({
        "prompt": "What is the weather?"
    })
    print(response)
```

### 4. MCPServerClient

**Purpose**: Host Model Context Protocol (MCP) servers

```python
class MCPServerClient(Sandbox):
    """MCP server hosting client"""
    
    ...
```

## Testing Strategy

### Unit Tests
- Test base `Sandbox` class independently
- Test each client class with mocked dependencies
- Test inheritance and method overriding

### Integration Tests
- Test with actual sandbox API (if available)
- Test SSH connections for `CodeInterpreterClient`
- Test resource cleanup

### Example Test Structure
```python
class TestSandboxBase(unittest.TestCase):
    """Test base Sandbox functionality"""
    
    def test_sandbox_initialization(self): ...
    def test_is_running(self): ...
    def test_stop(self): ...

class TestCodeInterpreterClient(unittest.TestCase):
    """Test CodeInterpreterClient functionality"""
    
    def test_execute_command(self): ...
    def test_run_code(self): ...
    def test_file_operations(self): ...
    def test_cleanup(self): ...
```

## Performance Considerations

### Resource Management
- SSH connections are reused within a session
- Proper cleanup prevents connection leaks
- Context managers ensure cleanup even on exceptions

### Connection Pooling
Future enhancement: Connection pool for multiple sandboxes

### Timeout Handling
- Configure timeouts for long-running operations
- Default timeout of 30 seconds for code execution
- Configurable per operation

## Conclusion

This refactored architecture provides:
1. ✅ Clear separation between control and data planes
2. ✅ Extensibility for future use cases
3. ✅ Maintained backward compatibility
4. ✅ Type-safe interfaces
5. ✅ Proper resource management

The design enables the Agentcube SDK to support diverse scenarios beyond code interpretation, including browser automation, desktop control, and agent hosting, while maintaining a clean and maintainable codebase.
