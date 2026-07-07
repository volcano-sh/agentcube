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
import requests
from typing import Dict, Any, Optional

from agentcube.utils.log import get_logger
from agentcube.utils.utils import read_token_from_file
from agentcube.utils.http import create_session

class ControlPlaneClient:
    """Client for AgentCube Control Plane (WorkloadManager).
    Handles creation and deletion of Code Interpreter sessions.
    """

    def __init__(
        self,
        workload_manager_url: Optional[str] = None,
        auth_token: Optional[str] = None,
        timeout: int = 120,
        connect_timeout: float = 5.0,
        pool_connections: int = 10,
        pool_maxsize: int = 10,
    ):
        # ... existing __init__ code ...

    def create_session(
        self,
        name: str = "my-interpreter",
        namespace: str = "default",
        metadata: Optional[Dict[str, Any]] = None,
        ttl: int = 3600,
    ) -> str:
        # ... existing create_session code ...

    def delete_session(self, session_id: str) -> bool:
        # ... existing delete_session code ...

    def delete_agent_runtime_session(self, session_id: str) -> Dict[str, Any]:
        """
        Delete an agent runtime session from the server.

        Args:
            session_id: The ID of the session to delete

        Returns:
            The response from the server (empty dict for 204)

        Raises:
            HTTPError: If the request fails
        """
        url = f"{self.base_url}/v1/agent-runtime/sessions/{session_id}"
        self.logger.debug(f"Deleting agent runtime session {session_id} at {url}")

        try:
            response = self.session.delete(
                url,
                timeout=(self.connect_timeout, self.timeout)
            )
            if response.status_code == 404:
                return {}  # Already gone
            response.raise_for_status()
            # Handle empty response for 200 OK
            if response.status_code == 200 and response.text.strip():
                try:
                    return response.json()
                except ValueError:
                    return {}
            return {}
        except requests.exceptions.RequestException as e:
            self.logger.error(f"Failed to delete agent runtime session {session_id}: {e}")
            if e.response is not None:
                self.logger.error(f"Server response: {e.response.text}")
            raise

    def close(self):
        """Close the underlying session and release connection pool resources."""
        self.session.close()