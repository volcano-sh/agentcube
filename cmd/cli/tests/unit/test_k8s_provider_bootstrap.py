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

"""Tests for KubernetesProvider bootstrap helpers."""

from __future__ import annotations

from unittest.mock import MagicMock, patch

import pytest
from kubernetes.client.rest import ApiException

from agentcube.services.k8s_provider import KubernetesProvider


@pytest.fixture
def provider() -> KubernetesProvider:
    """A KubernetesProvider with API loading + namespace check stubbed out."""
    with patch("agentcube.services.k8s_provider.config"), \
         patch("agentcube.services.k8s_provider.client") as mock_client:
        mock_client.CoreV1Api.return_value = MagicMock()
        mock_client.AppsV1Api.return_value = MagicMock()
        p = KubernetesProvider(
            namespace="agentcube-system",
            verbose=False,
            kubeconfig=None,
            auto_create_namespace=False,
        )
    return p


def _api_exc(status: int) -> ApiException:
    e = ApiException(status=status, reason=f"HTTP {status}")
    return e


# --- get_or_create_secret ---

def test_get_or_create_secret_returns_existing(provider):
    mock_secret = MagicMock()
    provider.core_api.read_namespaced_secret.return_value = mock_secret
    out = provider.get_or_create_secret("agentcube-system", "e2b-api-keys")
    assert out is mock_secret
    provider.core_api.create_namespaced_secret.assert_not_called()


def test_get_or_create_secret_creates_on_404(provider):
    provider.core_api.read_namespaced_secret.side_effect = _api_exc(404)
    created = MagicMock()
    provider.core_api.create_namespaced_secret.return_value = created
    out = provider.get_or_create_secret("agentcube-system", "e2b-api-keys")
    assert out is created
    body = provider.core_api.create_namespaced_secret.call_args.kwargs["body"]
    assert body.metadata.name == "e2b-api-keys"
    assert body.metadata.labels == {
        "app.kubernetes.io/managed-by": "kubectl-agentcube",
        "app.kubernetes.io/component": "e2b-api-keys",
    }


def test_get_or_create_secret_propagates_other_errors(provider):
    provider.core_api.read_namespaced_secret.side_effect = _api_exc(403)
    with pytest.raises(ApiException) as exc:
        provider.get_or_create_secret("agentcube-system", "e2b-api-keys")
    assert exc.value.status == 403


# --- get_or_create_configmap ---

def test_get_or_create_configmap_returns_existing(provider):
    mock_cm = MagicMock()
    provider.core_api.read_namespaced_config_map.return_value = mock_cm
    out = provider.get_or_create_configmap("agentcube-system", "e2b-api-key-config")
    assert out is mock_cm
    provider.core_api.create_namespaced_config_map.assert_not_called()


def test_get_or_create_configmap_creates_on_404(provider):
    provider.core_api.read_namespaced_config_map.side_effect = _api_exc(404)
    created = MagicMock()
    provider.core_api.create_namespaced_config_map.return_value = created
    out = provider.get_or_create_configmap("agentcube-system", "e2b-api-key-config")
    assert out is created
    body = provider.core_api.create_namespaced_config_map.call_args.kwargs["body"]
    assert body.metadata.name == "e2b-api-key-config"
    assert body.metadata.labels == {
        "app.kubernetes.io/managed-by": "kubectl-agentcube",
        "app.kubernetes.io/component": "e2b-api-keys",
    }
