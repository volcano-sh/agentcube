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

from __future__ import annotations

from typing import TYPE_CHECKING, Any, Dict, Optional
from urllib.parse import urljoin

import requests

from agentcube.utils.http import create_session
from agentcube.utils.log import get_logger

if TYPE_CHECKING:
    from agentcube.auth import AuthProvider


class AgentRuntimeDataPlaneClient:
    SESSION_HEADER = "x-agentcube-session-id"

    def __init__(
        self,
        router_url: str,
        namespace: str,
        agent_name: str,
        timeout: int = 120,
        connect_timeout: float = 5.0,
        pool_connections: int = 10,
        pool_maxsize: int = 10,
        auth: Optional["AuthProvider"] = None,
    ):
        self.router_url = router_url
        self.namespace = namespace
        self.agent_name = agent_name
        self.timeout = timeout
        self.connect_timeout = connect_timeout
        self._auth = auth
        self.logger = get_logger(f"{__name__}.AgentRuntimeDataPlaneClient")

        base_path = (
            f"/v1/namespaces/{namespace}/agent-runtimes/{agent_name}/invocations/"
        )
        self.base_url = urljoin(router_url, base_path)

        self.session = create_session(
            pool_connections=pool_connections,
            pool_maxsize=pool_maxsize,
        )

    def _auth_headers(self) -> Dict[str, str]:
        """Return Authorization header dict if auth is available."""
        if not self._auth:
            return {}
        return {"Authorization": f"Bearer {self._auth.get_token()}"}

    def bootstrap_session_id(self) -> str:
        resp = self.session.get(
            self.base_url,
            headers=self._auth_headers(),
            timeout=(self.connect_timeout, self.timeout),
        )
        session_id = resp.headers.get(self.SESSION_HEADER)
        if session_id:
            if resp.status_code >= 400:
                self.logger.debug(
                    f"Bootstrap request returned status {resp.status_code}, "
                    f"but session ID was found: {session_id}"
                )
            return session_id
        resp.raise_for_status()
        content_type = resp.headers.get("Content-Type")
        content_length = resp.headers.get("Content-Length")
        raise ValueError(
            f"Missing required response header: {self.SESSION_HEADER} "
            f"(status: {resp.status_code}, "
            f"content-type: {content_type}, "
            f"content-length: {content_length})"
        )

    def invoke(
        self,
        session_id: str,
        payload: Dict[str, Any],
        timeout: Optional[float] = None,
        path: str = "",
    ) -> requests.Response:
        headers = {
            self.SESSION_HEADER: session_id,
            "Content-Type": "application/json",
        }
        headers.update(self._auth_headers())
        read_timeout = timeout if timeout is not None else self.timeout

        # Ensure path doesn't have leading slash so urljoin works correctly with base_url
        invoke_path = path.lstrip("/")
        url = urljoin(self.base_url, invoke_path) if invoke_path else self.base_url

        self.logger.debug(f"POST {url}")
        return self.session.post(
            url,
            json=payload,
            headers=headers,
            timeout=(self.connect_timeout, read_timeout),
        )

    def close(self) -> None:
        self.session.close()
