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

from typing import Any, Dict, Optional
from urllib.parse import urljoin

import httpx

from agentcube.utils.async_http import create_async_session
from agentcube.utils.log import get_logger


class AsyncAgentRuntimeDataPlaneClient:
    SESSION_HEADER = "x-agentcube-session-id"

    def __init__(
        self,
        router_url: str,
        namespace: str,
        agent_name: str,
        timeout: int = 120,
        connect_timeout: float = 5.0,
        connector_limit: int = 100,
        connector_limit_per_host: int = 10,
    ):
        self.router_url = router_url
        self.namespace = namespace
        self.agent_name = agent_name
        self.timeout = timeout
        self.connect_timeout = connect_timeout
        self.logger = get_logger(f"{__name__}.AsyncAgentRuntimeDataPlaneClient")

        base_path = (
            f"/v1/namespaces/{namespace}/agent-runtimes/{agent_name}/invocations/"
        )
        self.base_url = urljoin(router_url, base_path)

        self._http_session = create_async_session(
            connector_limit=connector_limit,
            connector_limit_per_host=connector_limit_per_host,
        )

    def _make_timeout(self, read_timeout: Optional[float] = None) -> httpx.Timeout:
        """Build an httpx.Timeout with the given read timeout."""
        return httpx.Timeout(
            read_timeout if read_timeout is not None else self.timeout,
            connect=self.connect_timeout,
        )

    async def bootstrap_session_id(self) -> str:
        """Send a GET to the base URL to obtain a session ID from the response header."""
        resp = await self._http_session.get(
            self.base_url, timeout=self._make_timeout()
        )
        resp.raise_for_status()
        session_id = resp.headers.get(self.SESSION_HEADER)

        if not session_id:
            raise ValueError(
                f"Missing required response header: {self.SESSION_HEADER}"
            )
        return session_id

    async def invoke(
        self,
        session_id: str,
        payload: Dict[str, Any],
        timeout: Optional[float] = None,
    ) -> httpx.Response:
        """Invoke the agent runtime with a payload."""
        headers = {
            self.SESSION_HEADER: session_id,
            "Content-Type": "application/json",
        }
        t = self._make_timeout(timeout)
        self.logger.debug(f"POST {self.base_url}")
        return await self._http_session.post(
            self.base_url,
            json=payload,
            headers=headers,
            timeout=t,
        )

    async def close(self) -> None:
        """Close the underlying HTTP session."""
        await self._http_session.aclose()

    async def __aenter__(self) -> "AsyncAgentRuntimeDataPlaneClient":
        return self

    async def __aexit__(self, exc_type, exc_val, exc_tb) -> None:
        await self.close()
