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

"""Tests for verify_namespace_exists (apikey commands never auto-create the target namespace)."""

from __future__ import annotations

from unittest.mock import MagicMock, patch

import pytest
from kubernetes.client.rest import ApiException

from agentcube.services.k8s_provider import (
    KubernetesProvider,
    NamespaceNotFoundError,
)


def _provider() -> KubernetesProvider:
    with patch("agentcube.services.k8s_provider.config"), \
         patch("agentcube.services.k8s_provider.client") as mock_client:
        mock_client.CoreV1Api.return_value = MagicMock()
        mock_client.AppsV1Api.return_value = MagicMock()
        return KubernetesProvider(
            namespace="agentcube-system",
            auto_create_namespace=False,
        )


def test_verify_namespace_exists_returns_normally_when_present():
    p = _provider()
    p.core_api.read_namespace.return_value = MagicMock()
    p.verify_namespace_exists("agentcube-system")  # must not raise


def test_verify_namespace_exists_raises_namespace_not_found_on_404():
    p = _provider()
    p.core_api.read_namespace.side_effect = ApiException(status=404, reason="not found")
    with pytest.raises(NamespaceNotFoundError) as exc:
        p.verify_namespace_exists("agentcube-system")
    assert "agentcube-system" in str(exc.value)


def test_verify_namespace_exists_propagates_other_errors():
    p = _provider()
    p.core_api.read_namespace.side_effect = ApiException(status=403, reason="forbidden")
    with pytest.raises(ApiException) as exc:
        p.verify_namespace_exists("agentcube-system")
    assert exc.value.status == 403
