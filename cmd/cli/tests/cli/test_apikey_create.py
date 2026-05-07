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

"""CLI-surface tests for `kubectl agentcube apikey create`."""

from __future__ import annotations

import json
import re
from unittest.mock import MagicMock, patch

import pytest
from typer.testing import CliRunner

from agentcube.cli.apikey_commands import apikey_app

runner = CliRunner()


@pytest.fixture
def mock_provider():
    """Patch KubernetesProvider used by apikey_commands."""
    with patch("agentcube.cli.apikey_commands.KubernetesProvider") as cls:
        instance = MagicMock()
        cls.return_value = instance
        # Bootstrap calls succeed.
        instance.verify_namespace_exists.return_value = None

        secret = MagicMock()
        secret.data = {}
        secret.metadata.annotations = {}
        instance.get_or_create_secret.return_value = secret

        configmap = MagicMock()
        configmap.data = {}
        instance.get_or_create_configmap.return_value = configmap

        instance.patch_configmap_data.return_value = MagicMock()
        instance.patch_secret_data.return_value = MagicMock()
        yield instance


def test_create_text_output_emits_raw_key_once(mock_provider):
    result = runner.invoke(apikey_app, ["create", "--namespace", "team-ml"])
    assert result.exit_code == 0, result.stderr
    # Exactly one `e2b_<32 chars>` token in stdout.
    matches = re.findall(r"e2b_[A-Za-z0-9_-]{32}", result.stdout)
    assert len(matches) == 1
    # Hash, namespace, status all rendered.
    assert "Hash:" in result.stdout
    assert "team-ml" in result.stdout
    assert "valid" in result.stdout
    # Warning present.
    assert "WARNING" in result.stdout


def test_create_raw_key_never_on_stderr(mock_provider):
    result = runner.invoke(apikey_app, ["create", "--namespace", "team-ml", "-v"])
    assert result.exit_code == 0
    assert not re.search(r"e2b_[A-Za-z0-9_-]{32}", result.stderr or "")


def test_create_writes_configmap_before_secret(mock_provider):
    runner.invoke(apikey_app, ["create", "--namespace", "team-ml"])
    cm_call = mock_provider.patch_configmap_data.call_args
    secret_call = mock_provider.patch_secret_data.call_args
    # method_calls preserves order across the mock.
    method_names = [c[0] for c in mock_provider.method_calls]
    cm_idx = method_names.index("patch_configmap_data")
    secret_idx = method_names.index("patch_secret_data")
    assert cm_idx < secret_idx, method_names


def test_create_default_namespace_falls_back_to_default(mock_provider, monkeypatch):
    monkeypatch.delenv("E2B_DEFAULT_NAMESPACE", raising=False)
    result = runner.invoke(apikey_app, ["create"])
    assert result.exit_code == 0
    cm_data = mock_provider.patch_configmap_data.call_args.kwargs["data"]
    # Hash maps to "default".
    assert list(cm_data.values()) == ["default"]


def test_create_json_output_shape(mock_provider):
    result = runner.invoke(apikey_app, ["create", "--namespace", "team-ml", "-o", "json"])
    assert result.exit_code == 0
    payload = json.loads(result.stdout)
    assert set(payload.keys()) >= {"api_key", "hash", "namespace", "status", "created"}
    assert payload["namespace"] == "team-ml"
    assert payload["status"] == "valid"
    assert payload["api_key"].startswith("e2b_")


def test_create_invalid_namespace_exits_2(mock_provider):
    result = runner.invoke(apikey_app, ["create", "--namespace", "Bad_NS"])
    assert result.exit_code == 2
    assert "DNS-1123" in result.stderr or "valid" in result.stderr.lower()


def test_create_namespace_missing_exits_1(mock_provider):
    from agentcube.services.k8s_provider import NamespaceNotFoundError
    mock_provider.verify_namespace_exists.side_effect = NamespaceNotFoundError(
        "namespace 'agentcube-system' not found"
    )
    result = runner.invoke(apikey_app, ["create", "--namespace", "team-ml"])
    assert result.exit_code == 1
    assert "agentcube-system" in result.stderr


def test_create_rollback_on_secret_failure(mock_provider):
    from kubernetes.client.rest import ApiException
    mock_provider.patch_secret_data.side_effect = ApiException(
        status=403, reason="forbidden"
    )
    result = runner.invoke(apikey_app, ["create", "--namespace", "team-ml"])
    assert result.exit_code == 1
    # Rollback attempt was made on the ConfigMap key just written.
    mock_provider.remove_configmap_data_key.assert_called_once()


def test_create_description_too_long_exits_2(mock_provider):
    result = runner.invoke(apikey_app, [
        "create", "--namespace", "team-ml", "--description", "x" * 300,
    ])
    assert result.exit_code == 2
    assert "description" in result.stderr.lower()
