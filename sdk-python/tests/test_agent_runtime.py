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
import unittest
from unittest.mock import Mock, patch

import requests.exceptions
os.environ.setdefault("ROUTER_URL", "http://mock-router:8080")

from agentcube.agent_runtime import AgentRuntimeClient


class TestAgentRuntimeClientSessionBootstrap(unittest.TestCase):
    @patch("agentcube.agent_runtime.AgentRuntimeDataPlaneClient")
    def test_init_bootstraps_session_id_when_missing(self, mock_dp_class):
        mock_dp = Mock()
        mock_dp.bootstrap_session_id.return_value = "sess-123"
        mock_dp_class.return_value = mock_dp

        client = AgentRuntimeClient(agent_name="agent-a", router_url="http://t:1")

        self.assertEqual(client.session_id, "sess-123")
        mock_dp.bootstrap_session_id.assert_called_once_with()

    @patch("agentcube.agent_runtime.AgentRuntimeDataPlaneClient")
    def test_init_reuses_session_id_when_provided(self, mock_dp_class):
        mock_dp = Mock()
        mock_dp_class.return_value = mock_dp

        client = AgentRuntimeClient(
            agent_name="agent-a",
            router_url="http://t:1",
            session_id="existing-456",
        )

        self.assertEqual(client.session_id, "existing-456")
        mock_dp.bootstrap_session_id.assert_not_called()


class TestAgentRuntimeClientInvoke(unittest.TestCase):
    @patch("agentcube.agent_runtime.AgentRuntimeDataPlaneClient")
    def test_invoke_passes_session_header_and_returns_json(self, mock_dp_class):
        mock_dp = Mock()
        mock_dp.bootstrap_session_id.return_value = "sess-789"

        resp = Mock()
        resp.raise_for_status.return_value = None
        resp.json.return_value = {"ok": True}
        mock_dp.invoke.return_value = resp

        mock_dp_class.return_value = mock_dp

        client = AgentRuntimeClient(agent_name="agent-a", router_url="http://t:1")
        out = client.invoke({"input": "hi"})

        self.assertEqual(out, {"ok": True})
        mock_dp.invoke.assert_called_once()
        kwargs = mock_dp.invoke.call_args.kwargs
        self.assertEqual(kwargs["session_id"], "sess-789")
        self.assertEqual(kwargs["payload"], {"input": "hi"})

    @patch("agentcube.agent_runtime.AgentRuntimeDataPlaneClient")
    def test_invoke_falls_back_to_text_when_non_json(self, mock_dp_class):
        mock_dp = Mock()
        mock_dp.bootstrap_session_id.return_value = "sess-999"

        resp = Mock()
        resp.raise_for_status.return_value = None
        resp.json.side_effect = requests.exceptions.JSONDecodeError("not json", "", 0)
        resp.text = "plain"
        mock_dp.invoke.return_value = resp

        mock_dp_class.return_value = mock_dp

        client = AgentRuntimeClient(agent_name="agent-a", router_url="http://t:1")
        out = client.invoke({"input": "hi"})

        self.assertEqual(out, "plain")


class TestAgentRuntimeDataPlaneClient(unittest.TestCase):
    @patch("agentcube.clients.agent_runtime_data_plane.create_session")
    def test_bootstrap_session_id_extracts_header(self, mock_create_session):
        sess = Mock()
        resp = Mock()
        resp.raise_for_status.return_value = None
        resp.headers = {"x-agentcube-session-id": "abc"}
        sess.get.return_value = resp
        mock_create_session.return_value = sess

        from agentcube.clients.agent_runtime_data_plane import AgentRuntimeDataPlaneClient

        client = AgentRuntimeDataPlaneClient(
            router_url="http://router",
            namespace="default",
            agent_name="agent-a",
        )
        self.assertEqual(client.bootstrap_session_id(), "abc")

    @patch("agentcube.clients.agent_runtime_data_plane.create_session")
    def test_invoke_sends_session_header(self, mock_create_session):
        sess = Mock()
        resp = Mock()
        sess.post.return_value = resp
        mock_create_session.return_value = sess

        from agentcube.clients.agent_runtime_data_plane import AgentRuntimeDataPlaneClient

        client = AgentRuntimeDataPlaneClient(
            router_url="http://router",
            namespace="default",
            agent_name="agent-a",
            connect_timeout=1.0,
            timeout=2,
        )
        _resp = client.invoke(session_id="sid", payload={"p": 1})
        self.assertIs(_resp, resp)

        sess.post.assert_called_once()
        call_kwargs = sess.post.call_args.kwargs
        self.assertEqual(call_kwargs["headers"]["x-agentcube-session-id"], "sid")
        self.assertEqual(call_kwargs["json"], {"p": 1})


if __name__ == "__main__":
    unittest.main()
