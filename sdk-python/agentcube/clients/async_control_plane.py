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
from typing import Any, Dict, Optional

import httpx

from agentcube.utils.async_http import create_async_session
from agentcube.utils.log import get_logger
from agentcube.utils.utils import read_token_from_file


class AsyncControlPlaneClient:
    """Async client for AgentCube Control Plane (WorkloadManager).
    Handles creation and deletion of Code Interpreter sessions.
    """

    def __init__(
        self,
        workload_manager_url: Optional[str] = None,
        auth_token: Optional[str] = None,
        timeout: int = 120,
        connect_timeout: float = 5.0,
        connector_limit: int = 100,
        connector_limit_per_host: int = 10,
    ):
        """Initialize the async Control Plane client.

        Args:
            workload_manager_url: URL of the WorkloadManager service.
            auth_token: Kubernetes Service Account Token for authentication.
            timeout: Default request timeout in seconds (default: 120).
            connect_timeout: Connection timeout in seconds (default: 5).
            connector_limit: Total simultaneous connections (default: 100).
            connector_limit_per_host: Max keepalive connections per host (default: 10).
        """
        self.base_url = workload_manager_url or os.getenv("WORKLOAD_MANAGER_URL")
        if not self.base_url:
            raise ValueError(
                "Workload Manager URL must be provided via 'workload_manager_url' argument "
                "or 'WORKLOAD_MANAGER_URL' environment variable."
            )

        token_path = "/var/run/secrets/kubernetes.io/serviceaccount/token"
        token = auth_token or read_token_from_file(token_path)

        self.timeout = httpx.Timeout(timeout, connect=connect_timeout)
        self.logger = get_logger(f"{__name__}.AsyncControlPlaneClient")

        headers: Dict[str, str] = {"Content-Type": "application/json"}
        if token:
            headers["Authorization"] = f"Bearer {token}"

        self.session = create_async_session(
            connector_limit=connector_limit,
            connector_limit_per_host=connector_limit_per_host,
        )
        self.session.headers.update(headers)

    async def create_session(
        self,
        name: str = "my-interpreter",
        namespace: str = "default",
        metadata: Optional[Dict[str, Any]] = None,
        ttl: int = 3600,
    ) -> str:
        """Create a new Code Interpreter session.

        Args:
            name: Name of the CodeInterpreter template (CRD name).
            namespace: Kubernetes namespace.
            metadata: Optional metadata.
            ttl: Time to live (seconds).

        Returns:
            session_id (str): The ID of the created session.
        """
        payload = {
            "name": name,
            "namespace": namespace,
            "ttl": ttl,
            "metadata": metadata or {},
        }

        url = f"{self.base_url}/v1/code-interpreter"
        self.logger.debug(f"Creating session at {url} with payload: {payload}")

        resp = await self.session.post(url, json=payload, timeout=self.timeout)
        resp.raise_for_status()
        data = resp.json()

        if "sessionId" not in data or not data["sessionId"]:
            self.logger.error("Response JSON missing 'sessionId' in create_session response.")
            self.logger.debug(f"Full response data: {data}")
            raise ValueError("Failed to create session: 'sessionId' missing from response")
        return data["sessionId"]

    async def delete_session(self, session_id: str) -> bool:
        """Delete a Code Interpreter session.

        Args:
            session_id: The session ID to delete.

        Returns:
            True if deleted successfully (or didn't exist), False on failure.
        """
        url = f"{self.base_url}/v1/code-interpreter/sessions/{session_id}"
        self.logger.debug(f"Deleting session {session_id} at {url}")

        try:
            resp = await self.session.delete(url, timeout=self.timeout)
            if resp.status_code == 404:
                return True  # Already gone
            resp.raise_for_status()
            return True
        except httpx.HTTPError as e:
            # httpx.HTTPError is the base for both network errors (RequestError)
            # and HTTP status errors (HTTPStatusError). We treat all of them as
            # non-fatal so that callers can continue without a hard crash.
            self.logger.error(f"Failed to delete session {session_id}: {e}")
            return False

    async def close(self) -> None:
        """Close the underlying session and release connection pool resources."""
        await self.session.aclose()

    async def __aenter__(self) -> "AsyncControlPlaneClient":
        return self

    async def __aexit__(self, exc_type, exc_val, exc_tb) -> None:
        await self.close()
