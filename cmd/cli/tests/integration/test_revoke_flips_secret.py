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

"""revoke flips Secret status; ConfigMap entry untouched."""

from __future__ import annotations

import base64
import json

from typer.testing import CliRunner

from agentcube.cli.apikey_commands import apikey_app

runner = CliRunner()


def test_revoke_changes_status_keeping_configmap(kubeconfig, ephemeral_namespace, core_api):
    create = runner.invoke(apikey_app, [
        "create",
        "--namespace", "team-int",
        "--secret-namespace", ephemeral_namespace,
        "--kubeconfig", kubeconfig,
        "-o", "json",
    ])
    assert create.exit_code == 0
    h = json.loads(create.stdout)["hash"]

    revoke = runner.invoke(apikey_app, [
        "revoke", h, "--force",
        "--secret-namespace", ephemeral_namespace,
        "--kubeconfig", kubeconfig,
    ])
    assert revoke.exit_code == 0, revoke.stderr

    secret = core_api.read_namespaced_secret(
        name="e2b-api-keys", namespace=ephemeral_namespace,
    )
    cm = core_api.read_namespaced_config_map(
        name="e2b-api-key-config", namespace=ephemeral_namespace,
    )
    assert base64.b64decode(secret.data[h]).decode() == "revoked"
    assert (cm.data or {}).get(h) == "team-int"
