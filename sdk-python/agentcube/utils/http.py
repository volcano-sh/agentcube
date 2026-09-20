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

"""HTTP session utilities for AgentCube SDK."""

import requests
from requests.adapters import HTTPAdapter

from agentcube.exceptions import SessionNotFoundError


SESSION_NOT_FOUND_CODE = "SESSION_NOT_FOUND"


def create_session(
    pool_connections: int = 10,
    pool_maxsize: int = 10,
) -> requests.Session:
    """Create a requests Session with connection pooling.

    Args:
        pool_connections: Number of connection pools to cache (default: 10).
        pool_maxsize: Maximum connections per pool (default: 10).

    Returns:
        A configured requests.Session object with connection pooling.
    """
    session = requests.Session()

    adapter = HTTPAdapter(
        pool_connections=pool_connections,
        pool_maxsize=pool_maxsize,
    )

    session.mount("http://", adapter)
    session.mount("https://", adapter)

    return session


def raise_for_session_status(response: requests.Response, session_id: str) -> None:
    """Raise a typed error for a missing session, or the normal HTTP error."""
    if response.status_code == 404:
        try:
            payload = response.json()
        except (TypeError, ValueError):
            payload = {}
        if isinstance(payload, dict) and payload.get("code") == SESSION_NOT_FOUND_CODE:
            raise SessionNotFoundError(
                session_id=session_id,
                message=payload.get("error"),
                response=response,
            )
    response.raise_for_status()
