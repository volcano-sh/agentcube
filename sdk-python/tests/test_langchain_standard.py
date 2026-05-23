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

from typing import Iterator
import pytest
from unittest.mock import MagicMock

from agentcube.integrations.langchain import AgentCubeSandbox
from agentcube.code_interpreter import CodeInterpreterClient

try:
    from langchain_tests.integration_tests import SandboxIntegrationTests
    HAS_LANGCHAIN_TESTS = True
except ImportError:
    # Fallback for CI environments where optional dependencies are not installed
    class SandboxIntegrationTests:  # type: ignore
        pass
    HAS_LANGCHAIN_TESTS = False

@pytest.mark.skipif(not HAS_LANGCHAIN_TESTS, reason="langchain-tests not installed")
class TestAgentCubeSandboxStandard(SandboxIntegrationTests):
    """Standard LangChain integration tests for AgentCubeSandbox."""

    @pytest.fixture(scope="class")
    def sandbox(self) -> Iterator[AgentCubeSandbox]:
        """Provide a configured AgentCubeSandbox for testing.

        Note: This currently uses a mocked backend to allow CI execution.
        To test against a real backend, provide the necessary environment variables
        (ROUTER_URL, etc.) and remove the mocking logic.
        """
        # For standard integration tests, we provide a mocked client
        # that simulates the behavior required by the test suite.
        mock_client = MagicMock(spec=CodeInterpreterClient)
        mock_client.session_id = "test-session-id"

        # Simulate successful command execution
        mock_client.execute_command.return_value = "standard output"

        # Simulate file operations
        mock_client.list_files.return_value = []

        # Return the sandbox with the mocked client
        backend = AgentCubeSandbox(client=mock_client)

        try:
            yield backend
        finally:
            # Cleanup
            mock_client.stop()
