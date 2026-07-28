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

import tempfile
from pathlib import Path
from unittest.mock import patch

import yaml

from agentcube.runtime.build_runtime import BuildRuntime


class TestBuildRuntime:
    """Tests for BuildRuntime local and cloud build options."""

    def _write_yaml(self, path: Path, data: dict):
        with open(path, "w", encoding="utf-8") as f:
            yaml.dump(data, f)

    @patch("agentcube.runtime.build_runtime.DockerService")
    def test_build_local_success(self, MockDockerSvc):
        mock_docker = MockDockerSvc.return_value
        mock_docker.check_docker_available.return_value = True
        mock_docker.build_image.return_value = {
            "image_name": "test-agent:0.0.2",
            "image_id": "1234567890ab",
            "image_size": "50MB",
            "build_time": "5s",
        }

        with tempfile.TemporaryDirectory() as tmpdir:
            ws = Path(tmpdir)
            self._write_yaml(ws / "agent_metadata.yaml", {
                "agent_name": "test-agent",
                "entrypoint": "python main.py",
                "build_mode": "local",
                "version": "0.0.1",
            })
            (ws / "main.py").touch()
            (ws / "requirements.txt").touch()
            (ws / "Dockerfile").touch()

            runtime = BuildRuntime(verbose=True)
            runtime.docker_service = mock_docker

            result = runtime.build(ws)

            assert result["build_mode"] == "local"
            assert result["image_name"] == "test-agent:0.0.2"
            assert result["image_size"] == "50MB"

            # Check that metadata was updated with build details
            metadata = runtime.metadata_service.load_metadata(ws)
            assert metadata.image is not None
            assert metadata.image["build_mode"] == "local"
            assert metadata.image["repository_url"] == "test-agent:0.0.2"

    def test_build_cloud_success(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            ws = Path(tmpdir)
            self._write_yaml(ws / "agent_metadata.yaml", {
                "agent_name": "test-agent",
                "entrypoint": "python main.py",
                "build_mode": "cloud",
                "version": "0.0.2",
            })
            (ws / "main.py").touch()
            (ws / "requirements.txt").touch()
            (ws / "Dockerfile").touch()

            runtime = BuildRuntime(verbose=True)
            result = runtime.build(ws, cloud_provider="huawei", dry_run=True)

            assert result["build_mode"] == "cloud"
            assert result["image_name"] == "swr.cn-east-3.myhuaweicloud.com/agentcube/test-agent:0.0.3"
            assert result["image_tag"] == "0.0.3"
            assert result["dry_run"] is True

            # Check metadata got updated with cloud build mode and dry_run
            metadata = runtime.metadata_service.load_metadata(ws)
            assert metadata.image is not None
            assert metadata.image["build_mode"] == "cloud"
            assert metadata.image["repository_url"] == "swr.cn-east-3.myhuaweicloud.com/agentcube/test-agent:0.0.3"
            assert metadata.image["tag"] == "0.0.3"
            assert metadata.image["dry_run"] is True

    def test_build_cloud_fails_without_dry_run(self):
        import pytest
        with tempfile.TemporaryDirectory() as tmpdir:
            ws = Path(tmpdir)
            self._write_yaml(ws / "agent_metadata.yaml", {
                "agent_name": "test-agent",
                "entrypoint": "python main.py",
                "build_mode": "cloud",
                "version": "0.0.2",
            })
            (ws / "main.py").touch()
            (ws / "requirements.txt").touch()
            (ws / "Dockerfile").touch()

            runtime = BuildRuntime(verbose=True)
            with pytest.raises(ValueError, match="Real cloud builds are not supported yet"):
                runtime.build(ws, cloud_provider="huawei", dry_run=False)

    def test_build_local_dry_run(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            ws = Path(tmpdir)
            self._write_yaml(ws / "agent_metadata.yaml", {
                "agent_name": "test-agent",
                "entrypoint": "python main.py",
                "build_mode": "local",
                "version": "0.0.2",
            })
            (ws / "main.py").touch()
            (ws / "requirements.txt").touch()
            (ws / "Dockerfile").touch()

            runtime = BuildRuntime(verbose=True)
            result = runtime.build(ws, dry_run=True)

            assert result["build_mode"] == "local"
            assert result["dry_run"] is True
            assert result["image_size"] == "10.0MB"
            assert result["build_time"] == "1.0s"

            # Check that metadata has dry_run = True
            metadata = runtime.metadata_service.load_metadata(ws)
            assert metadata.image is not None
            assert metadata.image["dry_run"] is True

    def test_publish_dry_run_image_fails(self):
        import pytest
        from agentcube.runtime.publish_runtime import PublishRuntime
        with tempfile.TemporaryDirectory() as tmpdir:
            ws = Path(tmpdir)
            self._write_yaml(ws / "agent_metadata.yaml", {
                "agent_name": "test-agent",
                "entrypoint": "python main.py",
                "build_mode": "local",
                "version": "0.0.2",
                "image": {
                    "repository_url": "test-agent:0.0.2",
                    "tag": "0.0.2",
                    "build_mode": "local",
                    "build_size": "10MB",
                    "build_time": "1s",
                    "dry_run": True
                }
            })
            (ws / "main.py").touch()
            (ws / "requirements.txt").touch()
            (ws / "Dockerfile").touch()

            runtime = PublishRuntime(verbose=True)
            with pytest.raises(ValueError, match="Cannot publish a dry-run/simulated build image"):
                runtime.publish(ws)

    def test_build_cloud_custom_registry_as_base(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            ws = Path(tmpdir)
            self._write_yaml(ws / "agent_metadata.yaml", {
                "agent_name": "test-agent",
                "entrypoint": "python main.py",
                "build_mode": "cloud",
                "version": "0.0.2",
                "registry_url": "myregistry.com/myproject/",
            })
            (ws / "main.py").touch()
            (ws / "requirements.txt").touch()
            (ws / "Dockerfile").touch()

            runtime = BuildRuntime(verbose=True)
            result = runtime.build(ws, cloud_provider="huawei", dry_run=True)

            assert result["build_mode"] == "cloud"
            assert result["image_name"] == "myregistry.com/myproject/test-agent:0.0.3"
            assert result["image_tag"] == "0.0.3"

            metadata = runtime.metadata_service.load_metadata(ws)
            assert metadata.image is not None
            assert metadata.image["repository_url"] == "myregistry.com/myproject/test-agent:0.0.3"

    def test_build_cloud_custom_registry_with_tag_fails(self):
        import pytest
        with tempfile.TemporaryDirectory() as tmpdir:
            ws = Path(tmpdir)
            self._write_yaml(ws / "agent_metadata.yaml", {
                "agent_name": "test-agent",
                "entrypoint": "python main.py",
                "build_mode": "cloud",
                "version": "0.0.2",
                "registry_url": "myregistry.com/myproject/test-agent:latest",
            })
            (ws / "main.py").touch()
            (ws / "requirements.txt").touch()
            (ws / "Dockerfile").touch()

            runtime = BuildRuntime(verbose=True)
            with pytest.raises(ValueError, match="registry_url must not include an image tag"):
                runtime.build(ws, cloud_provider="huawei", dry_run=True)

    def test_build_cloud_non_huawei_without_registry_url_fails(self):
        import pytest
        with tempfile.TemporaryDirectory() as tmpdir:
            ws = Path(tmpdir)
            self._write_yaml(ws / "agent_metadata.yaml", {
                "agent_name": "test-agent",
                "entrypoint": "python main.py",
                "build_mode": "cloud",
                "version": "0.0.2",
            })
            (ws / "main.py").touch()
            (ws / "requirements.txt").touch()
            (ws / "Dockerfile").touch()

            runtime = BuildRuntime(verbose=True)
            with pytest.raises(ValueError, match="registry_url must be set for non-huawei cloud providers"):
                runtime.build(ws, cloud_provider="aliyun", dry_run=True)
