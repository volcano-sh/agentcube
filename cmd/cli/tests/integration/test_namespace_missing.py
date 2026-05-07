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

"""list against a non-existent namespace exits 1 with the documented hint."""

from __future__ import annotations

from typer.testing import CliRunner

from agentcube.cli.apikey_commands import apikey_app

runner = CliRunner()


def test_list_with_missing_namespace_exits_1(kubeconfig):
    result = runner.invoke(apikey_app, [
        "list",
        "--secret-namespace", "definitely-does-not-exist-12345",
        "--kubeconfig", kubeconfig,
    ])
    assert result.exit_code == 1
    assert "definitely-does-not-exist-12345" in result.stderr
    assert "not found" in result.stderr.lower()
