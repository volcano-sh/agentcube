#!/usr/bin/env python3
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

import os
import sys
import json
import unittest

# Import agentcube package (Installed in the virtual environment by run_e2e.sh)
from agentcube import CodeInterpreterClient
from agentcube.exceptions import CommandExecutionError

class TestCodeInterpreterE2E(unittest.TestCase):
    """E2E tests for CodeInterpreter functionality using Python SDK."""

    def setUp(self):
        """Set up test environment."""
        self.namespace = os.getenv("AGENTCUBE_NAMESPACE", "agentcube")
        self.workload_manager_url = os.getenv("WORKLOAD_MANAGER_ADDR")
        self.router_url = os.getenv("ROUTER_URL")
        self.api_token = os.getenv("API_TOKEN")

        if not self.workload_manager_url:
            self.fail("WORKLOAD_MANAGER_ADDR environment variable not set")

        if not self.router_url:
            self.fail("ROUTER_URL environment variable not set")

        print(
            f"Test environment: namespace={self.namespace}, "
            f"workload_manager={self.workload_manager_url}, router={self.router_url}"
        )

    def test_case1_simple_code_execution_auto_session(self):
        """
        Case1. Simple code execution with session auto-creation

        Precondition: CodeInterpreter CR (e.g. default/e2e-code-interpreter) deployed.
        POST /v1/namespaces/{ns}/code-interpreters/{name}/invocations/run without x-agentcube-session-id.
        Body: simple code snippet (e.g. print(1+1)).

        Assert:
        - HTTP 200.
        - Output (e.g. "stdout":"2\n").
        - Response header x-agentcube-session-id is present.
        """
        # Test simple Python code execution
        code = "print(1+1)"

        with CodeInterpreterClient(
            name="e2e-code-interpreter",
            namespace=self.namespace,
            workload_manager_url=self.workload_manager_url,
            router_url=self.router_url,
            auth_token=self.api_token,
            verbose=True
        ) as client:
            print(f"Session created: {client.session_id}")
            self.assertIsNotNone(client.session_id, "Session ID should be created")

            # Execute simple code
            result = client.run_code("python", code)
            print(f"Code execution result: {repr(result)}")

            # Assert: Should contain "2\n" in output
            self.assertIn("2", result.strip(), f"Expected '2' in output, got: {result}")

    def test_case2_code_execution_in_session(self):
        """
        Case2. Code execution within a session (stateless expectation)

        - Reuse one session for multiple calls.
        - CodeInterpreter runs each call in an isolated process; variables are not preserved.
        - We assert that a second call depending on a previous variable fails with NameError.
        """
        with CodeInterpreterClient(
            name="e2e-code-interpreter",
            namespace=self.namespace,
            workload_manager_url=self.workload_manager_url,
            router_url=self.router_url,
            auth_token=self.api_token,
            verbose=True,
        ) as client:
            print(f"Session created: {client.session_id}")
            self.assertIsNotNone(client.session_id, "Session ID should be created")

            # First execution: define a variable (should succeed)
            result1 = client.run_code("python", "x = 10\nprint('defined')")
            print(f"Define variable result: {repr(result1)}")
            self.assertIn("defined", result1.strip(), f"Expected 'defined' in output, got: {result1}")

            # Second execution: try to use the previous variable; expect failure due to statelessness
            with self.assertRaises(CommandExecutionError) as ctx:
                client.run_code("python", "print(x)")
            stderr = ctx.exception.stderr or ""
            print(f"Stateless assertion stderr: {stderr!r}")
            self.assertTrue(
                "NameError" in stderr or "name 'x' is not defined" in stderr,
                f"Expected NameError due to stateless execution, got stderr: {stderr}",
            )

    def test_case3_file_based_workflow_fibonacci_json(self):
        """
        Case3. File-based workflow via CodeInterpreter (Fibonacci JSON)

        - Use CodeInterpreter API to:
        - Upload a small Python script (fibonacci.py) into the picod working directory (default: /root).
        - Execute it to generate output.json (stored in /root).
        - Retrieve the JSON result.
        - Assert:
        - retrieve the result to check.
        """
        try:
            # Create fibonacci.py script content
            fibonacci_script = '''
import json

def fibonacci(n):
    if n <= 1:
        return n
    return fibonacci(n-1) + fibonacci(n-2)

# Generate fibonacci sequence
fib_sequence = [fibonacci(i) for i in range(10)]
result = {
    "fibonacci_sequence": fib_sequence,
    "length": len(fib_sequence)
}

# Write to output.json
with open("output.json", "w") as f:
    json.dump(result, f, indent=2)

print("Fibonacci sequence generated and saved to output.json")
'''

            # Expected result
            expected_fib = [0, 1, 1, 2, 3, 5, 8, 13, 21, 34]

            with CodeInterpreterClient(
                name="e2e-code-interpreter",
                namespace=self.namespace,
                workload_manager_url=self.workload_manager_url,
                router_url=self.router_url,
                auth_token=self.api_token,
                verbose=True
            ) as client:
                print(f"Session created: {client.session_id}")
                self.assertIsNotNone(client.session_id, "Session ID should be created")

                # Step 1: Upload fibonacci.py script
                print("Uploading fibonacci.py script...")
                client.write_file(fibonacci_script, "fibonacci.py")

                # Step 2: Execute the script
                print("Executing fibonacci.py script...")
                exec_result = client.run_code("python", fibonacci_script)
                print(f"Script execution result: {repr(exec_result)}")
                self.assertIn("Fibonacci sequence generated", exec_result,
                             f"Expected success message, got: {exec_result}")

                # Step 3: Download and verify the output.json
                print("Downloading output.json...")
                client.download_file("output.json", "/tmp/test_output.json")

                # Read and parse the downloaded JSON file
                with open("/tmp/test_output.json", "r") as f:
                    output_content = f.read()
                result_data = json.loads(output_content)
                print(f"Generated JSON content: {json.dumps(result_data, indent=2)}")

                # Assert: Check fibonacci sequence
                self.assertEqual(
                    result_data["fibonacci_sequence"],
                    expected_fib,
                    f"Fibonacci mismatch: expected {expected_fib}, got {result_data['fibonacci_sequence']}"
                )

                # Assert: Check length
                self.assertEqual(result_data["length"], len(expected_fib),
                               f"Length mismatch: expected {len(expected_fib)}, got {result_data['length']}")
        finally:
            # Clean up temporary files
            try:
                if os.path.exists("/tmp/test_output.json"):
                    os.remove("/tmp/test_output.json")
                    print("Cleaned up temporary file: /tmp/test_output.json")
            except Exception as cleanup_error:
                print(f"Warning: Failed to clean up temporary file: {cleanup_error}")

if __name__ == "__main__":
    print("Starting CodeInterpreter E2E Tests...")
    print(f"Python path: {sys.path}")

    # Ensure we're in verbose mode for debugging
    if "--verbose" not in sys.argv:
        sys.argv.append("--verbose")

    unittest.main(verbosity=2)
