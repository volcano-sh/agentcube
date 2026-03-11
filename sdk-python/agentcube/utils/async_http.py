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

"""Async HTTP session utilities for AgentCube SDK."""

import httpx


def create_async_session(
    connector_limit: int = 10,
    connector_limit_per_host: int = 10,
) -> httpx.AsyncClient:
    """Create an httpx AsyncClient with connection pooling.

    Args:
        connector_limit: Total number of simultaneous connections (default: 100).
        connector_limit_per_host: Max keepalive connections per host (default: 10).

    Returns:
        A configured httpx.AsyncClient with connection limits.
    """
    limits = httpx.Limits(
        max_connections=connector_limit,
        max_keepalive_connections=connector_limit_per_host,
    )
    return httpx.AsyncClient(limits=limits)
