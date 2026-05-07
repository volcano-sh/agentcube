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

"""First-run bootstrap creates labelled Secret + ConfigMap."""

from __future__ import annotations

from typer.testing import CliRunner

from agentcube.cli.apikey_commands import apikey_app

runner = CliRunner()

EXPECTED_LABELS = {
    "app.kubernetes.io/managed-by": "kubectl-agentcube",
    "app.kubernetes.io/component": "e2b-api-keys",
}


def test_bootstrap_creates_labelled_resources(kubeconfig, ephemeral_namespace, core_api):
    result = runner.invoke(apikey_app, [
        "create",
        "--namespace", "team-int",
        "--secret-namespace", ephemeral_namespace,
        "--kubeconfig", kubeconfig,
    ])
    assert result.exit_code == 0, result.stderr

    secret = core_api.read_namespaced_secret(
        name="e2b-api-keys", namespace=ephemeral_namespace,
    )
    cm = core_api.read_namespaced_config_map(
        name="e2b-api-key-config", namespace=ephemeral_namespace,
    )

    for k, v in EXPECTED_LABELS.items():
        assert secret.metadata.labels.get(k) == v
        assert cm.metadata.labels.get(k) == v
