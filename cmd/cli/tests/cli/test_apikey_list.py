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

"""CLI-surface tests for `kubectl agentcube apikey list`."""

from __future__ import annotations

import json
from unittest.mock import MagicMock, patch

import pytest
from typer.testing import CliRunner

from agentcube.cli.apikey_commands import apikey_app


HASH_VALID_ML = "a" * 64
HASH_REVOKED_ML = "b" * 64
HASH_VALID_PROD = "c" * 64
HASH_ORPHAN_NO_NS = "d" * 64        # in Secret only
HASH_ORPHAN_NO_SECRET = "e" * 64    # in ConfigMap only

runner = CliRunner()


@pytest.fixture
def populated_provider():
    """Patch KubernetesProvider with a Secret + ConfigMap holding 5 entries."""
    with patch("agentcube.cli.apikey_commands.KubernetesProvider") as cls:
        instance = MagicMock()
        cls.return_value = instance
        instance.verify_namespace_exists.return_value = None

        secret = MagicMock()
        secret.data = {
            HASH_VALID_ML: "dmFsaWQ=",
            HASH_REVOKED_ML: "cmV2b2tlZA==",
            HASH_VALID_PROD: "dmFsaWQ=",
            HASH_ORPHAN_NO_NS: "dmFsaWQ=",
        }
        secret.metadata.annotations = {
            "apikey.agentcube.io/metadata": json.dumps({
                HASH_VALID_ML: {"created": "2026-01-01T00:00:00Z", "description": "ml"},
                HASH_REVOKED_ML: {"created": "2026-02-01T00:00:00Z", "description": "ml-old"},
                HASH_VALID_PROD: {"created": "2026-03-01T00:00:00Z", "description": "prod"},
            }),
        }
        instance.get_or_create_secret.return_value = secret

        instance.read_secret_decoded_data.return_value = {
            HASH_VALID_ML: "valid",
            HASH_REVOKED_ML: "revoked",
            HASH_VALID_PROD: "valid",
            HASH_ORPHAN_NO_NS: "valid",
        }

        configmap = MagicMock()
        configmap.data = {
            HASH_VALID_ML: "team-ml",
            HASH_REVOKED_ML: "team-ml",
            HASH_VALID_PROD: "team-prod",
            HASH_ORPHAN_NO_SECRET: "team-ml",
            "defaultNamespace": "default",
        }
        instance.get_or_create_configmap.return_value = configmap
        yield instance


def test_list_default_status_filter_is_valid(populated_provider):
    result = runner.invoke(apikey_app, ["list", "-o", "json"])
    assert result.exit_code == 0, result.stderr
    rows = json.loads(result.stdout)
    statuses = {r["status"] for r in rows}
    assert "revoked" not in statuses


def test_list_status_all_includes_everything(populated_provider):
    result = runner.invoke(apikey_app, ["list", "--status", "all", "-o", "json"])
    assert result.exit_code == 0
    rows = json.loads(result.stdout)
    hashes = {r["hash"] for r in rows}
    assert HASH_VALID_ML in hashes
    assert HASH_REVOKED_ML in hashes
    assert HASH_VALID_PROD in hashes
    assert HASH_ORPHAN_NO_NS in hashes
    assert HASH_ORPHAN_NO_SECRET in hashes


def test_list_namespace_filter(populated_provider):
    result = runner.invoke(apikey_app, [
        "list", "--namespace", "team-ml", "--status", "all", "-o", "json",
    ])
    assert result.exit_code == 0
    rows = json.loads(result.stdout)
    namespaces = {r["namespace"] for r in rows}
    assert namespaces == {"team-ml"}


def test_list_orphans_surface_with_explanatory_status(populated_provider):
    result = runner.invoke(apikey_app, ["list", "--status", "all", "-o", "json"])
    rows = json.loads(result.stdout)
    by_hash = {r["hash"]: r for r in rows}
    assert "orphaned" in by_hash[HASH_ORPHAN_NO_NS]["status"].lower()
    assert "no namespace" in by_hash[HASH_ORPHAN_NO_NS]["status"].lower()
    assert "orphaned" in by_hash[HASH_ORPHAN_NO_SECRET]["status"].lower()
    assert "no secret" in by_hash[HASH_ORPHAN_NO_SECRET]["status"].lower()


def test_list_excludes_default_namespace_sentinel(populated_provider):
    result = runner.invoke(apikey_app, ["list", "--status", "all", "-o", "json"])
    rows = json.loads(result.stdout)
    hashes = {r["hash"] for r in rows}
    assert "defaultNamespace" not in hashes


def test_list_table_output_shows_columns(populated_provider):
    result = runner.invoke(apikey_app, ["list"])
    assert result.exit_code == 0
    for col in ("HASH", "NAMESPACE", "STATUS", "CREATED", "DESCRIPTION"):
        assert col in result.stdout, result.stdout


def test_list_invalid_status_exits_2(populated_provider):
    result = runner.invoke(apikey_app, ["list", "--status", "garbage"])
    assert result.exit_code == 2


def test_list_empty_secret_emits_empty_table(populated_provider):
    populated_provider.read_secret_decoded_data.return_value = {}
    populated_provider.get_or_create_configmap.return_value.data = {
        "defaultNamespace": "default"
    }
    result = runner.invoke(apikey_app, ["list", "--status", "all", "-o", "json"])
    assert result.exit_code == 0
    assert json.loads(result.stdout) == []
