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
from unittest.mock import AsyncMock, MagicMock, patch

os.environ.setdefault("ROUTER_URL", "http://mock-router:8080")

from agentcube.async_agent_runtime import AsyncAgentRuntimeClient


class TestAsyncAgentRuntimeClientSessionBootstrap(unittest.IsolatedAsyncioTestCase):
    @patch("agentcube.async_agent_runtime.AsyncAgentRuntimeDataPlaneClient")
    async def test_start_bootstraps_session_id_when_missing(self, mock_dp_class):
        mock_dp = MagicMock()
        mock_dp.bootstrap_session_id = AsyncMock(return_value="sess-123")
        mock_dp.logger = MagicMock()
        mock_dp_class.return_value = mock_dp

        client = AsyncAgentRuntimeClient(agent_name="agent-a", router_url="http://t:1")
        await client.start()

        self.assertEqual(client.session_id, "sess-123")
        mock_dp.bootstrap_session_id.assert_awaited_once_with()

    @patch("agentcube.async_agent_runtime.AsyncAgentRuntimeDataPlaneClient")
    async def test_start_reuses_session_id_when_provided(self, mock_dp_class):
        mock_dp = MagicMock()
        mock_dp.logger = MagicMock()
        mock_dp_class.return_value = mock_dp

        client = AsyncAgentRuntimeClient(
            agent_name="agent-a",
            router_url="http://t:1",
            session_id="existing-456",
        )
        await client.start()

        self.assertEqual(client.session_id, "existing-456")
        mock_dp.bootstrap_session_id.assert_not_called()


class TestAsyncAgentRuntimeClientContextManager(unittest.IsolatedAsyncioTestCase):
    @patch("agentcube.async_agent_runtime.AsyncAgentRuntimeDataPlaneClient")
    async def test_context_manager_starts_and_closes(self, mock_dp_class):
        mock_dp = MagicMock()
        mock_dp.bootstrap_session_id = AsyncMock(return_value="ctx-sess")
        mock_dp.close = AsyncMock()
        mock_dp.logger = MagicMock()
        mock_dp_class.return_value = mock_dp

        async with AsyncAgentRuntimeClient(
            agent_name="agent-a", router_url="http://t:1"
        ) as client:
            self.assertEqual(client.session_id, "ctx-sess")

        mock_dp.close.assert_awaited_once()


class TestAsyncAgentRuntimeClientInvoke(unittest.IsolatedAsyncioTestCase):
    @patch("agentcube.async_agent_runtime.AsyncAgentRuntimeDataPlaneClient")
    async def test_invoke_returns_json(self, mock_dp_class):
        mock_dp = MagicMock()
        mock_dp.bootstrap_session_id = AsyncMock(return_value="sess-789")
        mock_dp.logger = MagicMock()

        # httpx responses are fully loaded — json() and text are sync
        mock_resp = MagicMock()
        mock_resp.raise_for_status = MagicMock()
        mock_resp.json = MagicMock(return_value={"ok": True})
        mock_dp.invoke = AsyncMock(return_value=mock_resp)

        mock_dp_class.return_value = mock_dp

        client = AsyncAgentRuntimeClient(agent_name="agent-a", router_url="http://t:1")
        await client.start()
        out = await client.invoke({"input": "hi"})

        self.assertEqual(out, {"ok": True})
        mock_dp.invoke.assert_awaited_once()
        call_kwargs = mock_dp.invoke.call_args.kwargs
        self.assertEqual(call_kwargs["session_id"], "sess-789")
        self.assertEqual(call_kwargs["payload"], {"input": "hi"})

    @patch("agentcube.async_agent_runtime.AsyncAgentRuntimeDataPlaneClient")
    async def test_invoke_falls_back_to_text(self, mock_dp_class):
        mock_dp = MagicMock()
        mock_dp.bootstrap_session_id = AsyncMock(return_value="sess-999")
        mock_dp.logger = MagicMock()

        mock_resp = MagicMock()
        mock_resp.raise_for_status = MagicMock()
        mock_resp.json = MagicMock(side_effect=ValueError("not json"))
        mock_resp.text = "plain"
        mock_dp.invoke = AsyncMock(return_value=mock_resp)

        mock_dp_class.return_value = mock_dp

        client = AsyncAgentRuntimeClient(agent_name="agent-a", router_url="http://t:1")
        await client.start()
        out = await client.invoke({"input": "hi"})

        self.assertEqual(out, "plain")


class TestAsyncAgentRuntimeDataPlaneClient(unittest.IsolatedAsyncioTestCase):
    @patch("agentcube.clients.async_agent_runtime_data_plane.create_async_session")
    async def test_bootstrap_session_id_extracts_header(self, mock_create):
        from agentcube.clients.async_agent_runtime_data_plane import (
            AsyncAgentRuntimeDataPlaneClient,
        )

        # httpx responses are fully loaded; no async context manager needed
        mock_resp = MagicMock()
        mock_resp.raise_for_status = MagicMock()
        mock_resp.headers = {"x-agentcube-session-id": "abc"}

        mock_session = MagicMock()
        mock_session.get = AsyncMock(return_value=mock_resp)
        mock_create.return_value = mock_session

        client = AsyncAgentRuntimeDataPlaneClient(
            router_url="http://router",
            namespace="default",
            agent_name="agent-a",
        )
        result = await client.bootstrap_session_id()
        self.assertEqual(result, "abc")

    @patch("agentcube.clients.async_agent_runtime_data_plane.create_async_session")
    async def test_invoke_sends_session_header(self, mock_create):
        from agentcube.clients.async_agent_runtime_data_plane import (
            AsyncAgentRuntimeDataPlaneClient,
        )

        mock_resp = MagicMock()
        mock_session = MagicMock()
        mock_session.post = AsyncMock(return_value=mock_resp)
        mock_create.return_value = mock_session

        client = AsyncAgentRuntimeDataPlaneClient(
            router_url="http://router",
            namespace="default",
            agent_name="agent-a",
            connect_timeout=1.0,
            timeout=2,
        )
        resp = await client.invoke(session_id="sid", payload={"p": 1})
        self.assertIs(resp, mock_resp)

        mock_session.post.assert_awaited_once()
        call_kwargs = mock_session.post.call_args.kwargs
        self.assertEqual(call_kwargs["headers"]["x-agentcube-session-id"], "sid")
        self.assertEqual(call_kwargs["json"], {"p": 1})


if __name__ == "__main__":
    unittest.main()
