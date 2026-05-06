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

"""create writes Secret AND ConfigMap; ConfigMap entry lands first."""

from __future__ import annotations

import json
import re

from typer.testing import CliRunner

from agentcube.cli.apikey_commands import apikey_app

runner = CliRunner()


def test_create_writes_both_resources(kubeconfig, ephemeral_namespace, core_api):
    result = runner.invoke(apikey_app, [
        "create",
        "--namespace", "team-int",
        "--secret-namespace", ephemeral_namespace,
        "--kubeconfig", kubeconfig,
        "-o", "json",
    ])
    assert result.exit_code == 0, result.stderr
    payload = json.loads(result.stdout)
    h = payload["hash"]
    assert re.fullmatch(r"[0-9a-f]{64}", h)

    secret = core_api.read_namespaced_secret(
        name="e2b-api-keys", namespace=ephemeral_namespace,
    )
    cm = core_api.read_namespaced_config_map(
        name="e2b-api-key-config", namespace=ephemeral_namespace,
    )

    assert h in (secret.data or {})
    import base64
    assert base64.b64decode(secret.data[h]).decode() == "valid"
    assert (cm.data or {}).get(h) == "team-int"

    assert int(cm.metadata.resource_version) <= int(secret.metadata.resource_version)
