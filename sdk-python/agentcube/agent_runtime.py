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

from requests.exceptions import JSONDecodeError
from agentcube.auth import AuthProvider
from agentcube.clients.agent_runtime_data_plane import AgentRuntimeDataPlaneClient
from agentcube.utils.log import get_logger


class AgentRuntimeClient:
    def __init__(
        self,
        agent_name: str,
        namespace: str = "default",
        router_url: Optional[str] = None,
        verbose: bool = False,
        session_id: Optional[str] = None,
        timeout: int = 120,
        connect_timeout: float = 5.0,
        workload_manager_url: Optional[str] = None,
        auth_token: Optional[str] = None,
        auth: Optional[AuthProvider] = None,
    ):
        self.agent_name = agent_name
        self.namespace = namespace
        self.timeout = timeout
        self.connect_timeout = connect_timeout

        level = logging.DEBUG if verbose else logging.INFO
        self.logger = get_logger(__name__, level=level)

        self._auth = auth
        if not self._auth and auth_token:
            from agentcube.auth import TokenAuth
            self._auth = TokenAuth(auth_token)

        router_url = router_url or os.getenv("ROUTER_URL")
        if not router_url:
            raise ValueError(
                "Router URL for Data Plane communication must be provided via "
                "'router_url' argument or 'ROUTER_URL' environment variable."
            )
        self.router_url = router_url

        # Initialize Control Plane client for session deletion
        if workload_manager_url or os.getenv("AGENTCUBE_WORKLOAD_MANAGER_URL"):
            self.workload_manager_url = (
                workload_manager_url
                or os.getenv("AGENTCUBE_WORKLOAD_MANAGER_URL")
            )
            from agentcube.clients.control_plane import ControlPlaneClient
            self._control_plane = ControlPlaneClient(
                workload_manager_url=self.workload_manager_url,
                auth_token=auth_token or os.getenv("AGENTCUBE_AUTH_TOKEN"),
                auth=self._auth,
            )
        else:
            self._control_plane = None

        self.session_id: Optional[str] = session_id
        self._owned_session = session_id is None

        self.dp_client = AgentRuntimeDataPlaneClient(
            router_url=self.router_url,
            namespace=self.namespace,
            agent_name=self.agent_name,
            timeout=self.timeout,
            connect_timeout=self.connect_timeout,
            auth=self._auth,
        )
        if verbose:
            self.dp_client.logger.setLevel(logging.DEBUG)

        if not self.session_id:
            self.logger.info("Bootstrapping AgentRuntime session...")
            self.session_id = self.dp_client.bootstrap_session_id()
            self.logger.info(f"AgentRuntime session created: {self.session_id}")
        else:
            self.logger.info(f"Reusing AgentRuntime session: {self.session_id}")

    def __enter__(self):
        return self

    def __exit__(self, exc_type, exc_val, exc_tb):
        self.stop()

    def invoke(self, payload: Dict[str, Any], timeout: Optional[float] = None, path: str = "") -> Any:
        if not self.session_id:
            raise ValueError("AgentRuntime session_id is not initialized")

        resp = self.dp_client.invoke(
            session_id=self.session_id,
            payload=payload,
            timeout=timeout,
            path=path,
        )
        resp.raise_for_status()

        try:
            return resp.json()
        except JSONDecodeError:
            return resp.text

    def close(self) -> None:
        if self.dp_client:
            self.dp_client.close()

    def stop(self) -> None:
        """Close local connection and delete server-side session if owned."""
        try:
            self.close()
        except Exception as e:
            self.logger.warning(f"Error closing local connection: {e}")
        
        if self._owned_session and self.session_id and self._control_plane:
            try:
                self._control_plane.delete_agent_runtime_session(self.session_id)
                self.logger.info(f"Deleted AgentRuntime session: {self.session_id}")
            except Exception as e:
                self.logger.warning(f"Error deleting AgentRuntime session: {e}")