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

"""Verify `apikey` is registered on the top-level Typer app."""

from __future__ import annotations

from typer.testing import CliRunner

from agentcube.cli.main import app

runner = CliRunner()


def test_apikey_help_via_top_level_app():
    result = runner.invoke(app, ["apikey", "--help"])
    assert result.exit_code == 0, result.stderr
    assert "create" in result.stdout
    assert "list" in result.stdout
    assert "revoke" in result.stdout


def test_top_level_app_help_lists_apikey():
    result = runner.invoke(app, ["--help"])
    assert result.exit_code == 0
    assert "apikey" in result.stdout
