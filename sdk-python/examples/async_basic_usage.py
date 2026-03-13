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
AgentCube SDK Async Basic Usage Example

Demonstrates:
1. Async basic command execution and code running
2. Async file operations
3. Session reuse for file-system state persistence (useful for AI workflows)
"""

import asyncio

from agentcube import AsyncCodeInterpreterClient


async def basic_operations():
    """Demonstrate basic async SDK operations with context manager."""
    print("=== Async Basic Operations ===\n")

    async with AsyncCodeInterpreterClient(verbose=True) as client:
        print(f"Session ID: {client.session_id}")

        # 1. Shell commands
        print("\n--- Shell Command: whoami ---")
        output = await client.execute_command("whoami")
        print(f"Result: {output.strip()}")

        # 2. Python code execution
        print("\n--- Python Code ---")
        code = """
import math
print(f"Pi is approximately {math.pi:.6f}")
"""
        output = await client.run_code("python", code)
        print(f"Result: {output.strip()}")

        # 3. File operations
        print("\n--- File Operations ---")
        await client.write_file("Hello from AgentCube!", "hello.txt")
        files = await client.list_files(".")
        print(f"Files: {[f['name'] for f in files]}")

    # Session automatically deleted on exit
    print("\nSession deleted.")


async def session_reuse_example():
    """
    Demonstrate async session reuse for AI workflows.

    File system state persists across sessions; Python variables do not.
    """
    print("\n=== Async Session Reuse (File State Persistence) ===\n")

    # Step 1: Create session and write a file
    print("Step 1: Create session, write value.txt = 42")
    client1 = await AsyncCodeInterpreterClient.create(verbose=True)
    await client1.write_file("42", "value.txt")
    session_id = client1.session_id
    print(f"Session ID saved: {session_id}")
    # Don't call stop() - let session persist

    # Step 2: Reuse session - file system state should still exist
    print("\nStep 2: Reuse session, read value.txt")
    client2 = await AsyncCodeInterpreterClient.create(
        session_id=session_id, verbose=True
    )
    result = await client2.run_code("python", "print(open('value.txt').read())")
    print(f"Result: {result.strip()}")  # Should print "42"

    # Step 3: Cleanup
    print("\nStep 3: Delete session")
    await client2.stop()
    print("Session deleted.")


async def main():
    await basic_operations()
    await session_reuse_example()


if __name__ == "__main__":
    asyncio.run(main())
