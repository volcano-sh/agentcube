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
