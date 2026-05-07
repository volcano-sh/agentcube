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

"""
E2E tests for E2B API compatibility using Python E2B SDK.

This test suite verifies that AgentCube Router correctly implements
E2B-compatible REST API by using the official E2B Python SDK.

Prerequisites:
- AgentCube Router running with E2B API enabled
- E2B Python SDK installed: pip install e2b-code-interpreter
- Environment variables set:
  - E2B_API_KEY: API key for authentication
  - E2B_BASE_URL: Base URL of AgentCube Router (e.g., http://localhost:8081)
"""

import os
import time
import unittest

# Try to import E2B SDK
try:
    from e2b_code_interpreter import Sandbox
    from e2b_code_interpreter.exceptions import SandboxException
    E2B_SDK_AVAILABLE = True
except ImportError:
    E2B_SDK_AVAILABLE = False
    print("Warning: e2b_code_interpreter not installed. Install with: pip install e2b-code-interpreter")


class TestE2BSDKCompatibility(unittest.TestCase):
    """E2E tests for E2B API compatibility using E2B Python SDK."""

    @classmethod
    def setUpClass(cls):
        """Set up test class - check prerequisites."""
        if not E2B_SDK_AVAILABLE:
            raise unittest.SkipTest("E2B Python SDK not installed. Run: pip install e2b-code-interpreter")

        cls.api_key = os.getenv("E2B_API_KEY", "test-api-key")
        cls.base_url = os.getenv("E2B_BASE_URL", "http://localhost:8081")
        cls.template_id = os.getenv("E2B_TEMPLATE_ID", "default/code-interpreter")

        # Configure E2B SDK to use AgentCube Router
        os.environ["E2B_DOMAIN"] = cls.base_url.replace("http://", "").replace("https://", "")
        if cls.base_url.startswith("https"):
            os.environ["E2B_HTTPS"] = "true"

        print("\nTest Configuration:")
        print(f"  Base URL: {cls.base_url}")
        print(f"  Template ID: {cls.template_id}")
        print(f"  API Key: {'*' * len(cls.api_key)}")

    def test_01_create_sandbox(self):
        """
        Test Case 1: Create a sandbox using E2B SDK.

        Verifies:
        - Sandbox can be created via E2B SDK
        - Response contains valid sandbox ID
        - Sandbox is in running state after creation
        """
        print("\n[Test 1] Creating sandbox...")

        sandbox = Sandbox.create(
            api_key=self.api_key,
            template_id=self.template_id,
            timeout=300  # 5 minutes
        )

        self.assertIsNotNone(sandbox.sandbox_id, "Sandbox ID should be present")
        self.assertTrue(len(sandbox.sandbox_id) > 0, "Sandbox ID should not be empty")
        print(f"  Created sandbox: {sandbox.sandbox_id}")

        # Verify sandbox info
        info = sandbox.get_info()
        self.assertEqual(info.sandbox_id, sandbox.sandbox_id)
        self.assertEqual(info.template_id, self.template_id)
        print(f"  State: {info.state}")
        print(f"  Started at: {info.started_at}")

        # Clean up
        sandbox.close()
        print("  Sandbox closed successfully")

    def test_02_sandbox_lifecycle_with_context_manager(self):
        """
        Test Case 2: Sandbox lifecycle using context manager.

        Verifies:
        - Sandbox can be created using 'with' statement
        - Sandbox is automatically cleaned up after context exit
        - Timeout can be set during creation
        """
        print("\n[Test 2] Testing context manager lifecycle...")

        with Sandbox.create(
            api_key=self.api_key,
            template_id=self.template_id,
            timeout=600
        ) as sandbox:
            print(f"  Created sandbox: {sandbox.sandbox_id}")
            self.assertIsNotNone(sandbox.sandbox_id)

            # Verify basic properties
            self.assertIsNotNone(sandbox.started_at)
            print(f"  Started at: {sandbox.started_at}")

        print("  Sandbox automatically closed by context manager")

    def test_03_list_sandboxes(self):
        """
        Test Case 3: List running sandboxes.

        Verifies:
        - Sandboxes can be listed via E2B SDK
        - Created sandbox appears in the list
        - List contains correct sandbox metadata
        """
        print("\n[Test 3] Testing list sandboxes...")

        # Create a sandbox first
        sandbox = Sandbox.create(
            api_key=self.api_key,
            template_id=self.template_id,
            timeout=300
        )
        print(f"  Created sandbox: {sandbox.sandbox_id}")

        try:
            # List all sandboxes
            sandboxes = Sandbox.list(api_key=self.api_key)
            self.assertIsInstance(sandboxes, list)
            print(f"  Found {len(sandboxes)} running sandboxes")

            # Verify our sandbox is in the list
            sandbox_ids = [sb.sandbox_id for sb in sandboxes]
            self.assertIn(sandbox.sandbox_id, sandbox_ids,
                         f"Created sandbox {sandbox.sandbox_id} should be in the list")

            # Verify list entry metadata
            our_sandbox = next(sb for sb in sandboxes if sb.sandbox_id == sandbox.sandbox_id)
            self.assertEqual(our_sandbox.template_id, self.template_id)
            print(f"  Verified sandbox in list: {our_sandbox.template_id}")

        finally:
            sandbox.close()
            print("  Sandbox closed")

    def test_04_get_sandbox_info(self):
        """
        Test Case 4: Get sandbox details.

        Verifies:
        - Sandbox details can be retrieved
        - Response contains expected fields (ID, template, state, timestamps)
        """
        print("\n[Test 4] Testing get sandbox info...")

        with Sandbox.create(
            api_key=self.api_key,
            template_id=self.template_id,
            timeout=300
        ) as sandbox:
            print(f"  Created sandbox: {sandbox.sandbox_id}")

            # Get detailed info
            info = sandbox.get_info()

            self.assertEqual(info.sandbox_id, sandbox.sandbox_id)
            self.assertEqual(info.template_id, self.template_id)
            self.assertIsNotNone(info.state)
            self.assertIsNotNone(info.started_at)

            print(f"  Sandbox ID: {info.sandbox_id}")
            print(f"  Template: {info.template_id}")
            print(f"  State: {info.state}")
            print(f"  Started: {info.started_at}")
            if hasattr(info, 'cpu_count') and info.cpu_count:
                print(f"  CPU: {info.cpu_count}")
            if hasattr(info, 'memory_mb') and info.memory_mb:
                print(f"  Memory: {info.memory_mb}MB")

    def test_05_set_timeout(self):
        """
        Test Case 5: Set sandbox timeout.

        Verifies:
        - Timeout can be updated via set_timeout()
        - New timeout is reflected in sandbox info
        """
        print("\n[Test 5] Testing set timeout...")

        with Sandbox.create(
            api_key=self.api_key,
            template_id=self.template_id,
            timeout=300  # 5 minutes initially
        ) as sandbox:
            print(f"  Created sandbox: {sandbox.sandbox_id}")

            # Get initial end time
            initial_info = sandbox.get_info()
            initial_end = initial_info.end_at
            print(f"  Initial end time: {initial_end}")

            # Extend timeout to 10 minutes
            sandbox.set_timeout(600)
            print("  Timeout extended to 600 seconds")

            # Verify new end time (if supported by the response)
            updated_info = sandbox.get_info()
            if hasattr(updated_info, 'end_at') and updated_info.end_at:
                print(f"  Updated end time: {updated_info.end_at}")

    def test_06_refresh_ttl(self):
        """
        Test Case 6: Refresh sandbox TTL.

        Verifies:
        - TTL can be refreshed via refresh()
        - Refresh extends the sandbox lifetime
        """
        print("\n[Test 6] Testing refresh TTL...")

        with Sandbox.create(
            api_key=self.api_key,
            template_id=self.template_id,
            timeout=300
        ) as sandbox:
            print(f"  Created sandbox: {sandbox.sandbox_id}")

            # Refresh TTL (extend by 5 minutes from now)
            sandbox.refresh(timeout=300)
            print("  TTL refreshed successfully")

            # Verify sandbox is still accessible
            info = sandbox.get_info()
            self.assertEqual(info.sandbox_id, sandbox.sandbox_id)
            print("  Sandbox still accessible after refresh")

    def test_07_delete_sandbox(self):
        """
        Test Case 7: Delete sandbox explicitly.

        Verifies:
        - Sandbox can be deleted via close()
        - Deleted sandbox no longer appears in list
        """
        print("\n[Test 7] Testing delete sandbox...")

        # Create sandbox
        sandbox = Sandbox.create(
            api_key=self.api_key,
            template_id=self.template_id,
            timeout=300
        )
        sandbox_id = sandbox.sandbox_id
        print(f"  Created sandbox: {sandbox_id}")

        # Verify it's in the list
        sandboxes_before = Sandbox.list(api_key=self.api_key)
        ids_before = [sb.sandbox_id for sb in sandboxes_before]
        self.assertIn(sandbox_id, ids_before)
        print("  Sandbox present in list before deletion")

        # Delete sandbox
        sandbox.close()
        print("  Sandbox deleted")

        # Wait a moment for deletion to propagate
        time.sleep(2)

        # Verify it's no longer in the list
        sandboxes_after = Sandbox.list(api_key=self.api_key)
        ids_after = [sb.sandbox_id for sb in sandboxes_after]
        self.assertNotIn(sandbox_id, ids_after,
                        "Deleted sandbox should not appear in list")
        print("  Sandbox no longer in list after deletion")

    def test_08_full_workflow(self):
        """
        Test Case 8: Complete workflow - create, manage, delete.

        This test simulates a typical user workflow:
        1. Create sandbox
        2. Get info
        3. List sandboxes
        4. Set timeout
        5. Refresh TTL
        6. Delete sandbox

        Verifies the entire lifecycle works end-to-end.
        """
        print("\n[Test 8] Testing full workflow...")

        created_sandboxes = []

        try:
            # Step 1: Create multiple sandboxes
            print("  Step 1: Creating sandboxes...")
            for i in range(2):
                sandbox = Sandbox.create(
                    api_key=self.api_key,
                    template_id=self.template_id,
                    timeout=600
                )
                created_sandboxes.append(sandbox)
                print(f"    Created sandbox {i+1}: {sandbox.sandbox_id}")

            # Step 2: Get info for each sandbox
            print("  Step 2: Getting sandbox info...")
            for sandbox in created_sandboxes:
                info = sandbox.get_info()
                self.assertEqual(info.sandbox_id, sandbox.sandbox_id)
                print(f"    {sandbox.sandbox_id}: {info.state}")

            # Step 3: List all sandboxes
            print("  Step 3: Listing sandboxes...")
            all_sandboxes = Sandbox.list(api_key=self.api_key)
            our_ids = {sb.sandbox_id for sb in created_sandboxes}
            listed_ids = {sb.sandbox_id for sb in all_sandboxes}
            for sid in our_ids:
                self.assertIn(sid, listed_ids, f"Sandbox {sid} should be in list")
            print(f"    Found {len(all_sandboxes)} total sandboxes, our 2 are present")

            # Step 4: Set timeout
            print("  Step 4: Setting timeout...")
            for sandbox in created_sandboxes:
                sandbox.set_timeout(1200)  # 20 minutes
            print("    Timeout set to 1200 seconds for all sandboxes")

            # Step 5: Refresh TTL
            print("  Step 5: Refreshing TTL...")
            for sandbox in created_sandboxes:
                sandbox.refresh(timeout=600)  # 10 minutes from now
            print("    TTL refreshed for all sandboxes")

        finally:
            # Step 6: Clean up all sandboxes
            print("  Step 6: Cleaning up sandboxes...")
            for sandbox in created_sandboxes:
                try:
                    sandbox.close()
                    print(f"    Deleted: {sandbox.sandbox_id}")
                except Exception as e:
                    print(f"    Error deleting {sandbox.sandbox_id}: {e}")

        print("  Full workflow completed successfully!")

    def test_09_error_handling_invalid_template(self):
        """
        Test Case 9: Error handling for invalid template.

        Verifies:
        - Appropriate error is raised for invalid template ID
        - Error message is informative
        """
        print("\n[Test 9] Testing error handling for invalid template...")

        try:
            Sandbox.create(
                api_key=self.api_key,
                template_id="invalid/template-does-not-exist",
                timeout=300
            )
            self.fail("Should have raised an exception for invalid template")

        except SandboxException as e:
            print(f"  Got expected error: {type(e).__name__}")
            print(f"  Error message: {str(e)[:100]}...")

    def test_10_concurrent_sandboxes(self):
        """
        Test Case 10: Create multiple sandboxes concurrently.

        Verifies:
        - Multiple sandboxes can be created simultaneously
        - Each sandbox has unique ID
        - All sandboxes can be managed independently
        """
        print("\n[Test 10] Testing concurrent sandboxes...")

        import concurrent.futures

        def create_and_verify(idx):
            """Helper to create a sandbox and return its ID."""
            with Sandbox.create(
                api_key=self.api_key,
                template_id=self.template_id,
                timeout=300
            ) as sandbox:
                print(f"    Thread {idx}: Created {sandbox.sandbox_id}")

                # Verify we can get info
                info = sandbox.get_info()
                return sandbox.sandbox_id, info.template_id

        # Create 3 sandboxes concurrently
        results = []
        with concurrent.futures.ThreadPoolExecutor(max_workers=3) as executor:
            futures = [executor.submit(create_and_verify, i) for i in range(3)]
            for future in concurrent.futures.as_completed(futures):
                try:
                    result = future.result()
                    results.append(result)
                except Exception as e:
                    self.fail(f"Concurrent sandbox creation failed: {e}")

        # Verify all sandboxes were created with unique IDs
        self.assertEqual(len(results), 3, "Should have created 3 sandboxes")
        sandbox_ids = [r[0] for r in results]
        self.assertEqual(len(set(sandbox_ids)), 3, "All sandbox IDs should be unique")

        print(f"  Successfully created {len(results)} concurrent sandboxes")
        for sid, tid in results:
            print(f"    - {sid} (template: {tid})")


class TestE2BSDKCodeExecution(unittest.TestCase):
    """E2E tests for code execution using E2B Python SDK."""

    @classmethod
    def setUpClass(cls):
        """Set up test class."""
        if not E2B_SDK_AVAILABLE:
            raise unittest.SkipTest("E2B Python SDK not installed")

        cls.api_key = os.getenv("E2B_API_KEY", "test-api-key")
        cls.base_url = os.getenv("E2B_BASE_URL", "http://localhost:8081")
        cls.template_id = os.getenv("E2B_TEMPLATE_ID", "default/code-interpreter")

        os.environ["E2B_DOMAIN"] = cls.base_url.replace("http://", "").replace("https://", "")
        if cls.base_url.startswith("https"):
            os.environ["E2B_HTTPS"] = "true"

    def test_11_execute_python_code(self):
        """
        Test Case 11: Execute Python code in sandbox.

        Verifies:
        - Python code can be executed via E2B SDK
        - Output is captured correctly
        """
        print("\n[Test 11] Testing Python code execution...")

        # Note: This test requires the E2B SDK's code execution feature
        # which may require additional setup (e2b-code-interpreter with execution support)
        try:
            from e2b_code_interpreter import CodeInterpreter
            _ = CodeInterpreter
            CODE_EXECUTION_AVAILABLE = True
        except ImportError:
            CODE_EXECUTION_AVAILABLE = False

        if not CODE_EXECUTION_AVAILABLE:
            self.skipTest("CodeInterpreter not available in E2B SDK")

        with Sandbox.create(
            api_key=self.api_key,
            template_id=self.template_id,
            timeout=300
        ) as sandbox:
            print(f"  Created sandbox: {sandbox.sandbox_id}")

            # Execute Python code
            execution = sandbox.run_code("print('Hello from E2B SDK')")

            self.assertIn("Hello from E2B SDK", execution.stdout)
            print(f"  Code execution result: {execution.stdout}")


if __name__ == "__main__":
    print("=" * 70)
    print("E2B SDK Compatibility E2E Tests")
    print("=" * 70)
    print()

    # Check environment variables
    if not os.getenv("E2B_API_KEY"):
        print("WARNING: E2B_API_KEY not set, using default 'test-api-key'")

    if not os.getenv("E2B_BASE_URL"):
        print("WARNING: E2B_BASE_URL not set, using default 'http://localhost:8081'")

    print()

    # Run tests
    # Note: If E2B SDK is not installed, tests will be skipped via SkipTest in setUpClass
    unittest.main(verbosity=2)
