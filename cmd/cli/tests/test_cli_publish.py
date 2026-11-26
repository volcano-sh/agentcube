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

from typer.testing import CliRunner

from agentcube.cli.main import app


def test_publish_endpoint_option_is_forwarded(monkeypatch, tmp_path):
    call = {}

    class StubPublishRuntime:
        def __init__(self, verbose=False, provider="agentcube"):
            call["verbose"] = verbose
            call["provider"] = provider

        def publish(self, workspace_path, **options):
            call["workspace_path"] = workspace_path
            call["options"] = options
            return {
                "agent_name": "test-agent",
                "agent_id": "test-agent",
                "agent_endpoint": options["agent_endpoint"],
                "namespace": options.get("namespace", "default"),
                "status": "deployed",
            }

    monkeypatch.setattr("agentcube.cli.main.PublishRuntime", StubPublishRuntime)

    result = CliRunner().invoke(
        app,
        [
            "publish",
            "-f",
            str(tmp_path),
            "--endpoint",
            "http://router.example.com",
        ],
    )

    assert result.exit_code == 0, result.output
    assert call["provider"] == "agentcube"
    assert call["workspace_path"] == tmp_path.resolve()
    assert call["options"]["agent_endpoint"] == "http://router.example.com"
