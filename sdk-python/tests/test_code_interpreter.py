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
Unit tests for CodeInterpreterClient session management.

Tests cover:
- Session creation
- Session reuse
- Context manager behavior
- Error handling / resource cleanup
"""

import os
import unittest
from unittest.mock import Mock, patch

# Set required env var before import
os.environ.setdefault("ROUTER_URL", "http://mock-router:8080")

from agentcube.code_interpreter import CodeInterpreterClient


class TestCodeInterpreterClientInit(unittest.TestCase):
    """Test client initialization."""

    @patch('agentcube.code_interpreter.DataPlaneClient')
    @patch('agentcube.code_interpreter.ControlPlaneClient')
    def test_init_creates_session(self, mock_cp_class, mock_dp_class):
        """Session should be created on init."""
        mock_cp = Mock()
        mock_cp.create_session.return_value = "new-session-123"
        mock_cp_class.return_value = mock_cp

        client = CodeInterpreterClient(router_url="http://test:8080")

        # Session should be created
        self.assertEqual(client.session_id, "new-session-123")
        mock_cp.create_session.assert_called_once()
        mock_dp_class.assert_called_once()

    @patch('agentcube.code_interpreter.DataPlaneClient')
    @patch('agentcube.code_interpreter.ControlPlaneClient')
    def test_init_with_session_id_reuses_session(self, mock_cp_class, mock_dp_class):
        """Providing session_id should reuse existing session."""
        mock_cp = Mock()
        mock_cp_class.return_value = mock_cp

        client = CodeInterpreterClient(
            router_url="http://test:8080",
            session_id="existing-session-123"
        )

        # Should reuse session, not create new
        self.assertEqual(client.session_id, "existing-session-123")
        mock_cp.create_session.assert_not_called()
        mock_dp_class.assert_called_once()


class TestSessionIdProperty(unittest.TestCase):
    """Test session_id property."""

    @patch('agentcube.code_interpreter.DataPlaneClient')
    @patch('agentcube.code_interpreter.ControlPlaneClient')
    def test_session_id_available_after_init(self, mock_cp_class, mock_dp_class):
        """session_id should be available after init."""
        mock_cp = Mock()
        mock_cp.create_session.return_value = "new-session-456"
        mock_cp_class.return_value = mock_cp

        client = CodeInterpreterClient(router_url="http://test:8080")

        # session_id should be available
        self.assertEqual(client.session_id, "new-session-456")


class TestSessionReuse(unittest.TestCase):
    """Test session reuse across multiple client instances."""

    @patch('agentcube.code_interpreter.DataPlaneClient')
    @patch('agentcube.code_interpreter.ControlPlaneClient')
    def test_reuse_session_no_new_creation(self, mock_cp_class, mock_dp_class):
        """Reusing session_id should not create new session."""
        mock_cp = Mock()
        mock_cp_class.return_value = mock_cp

        # Create client with existing session_id
        _client = CodeInterpreterClient(
            router_url="http://test:8080",
            session_id="reused-session-789"
        )

        # Should NOT create new session
        mock_cp.create_session.assert_not_called()
        # DataPlaneClient should use the provided session_id
        mock_dp_class.assert_called_once()
        call_kwargs = mock_dp_class.call_args[1]
        self.assertEqual(call_kwargs['session_id'], "reused-session-789")


class TestContextManager(unittest.TestCase):
    """Test context manager behavior."""

    @patch('agentcube.code_interpreter.DataPlaneClient')
    @patch('agentcube.code_interpreter.ControlPlaneClient')
    def test_context_manager_calls_stop(self, mock_cp_class, mock_dp_class):
        """Context manager should call stop() on exit."""
        mock_cp = Mock()
        mock_cp.create_session.return_value = "ctx-session-123"
        mock_cp_class.return_value = mock_cp

        mock_dp = Mock()
        mock_dp_class.return_value = mock_dp

        with CodeInterpreterClient(router_url="http://test:8080") as _client:
            pass  # Session already created in __init__

        # stop() should delete session
        mock_cp.delete_session.assert_called_once_with("ctx-session-123")
        mock_dp.close.assert_called_once()
        mock_cp.close.assert_called_once()


class TestResourceLeakPrevention(unittest.TestCase):
    """Test that resources are cleaned up on failure."""

    @patch('agentcube.code_interpreter.DataPlaneClient')
    @patch('agentcube.code_interpreter.ControlPlaneClient')
    def test_cleanup_on_dp_init_failure(self, mock_cp_class, mock_dp_class):
        """Session should be deleted if DataPlaneClient init fails."""
        mock_cp = Mock()
        mock_cp.create_session.return_value = "leaked-session-999"
        mock_cp_class.return_value = mock_cp

        # Make DataPlaneClient init fail
        mock_dp_class.side_effect = Exception("Connection failed")

        with self.assertRaises(Exception) as ctx:
            CodeInterpreterClient(router_url="http://test:8080")

        self.assertIn("Connection failed", str(ctx.exception))

        # Session should be cleaned up
        mock_cp.delete_session.assert_called_once_with("leaked-session-999")


if __name__ == "__main__":
    unittest.main()
