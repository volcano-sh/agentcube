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

"""Tests for patch_secret_data, patch_configmap_data, remove_configmap_data_key."""

from __future__ import annotations

import json
from unittest.mock import MagicMock, patch

import pytest
from kubernetes.client.rest import ApiException

from agentcube.services.k8s_provider import KubernetesProvider


@pytest.fixture
def provider() -> KubernetesProvider:
    with patch("agentcube.services.k8s_provider.config"), \
         patch("agentcube.services.k8s_provider.client") as mock_client:
        mock_client.CoreV1Api.return_value = MagicMock()
        mock_client.AppsV1Api.return_value = MagicMock()
        p = KubernetesProvider(
            namespace="agentcube-system",
            verbose=False,
            auto_create_namespace=False,
        )
    return p


def _api_exc(status: int) -> ApiException:
    return ApiException(status=status, reason=f"HTTP {status}")


# --- patch_secret_data ---

def test_patch_secret_data_uses_string_data(provider):
    provider.core_api.patch_namespaced_secret.return_value = MagicMock()
    provider.patch_secret_data(
        namespace="agentcube-system",
        name="e2b-api-keys",
        data={"abc": "valid"},
        annotations={"apikey.agentcube.io/metadata": "{}"},
    )
    body = provider.core_api.patch_namespaced_secret.call_args.kwargs["body"]
    assert body["stringData"] == {"abc": "valid"}
    assert body["metadata"]["annotations"] == {
        "apikey.agentcube.io/metadata": "{}"
    }


def test_patch_secret_data_403_raises_apiexception_unmodified(provider):
    provider.core_api.patch_namespaced_secret.side_effect = _api_exc(403)
    with pytest.raises(ApiException) as exc:
        provider.patch_secret_data(
            "agentcube-system", "e2b-api-keys", data={"abc": "valid"}, annotations={},
        )
    assert exc.value.status == 403


def test_patch_secret_data_409_retries_once_then_raises(provider):
    provider.core_api.patch_namespaced_secret.side_effect = [
        _api_exc(409), _api_exc(409),
    ]
    with pytest.raises(ApiException) as exc:
        provider.patch_secret_data(
            "agentcube-system", "e2b-api-keys", data={"abc": "valid"}, annotations={},
        )
    assert exc.value.status == 409
    assert provider.core_api.patch_namespaced_secret.call_count == 2


def test_patch_secret_data_409_then_success(provider):
    success = MagicMock()
    provider.core_api.patch_namespaced_secret.side_effect = [_api_exc(409), success]
    out = provider.patch_secret_data(
        "agentcube-system", "e2b-api-keys", data={"abc": "valid"}, annotations={},
    )
    assert out is success
    assert provider.core_api.patch_namespaced_secret.call_count == 2


# --- patch_configmap_data ---

def test_patch_configmap_data_uses_string_data(provider):
    provider.core_api.patch_namespaced_config_map.return_value = MagicMock()
    provider.patch_configmap_data(
        namespace="agentcube-system",
        name="e2b-api-key-config",
        data={"abc": "team-ml"},
    )
    body = provider.core_api.patch_namespaced_config_map.call_args.kwargs["body"]
    assert body["data"] == {"abc": "team-ml"}


# --- remove_configmap_data_key ---

def test_remove_configmap_data_key_uses_strategic_merge_patch(provider):
    provider.core_api.patch_namespaced_config_map.return_value = MagicMock()
    provider.remove_configmap_data_key(
        namespace="agentcube-system",
        name="e2b-api-key-config",
        key="abc",
    )
    body = provider.core_api.patch_namespaced_config_map.call_args.kwargs["body"]
    # Strategic merge patch: setting key to None deletes it on PATCH.
    assert body == {"data": {"abc": None}}


def test_remove_configmap_data_key_swallows_404(provider):
    provider.core_api.patch_namespaced_config_map.side_effect = _api_exc(404)
    # Best-effort rollback: 404 means the key/cm is already gone.
    provider.remove_configmap_data_key(
        namespace="agentcube-system",
        name="e2b-api-key-config",
        key="abc",
    )  # must not raise


def test_remove_configmap_data_key_raises_on_other_errors(provider):
    provider.core_api.patch_namespaced_config_map.side_effect = _api_exc(500)
    with pytest.raises(ApiException):
        provider.remove_configmap_data_key("agentcube-system", "e2b-api-key-config", "abc")
