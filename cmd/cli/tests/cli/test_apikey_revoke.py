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

"""CLI-surface tests for `kubectl agentcube apikey revoke`."""

from __future__ import annotations

import json
from unittest.mock import MagicMock, patch

import pytest
from typer.testing import CliRunner

from agentcube.cli.apikey_commands import apikey_app

HASH_A = "abcd1234" + "0" * 56
HASH_B = "abcd1234" + "f" * 56
HASH_C = "deadbeef" + "0" * 56

runner = CliRunner()


@pytest.fixture
def revoke_provider():
    with patch("agentcube.cli.apikey_commands.KubernetesProvider") as cls:
        instance = MagicMock()
        cls.return_value = instance
        instance.verify_namespace_exists.return_value = None

        secret = MagicMock()
        secret.metadata.annotations = {}
        instance.get_or_create_secret.return_value = secret

        configmap = MagicMock()
        configmap.data = {}
        instance.get_or_create_configmap.return_value = configmap

        instance.read_secret_decoded_data.return_value = {
            HASH_A: "valid",
            HASH_B: "valid",
            HASH_C: "valid",
        }
        yield instance


def test_revoke_unique_prefix_flips_secret(revoke_provider):
    result = runner.invoke(apikey_app, ["revoke", "deadbeef", "--force"])
    assert result.exit_code == 0, result.stderr
    call = revoke_provider.patch_secret_data.call_args
    assert call.kwargs["data"] == {HASH_C: "revoked"}


def test_revoke_full_hash_works(revoke_provider):
    result = runner.invoke(apikey_app, ["revoke", HASH_A, "--force"])
    assert result.exit_code == 0
    call = revoke_provider.patch_secret_data.call_args
    assert call.kwargs["data"] == {HASH_A: "revoked"}


def test_revoke_ambiguous_exits_1_and_lists_candidates(revoke_provider):
    result = runner.invoke(apikey_app, ["revoke", "abcd1234", "--force"])
    assert result.exit_code == 1
    assert HASH_A in result.stderr
    assert HASH_B in result.stderr
    revoke_provider.patch_secret_data.assert_not_called()


def test_revoke_no_match_exits_1(revoke_provider):
    result = runner.invoke(apikey_app, ["revoke", "ffffffff", "--force"])
    assert result.exit_code == 1
    assert "no key matches" in result.stderr.lower()
    revoke_provider.patch_secret_data.assert_not_called()


def test_revoke_idempotent_on_already_revoked(revoke_provider):
    # Re-configure the fixture so HASH_C is already revoked.
    revoke_provider.read_secret_decoded_data.return_value = {
        HASH_A: "valid",
        HASH_B: "valid",
        HASH_C: "revoked",
    }
    result = runner.invoke(apikey_app, ["revoke", "deadbeef", "--force"])
    assert result.exit_code == 0
    revoke_provider.patch_secret_data.assert_not_called()
    assert "already revoked" in result.stdout.lower()


def test_revoke_idempotent_json_includes_changed_false(revoke_provider):
    revoke_provider.read_secret_decoded_data.return_value = {HASH_C: "revoked"}
    result = runner.invoke(apikey_app, ["revoke", HASH_C, "--force", "-o", "json"])
    assert result.exit_code == 0
    payload = json.loads(result.stdout)
    assert payload["changed"] is False
    assert payload["status"] == "revoked"


def test_revoke_invalid_prefix_exits_2(revoke_provider):
    result = runner.invoke(apikey_app, ["revoke", "abc", "--force"])
    assert result.exit_code == 2


def test_revoke_uppercase_prefix_exits_2(revoke_provider):
    result = runner.invoke(apikey_app, ["revoke", "ABCD1234", "--force"])
    assert result.exit_code == 2


def test_revoke_ambiguous_json_includes_candidates(revoke_provider):
    result = runner.invoke(
        apikey_app, ["revoke", "abcd1234", "--force", "-o", "json"],
    )
    assert result.exit_code == 1
    payload = json.loads(result.stdout)
    assert set(payload["candidates"]) == {HASH_A, HASH_B}


def test_revoke_no_force_aborts_when_user_declines():
    # With stdin closed, Typer.confirm should abort -> exit 1.
    with patch("agentcube.cli.apikey_commands.KubernetesProvider") as cls:
        instance = MagicMock()
        cls.return_value = instance
        instance.verify_namespace_exists.return_value = None
        instance.get_or_create_secret.return_value = MagicMock(metadata=MagicMock(annotations={}))
        instance.get_or_create_configmap.return_value = MagicMock(data={})
        instance.read_secret_decoded_data.return_value = {HASH_C: "valid"}
        result = runner.invoke(apikey_app, ["revoke", HASH_C], input="n\n")
        # Typer treats `n` to a confirm() with abort=True as Aborted (exit 1).
        assert result.exit_code == 1
        instance.patch_secret_data.assert_not_called()
