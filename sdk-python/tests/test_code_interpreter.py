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

import requests

# Set required env var before import
os.environ.setdefault("ROUTER_URL", "http://mock-router:8080")

from agentcube.code_interpreter import CodeInterpreterClient
from agentcube.clients.control_plane import ControlPlaneClient
from agentcube.exceptions import SessionError, SessionNotFoundError


class TestCodeInterpreterClientInit(unittest.TestCase):
    """Test client initialization."""

    @patch('agentcube.code_interpreter.CodeInterpreterDataPlaneClient')
    @patch('agentcube.code_interpreter.ControlPlaneClient')
    def test_init_creates_session(self, mock_cp_class, mock_dp_class):
        """Session should be created on init."""
        mock_cp = Mock()
        mock_cp.create_session.return_value = "new-session-123"
        mock_cp_class.return_value = mock_cp

        client = CodeInterpreterClient(router_url="http://test:8080")

        # Session should be created
        self.assertEqual(client.session_id, "new-session-123")
        mock_cp.create_session.assert_called_once_with(
            name="my-interpreter",
            namespace="default",
            ttl=None,
        )
        mock_dp_class.assert_called_once()

    @patch('agentcube.code_interpreter.CodeInterpreterDataPlaneClient')
    @patch('agentcube.code_interpreter.ControlPlaneClient')
    def test_init_forwards_explicit_ttl(self, mock_cp_class, mock_dp_class):
        mock_cp = Mock()
        mock_cp.create_session.return_value = "new-session-123"
        mock_cp_class.return_value = mock_cp

        CodeInterpreterClient(router_url="http://test:8080", ttl=600)

        mock_cp.create_session.assert_called_once_with(
            name="my-interpreter",
            namespace="default",
            ttl=600,
        )

    @patch('agentcube.code_interpreter.CodeInterpreterDataPlaneClient')
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


class TestControlPlaneClientTTL(unittest.TestCase):
    @patch('agentcube.clients.control_plane.create_session')
    def test_omits_ttl_by_default(self, mock_create_session):
        session = Mock()
        response = Mock()
        response.json.return_value = {"sessionId": "new-session-123"}
        session.post.return_value = response
        session.headers = {}
        mock_create_session.return_value = session

        client = ControlPlaneClient(workload_manager_url="http://test:8080")
        client.create_session()

        payload = session.post.call_args.kwargs["json"]
        self.assertNotIn("ttl", payload)

    @patch('agentcube.clients.control_plane.create_session')
    def test_includes_explicit_ttl(self, mock_create_session):
        session = Mock()
        response = Mock()
        response.json.return_value = {"sessionId": "new-session-123"}
        session.post.return_value = response
        session.headers = {}
        mock_create_session.return_value = session

        client = ControlPlaneClient(workload_manager_url="http://test:8080")
        client.create_session(ttl=600)

        payload = session.post.call_args.kwargs["json"]
        self.assertEqual(payload["ttl"], 600)


class TestSessionIdProperty(unittest.TestCase):
    """Test session_id property."""

    @patch('agentcube.code_interpreter.CodeInterpreterDataPlaneClient')
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

    @patch('agentcube.code_interpreter.CodeInterpreterDataPlaneClient')
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
        # CodeInterpreterDataPlaneClient should use the provided session_id
        mock_dp_class.assert_called_once()
        call_kwargs = mock_dp_class.call_args[1]
        self.assertEqual(call_kwargs['session_id'], "reused-session-789")

    @patch('agentcube.code_interpreter.CodeInterpreterDataPlaneClient')
    @patch('agentcube.code_interpreter.ControlPlaneClient')
    def test_missing_session_is_invalidated(self, mock_cp_class, mock_dp_class):
        mock_cp_class.return_value = Mock()
        mock_dp = Mock()
        mock_dp_class.return_value = mock_dp

        client = CodeInterpreterClient(
            router_url="http://test:8080",
            session_id="expired-session",
        )
        callback = mock_dp_class.call_args.kwargs["on_session_not_found"]
        callback()

        self.assertIsNone(client.session_id)
        with self.assertRaises(SessionError):
            client.list_files()
        mock_dp.list_files.assert_not_called()


class TestContextManager(unittest.TestCase):
    """Test context manager behavior."""

    @patch('agentcube.code_interpreter.CodeInterpreterDataPlaneClient')
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

    @patch('agentcube.code_interpreter.CodeInterpreterDataPlaneClient')
    @patch('agentcube.code_interpreter.ControlPlaneClient')
    def test_cleanup_on_dp_init_failure(self, mock_cp_class, mock_dp_class):
        """Session should be deleted if CodeInterpreterDataPlaneClient init fails."""
        mock_cp = Mock()
        mock_cp.create_session.return_value = "leaked-session-999"
        mock_cp_class.return_value = mock_cp

        # Make CodeInterpreterDataPlaneClient init fail
        mock_dp_class.side_effect = Exception("Connection failed")

        with self.assertRaises(Exception) as ctx:
            CodeInterpreterClient(router_url="http://test:8080")

        self.assertIn("Connection failed", str(ctx.exception))

        # Session should be cleaned up
        mock_cp.delete_session.assert_called_once_with("leaked-session-999")


class TestCodeInterpreterDataPlaneSessionErrors(unittest.TestCase):
    @patch('agentcube.clients.code_interpreter_data_plane.create_session')
    def test_session_not_found_raises_typed_error_and_invalidates(self, mock_create_session):
        session = Mock()
        response = requests.Response()
        response.status_code = 404
        response._content = b'{"code":"SESSION_NOT_FOUND","error":"session expired"}'
        session.request.return_value = response
        session.headers = requests.structures.CaseInsensitiveDict({
            "x-agentcube-session-id": "expired-session",
        })
        mock_create_session.return_value = session
        callback = Mock()

        from agentcube.clients.code_interpreter_data_plane import CodeInterpreterDataPlaneClient

        client = CodeInterpreterDataPlaneClient(
            session_id="expired-session",
            base_url="http://router/invocations/",
            on_session_not_found=callback,
        )

        with self.assertRaises(SessionNotFoundError) as ctx:
            client.list_files()

        self.assertEqual(ctx.exception.session_id, "expired-session")
        self.assertIsNone(client.session_id)
        self.assertNotIn("x-agentcube-session-id", session.headers)
        callback.assert_called_once_with()
        with self.assertRaises(SessionError):
            client.list_files()
        session.request.assert_called_once()

    @patch('agentcube.clients.code_interpreter_data_plane.create_session')
    def test_application_404_remains_http_error(self, mock_create_session):
        session = Mock()
        response = requests.Response()
        response.status_code = 404
        response._content = b'{"error":"application route not found"}'
        session.request.return_value = response
        session.headers = requests.structures.CaseInsensitiveDict({
            "x-agentcube-session-id": "active-session",
        })
        mock_create_session.return_value = session

        from agentcube.clients.code_interpreter_data_plane import CodeInterpreterDataPlaneClient

        client = CodeInterpreterDataPlaneClient(
            session_id="active-session",
            base_url="http://router/invocations/",
        )

        with self.assertRaises(requests.exceptions.HTTPError):
            client.list_files()

        self.assertEqual(client.session_id, "active-session")


if __name__ == "__main__":
    unittest.main()
