"""
Example demonstrating the refactored Sandbox SDK architecture

This example shows the separation of control plane (lifecycle management) 
and data plane (code execution, file operations) functionality.
"""
import logging

from agentcube import Sandbox, CodeInterpreterClient

logging.basicConfig(level=logging.INFO, format='%(message)s')
log = logging.getLogger(__name__)


def example_base_sandbox():
    """
    Example 1: Using the base Sandbox class for lifecycle management only
    
    The base Sandbox class provides control plane operations:
    - Creating sandboxes
    - Checking status
    - Getting info
    - Listing sandboxes
    - Stopping/deleting sandboxes
    
    This is useful when you need lifecycle management but don't need
    to execute code or transfer files.
    """
    log.info("\n" + "=" * 60)
    log.info("Example 1: Base Sandbox (Lifecycle Management Only)")
    log.info("=" * 60 + "\n")
    
    try:
        # Create a sandbox for lifecycle management only
        log.info("Creating sandbox with base Sandbox class...")
        sandbox = Sandbox(ttl=3600, image="python:3.9")
        log.info(f"✅ Sandbox created with ID: {sandbox.id}")
        
        # Check if sandbox is running
        log.info("\nChecking sandbox status...")
        if sandbox.is_running():
            log.info("✅ Sandbox is running")
        
        # Get detailed info
        log.info("\nRetrieving sandbox info...")
        info = sandbox.get_info()
        log.info(f"   Sandbox status: {info.get('status')}")
        
        # List all sandboxes
        log.info("\nListing all sandboxes...")
        sandboxes = sandbox.list_sandboxes()
        log.info(f"   Total sandboxes: {len(sandboxes)}")
        
        # Stop sandbox
        log.info("\nStopping sandbox...")
        if sandbox.stop():
            log.info("✅ Sandbox stopped successfully")
            
    except Exception as e:
        log.error(f"❌ Error: {e}")


def example_code_interpreter():
    """
    Example 2: Using CodeInterpreterClient for code execution
    
    The CodeInterpreterClient inherits from Sandbox and adds data plane operations:
    - All lifecycle methods from Sandbox
    - execute_command() - Run shell commands
    - run_code() - Execute code snippets
    - write_file() - Upload file content
    - upload_file() - Upload local files
    - download_file() - Download files
    
    This is the complete solution for code interpretation scenarios.
    """
    log.info("\n" + "=" * 60)
    log.info("Example 2: CodeInterpreterClient (Full Functionality)")
    log.info("=" * 60 + "\n")
    
    try:
        # Create a code interpreter sandbox
        log.info("Creating sandbox with CodeInterpreterClient...")
        code_interpreter = CodeInterpreterClient(ttl=3600, image="python:3.9")
        log.info(f"✅ Sandbox created with ID: {code_interpreter.id}")
        
        # Lifecycle operations (inherited from Sandbox)
        log.info("\n--- Lifecycle Operations (from Sandbox) ---")
        if code_interpreter.is_running():
            log.info("✅ Sandbox is running")
        
        # Data plane operations (specific to CodeInterpreterClient)
        log.info("\n--- Data Plane Operations (CodeInterpreterClient) ---")
        
        # Execute a command
        log.info("\n1. Executing shell command...")
        output = code_interpreter.execute_command("echo 'Hello from sandbox!'")
        log.info(f"   Output: {output.strip()}")
        
        # Run Python code
        log.info("\n2. Running Python code...")
        python_code = """
print("Python version:")
import sys
print(sys.version)
print("\\nCalculating fibonacci (iterative)...")
def fib(n):
    if n <= 1:
        return n
    # Initialize first two fibonacci numbers
    a, b = 0, 1
    # Generate fibonacci sequence iteratively
    for _ in range(2, n + 1):
        a, b = b, a + b
    return b
print(f"Fibonacci(10) = {fib(10)}")
"""
        output = code_interpreter.run_code(language="python", code=python_code)
        log.info(f"   Output: {output.strip()}")
        
        # Write file
        log.info("\n3. Writing file to sandbox...")
        code_interpreter.write_file(
            content="Hello from SDK!",
            remote_path="/workspace/test.txt"
        )
        log.info("   ✅ File written")
        
        # Verify file was written
        log.info("\n4. Verifying file content...")
        output = code_interpreter.execute_command("cat /workspace/test.txt")
        log.info(f"   File content: {output.strip()}")
        
        # Stop sandbox
        log.info("\nStopping sandbox...")
        if code_interpreter.stop():
            log.info("✅ Sandbox stopped successfully")
            
    except Exception as e:
        log.error(f"❌ Error: {e}")


def example_comparison():
    """
    Example 3: Demonstrating the architectural separation
    
    This shows why the refactoring is beneficial:
    - Base Sandbox: lightweight, for lifecycle management only
    - CodeInterpreterClient: full-featured, for code execution scenarios
    
    Future scenarios like BrowserUse, ComputerUse can extend Sandbox
    with their own specific data plane interfaces.
    """
    log.info("\n" + "=" * 60)
    log.info("Example 3: Architecture Comparison")
    log.info("=" * 60 + "\n")
    
    log.info("Base Sandbox class (Control Plane):")
    log.info("  - create sandbox")
    log.info("  - is_running()")
    log.info("  - get_info()")
    log.info("  - list_sandboxes()")
    log.info("  - stop()")
    log.info("  - cleanup()")
    
    log.info("\nCodeInterpreterClient (Control + Data Plane):")
    log.info("  [Inherits all from Sandbox, plus:]")
    log.info("  - execute_command()")
    log.info("  - execute_commands()")
    log.info("  - run_code()")
    log.info("  - write_file()")
    log.info("  - upload_file()")
    log.info("  - download_file()")
    
    log.info("\nFuture Extensions (examples):")
    log.info("  - BrowserUseClient(Sandbox):")
    log.info("      navigate(), click(), type(), screenshot(), etc.")
    log.info("  - ComputerUseClient(Sandbox):")
    log.info("      mouse_move(), keyboard_input(), screen_capture(), etc.")
    log.info("  - AgentHostClient(Sandbox):")
    log.info("      deploy_agent(), invoke_agent(), get_logs(), etc.")


if __name__ == "__main__":
    log.info("\n" + "=" * 60)
    log.info("Agentcube SDK Refactoring Examples")
    log.info("=" * 60)
    
    # Note: These examples show the API structure
    # They won't actually connect to a server without proper configuration
    log.info("\nNote: These examples demonstrate the refactored architecture.")
    log.info("To run with actual sandboxes, set the API_URL environment variable.")
    
    # Show architecture comparison
    example_comparison()
    
    log.info("\n" + "=" * 60)
    log.info("Examples Complete")
    log.info("=" * 60 + "\n")
