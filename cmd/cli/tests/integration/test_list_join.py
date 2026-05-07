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

"""list surfaces drift between Secret and ConfigMap as orphan rows."""

from __future__ import annotations

import json

from kubernetes import client
from typer.testing import CliRunner

from agentcube.cli.apikey_commands import apikey_app

runner = CliRunner()

HASH_ORPHAN_NO_CM = "e" * 64
HASH_ORPHAN_NO_SEC = "f" * 64


def test_list_surfaces_orphans(kubeconfig, ephemeral_namespace, core_api):
    secret_body = client.V1Secret(
        metadata=client.V1ObjectMeta(name="e2b-api-keys", namespace=ephemeral_namespace),
        string_data={HASH_ORPHAN_NO_CM: "valid"},
        type="Opaque",
    )
    cm_body = client.V1ConfigMap(
        metadata=client.V1ObjectMeta(name="e2b-api-key-config", namespace=ephemeral_namespace),
        data={HASH_ORPHAN_NO_SEC: "team-x"},
    )
    core_api.create_namespaced_secret(namespace=ephemeral_namespace, body=secret_body)
    core_api.create_namespaced_config_map(namespace=ephemeral_namespace, body=cm_body)

    result = runner.invoke(apikey_app, [
        "list",
        "--status", "all",
        "--secret-namespace", ephemeral_namespace,
        "--kubeconfig", kubeconfig,
        "-o", "json",
    ])
    assert result.exit_code == 0, result.stderr
    rows = json.loads(result.stdout)
    by_hash = {r["hash"]: r for r in rows}

    assert "no namespace" in by_hash[HASH_ORPHAN_NO_CM]["status"].lower()
    assert "no secret" in by_hash[HASH_ORPHAN_NO_SEC]["status"].lower()
