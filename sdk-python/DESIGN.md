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
9. [Migration Guide](#migration-guide)

## Overview

This document describes the architectural design for the Agentcube Python SDK refactoring that separates control plane (lifecycle management) from data plane (execution and interaction) operations.

### Version History
- **v1.0** (Current): Initial refactored architecture with Sandbox base class and CodeInterpreterClient

## Problem Statement

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
    
    def __init__(self, ttl: int, image: str, api_url: Optional[str]):
        super().__init__(ttl, image, api_url)
        # Initialize browser connection (e.g., Playwright, Selenium)
        self._browser = self._initialize_browser()
    
    def navigate(self, url: str) -> None:
        """Navigate to a URL"""
        pass
    
    def click(self, selector: str) -> None:
        """Click an element"""
        pass
    
    def type_text(self, selector: str, text: str) -> None:
        """Type text into an input field"""
        pass
    
    def screenshot(self, path: str) -> bytes:
        """Take a screenshot"""
        pass
    
    def get_page_content(self) -> str:
        """Get current page HTML"""
        pass
    
    def cleanup(self):
        """Close browser connections"""
        if self._browser:
            self._browser.close()
```

**Use Case**:
```python
with BrowserUseClient() as browser:
    browser.navigate("https://example.com")
    browser.click("#login-button")
    browser.type_text("#username", "user@example.com")
    screenshot = browser.screenshot("/tmp/page.png")
```

### 2. ComputerUseClient

**Purpose**: Provide desktop automation capabilities for AI agents

```python
class ComputerUseClient(Sandbox):
    """Desktop automation client"""
    
    def __init__(self, ttl: int, image: str, api_url: Optional[str]):
        super().__init__(ttl, image, api_url)
        # Initialize VNC or remote desktop connection
        self._desktop = self._initialize_desktop()
    
    def mouse_move(self, x: int, y: int) -> None:
        """Move mouse to coordinates"""
        pass
    
    def mouse_click(self, button: str = "left") -> None:
        """Click mouse button"""
        pass
    
    def keyboard_type(self, text: str) -> None:
        """Type text"""
        pass
    
    def keyboard_press(self, key: str) -> None:
        """Press a key"""
        pass
    
    def screen_capture(self) -> bytes:
        """Capture screen"""
        pass
    
    def get_window_list(self) -> List[str]:
        """Get list of open windows"""
        pass
    
    def cleanup(self):
        """Close desktop connections"""
        if self._desktop:
            self._desktop.close()
```

**Use Case**:
```python
with ComputerUseClient() as computer:
    computer.mouse_move(100, 200)
    computer.mouse_click()
    computer.keyboard_type("Hello World")
    screen = computer.screen_capture()
```

### 3. AgentHostClient

**Purpose**: Host and manage AI agents or MCP servers within sandboxes

```python
class AgentHostClient(Sandbox):
    """AI agent hosting client"""
    
    def __init__(self, ttl: int, image: str, api_url: Optional[str]):
        super().__init__(ttl, image, api_url)
        self._agent_process = None
    
    def deploy_agent(self, agent_config: Dict[str, Any]) -> str:
        """Deploy an AI agent"""
        pass
    
    def invoke_agent(self, input_data: Dict[str, Any]) -> Dict[str, Any]:
        """Invoke the agent with input"""
        pass
    
    def get_agent_logs(self) -> str:
        """Get agent execution logs"""
        pass
    
    def stop_agent(self) -> bool:
        """Stop the running agent"""
        pass
    
    def cleanup(self):
        """Stop agent and clean up"""
        if self._agent_process:
            self.stop_agent()
```

**Use Case**:
```python
with AgentHostClient() as host:
    agent_id = host.deploy_agent({
        "type": "chatbot",
        "model": "gpt-4",
        "config": {...}
    })
    
    response = host.invoke_agent({
        "prompt": "What is the weather?"
    })
    print(response)
```

### 4. MCPServerClient

**Purpose**: Host Model Context Protocol (MCP) servers

```python
class MCPServerClient(Sandbox):
    """MCP server hosting client"""
    
    def start_mcp_server(self, server_config: Dict[str, Any]) -> str:
        """Start an MCP server"""
        pass
    
    def list_tools(self) -> List[Dict[str, Any]]:
        """List available MCP tools"""
        pass
    
    def call_tool(self, tool_name: str, arguments: Dict) -> Any:
        """Call an MCP tool"""
        pass
    
    def get_server_info(self) -> Dict[str, Any]:
        """Get MCP server information"""
        pass
```

## Migration Guide

### For Existing Users

**Old Code**:
```python
from agentcube import Sandbox

sandbox = Sandbox()
sandbox.execute_command("echo hello")
sandbox.run_code("python", "print('hello')")
sandbox.stop()
```

**New Code**:
```python
from agentcube import CodeInterpreterClient

code_interpreter = CodeInterpreterClient()
code_interpreter.execute_command("echo hello")
code_interpreter.run_code("python", "print('hello')")
code_interpreter.stop()
```

### Migration Steps

1. **Update imports**: Change `Sandbox` to `CodeInterpreterClient` for code execution use cases
2. **Update variable names**: Use descriptive names like `code_interpreter` instead of `sandbox`
3. **Test thoroughly**: Verify all functionality works with the new class
4. **Use base Sandbox**: Only if you need lifecycle management without execution

### Breaking Changes

1. **Class Name**: The all-in-one `Sandbox` class is now split into `Sandbox` (base) and `CodeInterpreterClient` (with execution)
2. **Import Path**: Remains the same (`from agentcube import ...`)
3. **API Compatibility**: All methods remain the same; only class instantiation changes

### Deprecation Timeline

- **Current**: Both patterns supported
- **Future**: Original monolithic `Sandbox` will be deprecated in favor of explicit client types

## Design Principles

### 1. Single Responsibility Principle
Each class has one primary responsibility:
- `Sandbox`: Lifecycle management
- `CodeInterpreterClient`: Code execution
- Future clients: Their specific interaction pattern

### 2. Open/Closed Principle
- Base `Sandbox` class is open for extension (inheritance) but closed for modification
- New client types can be added without changing existing code

### 3. Liskov Substitution Principle
- All client classes can be used wherever `Sandbox` is expected
- Subclasses preserve the base class contract

### 4. Interface Segregation Principle
- Clients only expose methods relevant to their use case
- Base `Sandbox` doesn't force unused methods on subclasses

### 5. Dependency Inversion Principle
- High-level policy (lifecycle management) separated from low-level details (SSH, browser, desktop)
- Clients depend on abstractions (base class) not concrete implementations

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

## Security Considerations

### SSH Key Management
- Keys generated per session, never reused
- Private keys kept in memory, never persisted
- Keys destroyed on cleanup

### Sandbox Isolation
- Each sandbox is isolated in its own container
- No direct network access between sandboxes
- TTL ensures automatic cleanup

### Input Validation
- Validate command strings before execution
- Sanitize file paths
- Validate code snippet size limits

## Conclusion

This refactored architecture provides:
1. ✅ Clear separation between control and data planes
2. ✅ Extensibility for future use cases
3. ✅ Maintained backward compatibility
4. ✅ Type-safe interfaces
5. ✅ Proper resource management

The design enables the Agentcube SDK to support diverse scenarios beyond code interpretation, including browser automation, desktop control, and agent hosting, while maintaining a clean and maintainable codebase.
