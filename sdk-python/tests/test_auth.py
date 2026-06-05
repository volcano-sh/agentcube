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

import time
import unittest
from concurrent.futures import ThreadPoolExecutor
from unittest.mock import Mock, patch

from agentcube.auth import AuthProvider, ServiceAccountAuth, TokenAuth


class TestTokenAuth(unittest.TestCase):

    def test_token_auth_get_token(self):
        auth = TokenAuth("my-secret-token")
        self.assertEqual(auth.get_token(), "my-secret-token")

    def test_token_auth_empty_raises(self):
        with self.assertRaises(ValueError):
            TokenAuth("")


class TestServiceAccountAuth(unittest.TestCase):

    def _mock_response(self, access_token="tok-123", expires_in=3600, status_code=200):
        resp = Mock()
        resp.status_code = status_code
        resp.json.return_value = {
            "access_token": access_token,
            "expires_in": expires_in,
        }
        resp.raise_for_status = Mock()
        return resp

    @patch("agentcube.auth.requests.post")
    def test_service_account_auth_initial_fetch(self, mock_post):
        mock_post.return_value = self._mock_response()

        auth = ServiceAccountAuth(
            token_url="https://idp.example.com/token",
            client_id="cid",
            client_secret="csecret",
        )
        token = auth.get_token()

        self.assertEqual(token, "tok-123")
        mock_post.assert_called_once()

    @patch("agentcube.auth.requests.post")
    def test_service_account_auth_caches_token(self, mock_post):
        mock_post.return_value = self._mock_response()

        auth = ServiceAccountAuth(
            token_url="https://idp.example.com/token",
            client_id="cid",
            client_secret="csecret",
        )
        auth.get_token()
        auth.get_token()

        # Only one HTTP call despite two get_token() calls
        mock_post.assert_called_once()

    @patch("agentcube.auth.time.monotonic")
    @patch("agentcube.auth.requests.post")
    def test_service_account_auth_refreshes_expired(self, mock_post, mock_monotonic):
        mock_post.return_value = self._mock_response(expires_in=60)
        # First call at t=0, cached until t=30 (60 - 30s buffer)
        mock_monotonic.return_value = 0.0

        auth = ServiceAccountAuth(
            token_url="https://idp.example.com/token",
            client_id="cid",
            client_secret="csecret",
        )
        auth.get_token()
        self.assertEqual(mock_post.call_count, 1)

        # Advance past expiry (beyond the 30s buffer)
        mock_monotonic.return_value = 31.0
        mock_post.return_value = self._mock_response(access_token="tok-456")

        token = auth.get_token()
        self.assertEqual(token, "tok-456")
        self.assertEqual(mock_post.call_count, 2)

    @patch("agentcube.auth.requests.post")
    def test_service_account_auth_thread_safety(self, mock_post):
        mock_post.return_value = self._mock_response()

        auth = ServiceAccountAuth(
            token_url="https://idp.example.com/token",
            client_id="cid",
            client_secret="csecret",
        )

        with ThreadPoolExecutor(max_workers=8) as pool:
            results = list(pool.map(lambda _: auth.get_token(), range(50)))

        # All threads should get the same cached token
        self.assertTrue(all(t == "tok-123" for t in results))

    @patch("agentcube.auth.requests.post")
    def test_service_account_auth_http_error(self, mock_post):
        from requests.exceptions import HTTPError
        resp = Mock()
        resp.status_code = 401
        resp.raise_for_status.side_effect = HTTPError(response=resp)
        mock_post.return_value = resp

        auth = ServiceAccountAuth(
            token_url="https://idp.example.com/token",
            client_id="cid",
            client_secret="csecret",
        )
        with self.assertRaises(HTTPError):
            auth.get_token()


class TestAuthProviderProtocol(unittest.TestCase):

    def test_auth_provider_protocol(self):
        token_auth = TokenAuth("abc")
        sa_auth = ServiceAccountAuth(
            token_url="https://idp.example.com/token",
            client_id="cid",
            client_secret="csecret",
        )
        self.assertIsInstance(token_auth, AuthProvider)
        self.assertIsInstance(sa_auth, AuthProvider)


if __name__ == "__main__":
    unittest.main()
