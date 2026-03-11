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
Unit tests for AsyncCodeInterpreterClient session management.

Tests cover:
- Session creation
- Session reuse
- Context manager behavior
- Error handling / resource cleanup
"""

import os
import unittest
from unittest.mock import AsyncMock, MagicMock, patch

# Set required env var before import
os.environ.setdefault("ROUTER_URL", "http://mock-router:8080")

from agentcube.async_code_interpreter import AsyncCodeInterpreterClient


def _make_async_cp(session_id="new-session-123"):
    """Return a mock AsyncControlPlaneClient."""
    mock_cp = MagicMock()
    mock_cp.create_session = AsyncMock(return_value=session_id)
    mock_cp.delete_session = AsyncMock(return_value=True)
    mock_cp.close = AsyncMock()
    mock_cp.logger = MagicMock()
    return mock_cp


def _make_async_dp():
    """Return a mock AsyncCodeInterpreterDataPlaneClient."""
    mock_dp = MagicMock()
    mock_dp.close = AsyncMock()
    mock_dp.logger = MagicMock()
    return mock_dp


class TestAsyncCodeInterpreterClientCreate(unittest.IsolatedAsyncioTestCase):
    """Test client creation via the async factory classmethod."""

    @patch("agentcube.async_code_interpreter.AsyncCodeInterpreterDataPlaneClient")
    @patch("agentcube.async_code_interpreter.AsyncControlPlaneClient")
    async def test_create_creates_session(self, mock_cp_class, mock_dp_class):
        """AsyncCodeInterpreterClient.create() should create a session."""
        mock_cp = _make_async_cp("new-session-123")
        mock_cp_class.return_value = mock_cp

        client = await AsyncCodeInterpreterClient.create(router_url="http://test:8080")

        self.assertEqual(client.session_id, "new-session-123")
        mock_cp.create_session.assert_awaited_once()
        mock_dp_class.assert_called_once()

    @patch("agentcube.async_code_interpreter.AsyncCodeInterpreterDataPlaneClient")
    @patch("agentcube.async_code_interpreter.AsyncControlPlaneClient")
    async def test_create_with_session_id_reuses_session(self, mock_cp_class, mock_dp_class):
        """Providing session_id should reuse existing session."""
        mock_cp = _make_async_cp()
        mock_cp_class.return_value = mock_cp

        client = await AsyncCodeInterpreterClient.create(
            router_url="http://test:8080",
            session_id="existing-session-123",
        )

        self.assertEqual(client.session_id, "existing-session-123")
        mock_cp.create_session.assert_not_awaited()
        mock_dp_class.assert_called_once()


class TestAsyncCodeInterpreterContextManager(unittest.IsolatedAsyncioTestCase):
    """Test async context manager behavior."""

    @patch("agentcube.async_code_interpreter.AsyncCodeInterpreterDataPlaneClient")
    @patch("agentcube.async_code_interpreter.AsyncControlPlaneClient")
    async def test_context_manager_calls_stop(self, mock_cp_class, mock_dp_class):
        """Async context manager should call stop() on exit."""
        mock_cp = _make_async_cp("ctx-session-123")
        mock_cp_class.return_value = mock_cp

        mock_dp = _make_async_dp()
        mock_dp_class.return_value = mock_dp

        async with AsyncCodeInterpreterClient(router_url="http://test:8080") as _client:
            pass

        mock_cp.delete_session.assert_awaited_once_with("ctx-session-123")
        mock_dp.close.assert_awaited_once()
        mock_cp.close.assert_awaited_once()


class TestAsyncCodeInterpreterSessionReuse(unittest.IsolatedAsyncioTestCase):
    """Test session reuse across client instances."""

    @patch("agentcube.async_code_interpreter.AsyncCodeInterpreterDataPlaneClient")
    @patch("agentcube.async_code_interpreter.AsyncControlPlaneClient")
    async def test_reuse_session_no_new_creation(self, mock_cp_class, mock_dp_class):
        """Reusing session_id should not create a new session."""
        mock_cp = _make_async_cp()
        mock_cp_class.return_value = mock_cp

        _client = await AsyncCodeInterpreterClient.create(
            router_url="http://test:8080",
            session_id="reused-session-789",
        )

        mock_cp.create_session.assert_not_awaited()
        mock_dp_class.assert_called_once()
        call_kwargs = mock_dp_class.call_args[1]
        self.assertEqual(call_kwargs["session_id"], "reused-session-789")


class TestAsyncCodeInterpreterResourceLeakPrevention(unittest.IsolatedAsyncioTestCase):
    """Test that resources are cleaned up on failure."""

    @patch("agentcube.async_code_interpreter.AsyncCodeInterpreterDataPlaneClient")
    @patch("agentcube.async_code_interpreter.AsyncControlPlaneClient")
    async def test_cleanup_on_dp_init_failure(self, mock_cp_class, mock_dp_class):
        """Session should be deleted if data plane client init fails."""
        mock_cp = _make_async_cp("leaked-session-999")
        mock_cp_class.return_value = mock_cp

        mock_dp_class.side_effect = Exception("Connection failed")

        with self.assertRaises(Exception) as ctx:
            await AsyncCodeInterpreterClient.create(router_url="http://test:8080")

        self.assertIn("Connection failed", str(ctx.exception))
        mock_cp.delete_session.assert_awaited_once_with("leaked-session-999")


if __name__ == "__main__":
    unittest.main()
