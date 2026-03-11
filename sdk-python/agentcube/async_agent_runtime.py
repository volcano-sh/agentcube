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

import logging
import os
from typing import Any, Dict, Optional

from agentcube.clients.async_agent_runtime_data_plane import AsyncAgentRuntimeDataPlaneClient
from agentcube.utils.log import get_logger


class AsyncAgentRuntimeClient:
    """Async client for invoking AgentRuntime services.

    Usage::

        # Async context manager (recommended)
        async with AsyncAgentRuntimeClient(agent_name="my-agent", ...) as client:
            result = await client.invoke({"input": "hello"})

        # Manual lifecycle management
        client = AsyncAgentRuntimeClient(agent_name="my-agent", ...)
        await client.start()
        try:
            result = await client.invoke({"input": "hello"})
        finally:
            await client.close()
    """

    def __init__(
        self,
        agent_name: str,
        namespace: str = "default",
        router_url: Optional[str] = None,
        verbose: bool = False,
        session_id: Optional[str] = None,
        timeout: int = 120,
        connect_timeout: float = 5.0,
    ):
        self.agent_name = agent_name
        self.namespace = namespace
        self.timeout = timeout
        self.connect_timeout = connect_timeout

        level = logging.DEBUG if verbose else logging.INFO
        self.logger = get_logger(__name__, level=level)

        router_url = router_url or os.getenv("ROUTER_URL")
        if not router_url:
            raise ValueError(
                "Router URL for Data Plane communication must be provided via "
                "'router_url' argument or 'ROUTER_URL' environment variable."
            )
        self.router_url = router_url

        self.session_id: Optional[str] = session_id
        self.dp_client = AsyncAgentRuntimeDataPlaneClient(
            router_url=self.router_url,
            namespace=self.namespace,
            agent_name=self.agent_name,
            timeout=self.timeout,
            connect_timeout=self.connect_timeout,
        )
        if verbose:
            self.dp_client.logger.setLevel(logging.DEBUG)

    async def start(self) -> None:
        """Bootstrap the session ID if not already set."""
        if not self.session_id:
            self.logger.info("Bootstrapping AgentRuntime session...")
            self.session_id = await self.dp_client.bootstrap_session_id()
            self.logger.info(f"AgentRuntime session created: {self.session_id}")
        else:
            self.logger.info(f"Reusing AgentRuntime session: {self.session_id}")

    async def __aenter__(self) -> "AsyncAgentRuntimeClient":
        await self.start()
        return self

    async def __aexit__(self, exc_type, exc_val, exc_tb) -> None:
        await self.close()

    async def invoke(
        self, payload: Dict[str, Any], timeout: Optional[float] = None
    ) -> Any:
        """Invoke the agent runtime with a payload.

        Args:
            payload: The request payload to send.
            timeout: Optional per-request timeout in seconds.

        Returns:
            The parsed JSON response, or the raw text if the response is not JSON.
        """
        if not self.session_id:
            raise ValueError("AgentRuntime session_id is not initialized; call start() first.")

        resp = await self.dp_client.invoke(
            session_id=self.session_id,
            payload=payload,
            timeout=timeout,
        )
        resp.raise_for_status()
        try:
            return resp.json()
        except ValueError:
            # httpx raises ValueError (json.JSONDecodeError subclass) when the
            # response body is not valid JSON; fall back to returning raw text.
            return resp.text

    async def close(self) -> None:
        """Close the underlying HTTP session."""
        if self.dp_client:
            await self.dp_client.close()
