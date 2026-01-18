# Copyright The Volcano Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""
AgentCube SDK Basic Usage Example

Demonstrates:
1. Basic command execution and code running
2. File operations
3. Session reuse for state persistence (useful for AI workflows)
"""

from agentcube import CodeInterpreterClient


def basic_operations():
    """Demonstrate basic SDK operations with context manager."""
    print("=== Basic Operations ===\n")
    
    with CodeInterpreterClient(verbose=True) as client:
        print(f"Session ID: {client.session_id}")
        
        # 1. Shell commands
        print("\n--- Shell Command: whoami ---")
        output = client.execute_command("whoami")
        print(f"Result: {output.strip()}")

        # 2. Python code execution
        print("\n--- Python Code ---")
        code = """
import math
print(f"Pi is approximately {math.pi:.6f}")
"""
        output = client.run_code("python", code)
        print(f"Result: {output.strip()}")

        # 3. File operations
        print("\n--- File Operations ---")
        client.write_file("Hello from AgentCube!", "hello.txt")
        files = client.list_files(".")
        print(f"Files: {[f['name'] for f in files]}")
    
    # Session automatically deleted on exit
    print("\nSession deleted.")


def session_reuse_example():
    """
    Demonstrate session reuse for AI workflows.
    
    This pattern is essential for low-code/no-code platforms (like Dify)
    where the interpreter is invoked multiple times as a tool within a
    single workflow, and state needs to persist across invocations.
    """
    print("\n=== Session Reuse (State Persistence) ===\n")
    
    # Step 1: Create session and set variable
    print("Step 1: Create session, set x = 42")
    client1 = CodeInterpreterClient(verbose=True)
    client1.run_code("python", "x = 42")
    session_id = client1.session_id
    print(f"Session ID saved: {session_id}")
    # Don't call stop() - let session persist
    
    # Step 2: Reuse session - variable x should still exist
    print("\nStep 2: Reuse session, access x")
    client2 = CodeInterpreterClient(session_id=session_id, verbose=True)
    result = client2.run_code("python", "print(f'x = {x}')")
    print(f"Result: {result.strip()}")  # Should print "x = 42"
    
    # Step 3: Cleanup
    print("\nStep 3: Delete session")
    client2.stop()
    print("Session deleted.")


if __name__ == "__main__":
    basic_operations()
    session_reuse_example()
