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
- Lazy initialization
- Session reuse
- Context manager behavior
- Error handling / resource cleanup
"""

import unittest
from unittest.mock import Mock, patch
import os

# Set required env var before import
os.environ.setdefault("ROUTER_URL", "http://mock-router:8080")

from agentcube.code_interpreter import CodeInterpreterClient


class TestCodeInterpreterClientInit(unittest.TestCase):
    """Test client initialization and lazy loading."""
    
    @patch('agentcube.code_interpreter.ControlPlaneClient')
    def test_init_no_session_id_lazy_loading(self, mock_cp_class):
        """Session should not be created on init without session_id."""
        mock_cp = Mock()
        mock_cp_class.return_value = mock_cp
        
        client = CodeInterpreterClient(router_url="http://test:8080")
        
        # Session not created yet (lazy)
        self.assertIsNone(client._session_id)
        self.assertIsNone(client.dp_client)
        mock_cp.create_session.assert_not_called()
    
    @patch('agentcube.code_interpreter.DataPlaneClient')
    @patch('agentcube.code_interpreter.ControlPlaneClient')
    def test_init_with_session_id_immediate_dp_init(self, mock_cp_class, mock_dp_class):
        """DataPlaneClient should be initialized immediately when session_id provided."""
        mock_cp = Mock()
        mock_cp_class.return_value = mock_cp
        
        client = CodeInterpreterClient(
            router_url="http://test:8080",
            session_id="existing-session-123"
        )
        
        # Session should be set immediately
        self.assertEqual(client._session_id, "existing-session-123")
        # DataPlaneClient should be initialized
        mock_dp_class.assert_called_once()
        self.assertIsNotNone(client.dp_client)


class TestSessionIdProperty(unittest.TestCase):
    """Test session_id property auto-start behavior."""
    
    @patch('agentcube.code_interpreter.DataPlaneClient')
    @patch('agentcube.code_interpreter.ControlPlaneClient')
    def test_session_id_property_triggers_start(self, mock_cp_class, mock_dp_class):
        """Accessing session_id should auto-start session."""
        mock_cp = Mock()
        mock_cp.create_session.return_value = "new-session-456"
        mock_cp_class.return_value = mock_cp
        
        client = CodeInterpreterClient(router_url="http://test:8080")
        
        # Access session_id property
        session_id = client.session_id
        
        # Session should be created
        self.assertEqual(session_id, "new-session-456")
        mock_cp.create_session.assert_called_once()
        mock_dp_class.assert_called_once()


class TestSessionReuse(unittest.TestCase):
    """Test session reuse across multiple client instances."""
    
    @patch('agentcube.code_interpreter.DataPlaneClient')
    @patch('agentcube.code_interpreter.ControlPlaneClient')
    def test_reuse_session_no_new_creation(self, mock_cp_class, mock_dp_class):
        """Reusing session_id should not create new session."""
        mock_cp = Mock()
        mock_cp_class.return_value = mock_cp
        
        # Create client with existing session_id
        client = CodeInterpreterClient(
            router_url="http://test:8080",
            session_id="reused-session-789"
        )
        
        # Trigger an API call
        client._ensure_started()
        
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
        
        with CodeInterpreterClient(router_url="http://test:8080") as client:
            _ = client.session_id  # Trigger session creation
        
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
        
        client = CodeInterpreterClient(router_url="http://test:8080")
        
        with self.assertRaises(Exception) as ctx:
            _ = client.session_id  # Trigger session creation
        
        self.assertIn("Connection failed", str(ctx.exception))
        
        # Session should be cleaned up
        mock_cp.delete_session.assert_called_once_with("leaked-session-999")
        self.assertIsNone(client._session_id)


class TestStartMethod(unittest.TestCase):
    """Test explicit start() method."""
    
    @patch('agentcube.code_interpreter.DataPlaneClient')
    @patch('agentcube.code_interpreter.ControlPlaneClient')
    def test_start_returns_session_id(self, mock_cp_class, mock_dp_class):
        """start() should return session_id."""
        mock_cp = Mock()
        mock_cp.create_session.return_value = "started-session-111"
        mock_cp_class.return_value = mock_cp
        
        client = CodeInterpreterClient(router_url="http://test:8080")
        session_id = client.start()
        
        self.assertEqual(session_id, "started-session-111")
        mock_cp.create_session.assert_called_once()
    
    @patch('agentcube.code_interpreter.DataPlaneClient')
    @patch('agentcube.code_interpreter.ControlPlaneClient')
    def test_start_idempotent(self, mock_cp_class, mock_dp_class):
        """Calling start() multiple times should not create multiple sessions."""
        mock_cp = Mock()
        mock_cp.create_session.return_value = "single-session-222"
        mock_cp_class.return_value = mock_cp
        
        client = CodeInterpreterClient(router_url="http://test:8080")
        
        # Call start() multiple times
        client.start()
        client.start()
        client.start()
        
        # Should only create one session
        mock_cp.create_session.assert_called_once()


if __name__ == "__main__":
    unittest.main()
