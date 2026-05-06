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

"""Re-running revoke on an already-revoked key exits 0 with changed=false."""

from __future__ import annotations

import json

from typer.testing import CliRunner

from agentcube.cli.apikey_commands import apikey_app

runner = CliRunner()


def test_revoke_is_idempotent(kubeconfig, ephemeral_namespace, core_api):
    create = runner.invoke(apikey_app, [
        "create",
        "--namespace", "team-int",
        "--secret-namespace", ephemeral_namespace,
        "--kubeconfig", kubeconfig,
        "-o", "json",
    ])
    assert create.exit_code == 0
    h = json.loads(create.stdout)["hash"]

    first = runner.invoke(apikey_app, [
        "revoke", h, "--force",
        "--secret-namespace", ephemeral_namespace,
        "--kubeconfig", kubeconfig,
        "-o", "json",
    ])
    assert first.exit_code == 0
    assert json.loads(first.stdout)["changed"] is True

    second = runner.invoke(apikey_app, [
        "revoke", h, "--force",
        "--secret-namespace", ephemeral_namespace,
        "--kubeconfig", kubeconfig,
        "-o", "json",
    ])
    assert second.exit_code == 0
    payload = json.loads(second.stdout)
    assert payload["changed"] is False
    assert payload["status"] == "revoked"
