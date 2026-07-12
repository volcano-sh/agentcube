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

import threading
import time
from typing import Optional, Protocol, Dict, runtime_checkable

import requests

from agentcube.utils.log import get_logger

_REFRESH_BUFFER_SECONDS = 30
_DEFAULT_TOKEN_TIMEOUT = (5.0, 30.0)  # (connect, read)

logger = get_logger(__name__)


@runtime_checkable
class AuthProvider(Protocol):
    """Protocol for pluggable authentication providers."""

    def get_token(self) -> str: ...


class TokenAuth:
    """Wraps a static bearer token."""

    def __init__(self, token: str):
        if not token:
            raise ValueError("Token must not be empty")
        self._token = token

    def get_token(self) -> str:
        return self._token


class ServiceAccountAuth:
    """OAuth2 client_credentials grant with thread-safe token caching."""

    def __init__(
        self,
        token_url: str,
        client_id: str,
        client_secret: str,
        scope: Optional[str] = None,
        headers: Optional[Dict[str, str]] = None,
        timeout: tuple = _DEFAULT_TOKEN_TIMEOUT,
    ):
        self._token_url = token_url
        self._client_id = client_id
        self._client_secret = client_secret
        self._scope = scope
        self._headers = headers or {}
        self._timeout = timeout

        self._lock = threading.Lock()
        self._token: Optional[str] = None
        self._expires_at: float = 0.0

    def get_token(self) -> str:
        with self._lock:
            if self._token and time.monotonic() < self._expires_at:
                return self._token
            return self._fetch_token()

    def _fetch_token(self) -> str:
        data = {
            "grant_type": "client_credentials",
            "client_id": self._client_id,
            "client_secret": self._client_secret,
        }
        if self._scope:
            data["scope"] = self._scope

        logger.debug(f"Fetching token from {self._token_url}")
        resp = requests.post(self._token_url, data=data, headers=self._headers, timeout=self._timeout)
        resp.raise_for_status()

        body = resp.json()
        self._token = body["access_token"]
        expires_in = int(body.get("expires_in", 3600))
        effective = max(expires_in - _REFRESH_BUFFER_SECONDS, expires_in // 2)
        self._expires_at = time.monotonic() + effective
        return self._token or ""
