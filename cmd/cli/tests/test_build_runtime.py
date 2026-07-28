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

            # Check metadata on disk remains unchanged (side-effect free)
            metadata_on_disk = runtime.metadata_service.load_metadata(ws)
            assert metadata_on_disk.version == "0.0.2"
            assert metadata_on_disk.image is None

    def test_build_cloud_success_without_dry_run(self):
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
            result = runtime.build(ws, cloud_provider="huawei", dry_run=False)

            assert result["build_mode"] == "cloud"
            assert result["image_name"] == "swr.cn-east-3.myhuaweicloud.com/agentcube/test-agent:0.0.3"
            assert result["image_tag"] == "0.0.3"
            assert "dry_run" not in result

            # Check that metadata on disk got updated
            metadata_on_disk = runtime.metadata_service.load_metadata(ws)
            assert metadata_on_disk.version == "0.0.3"
            assert metadata_on_disk.image is not None
            assert metadata_on_disk.image["build_mode"] == "cloud"
            assert metadata_on_disk.image["repository_url"] == "swr.cn-east-3.myhuaweicloud.com/agentcube/test-agent:0.0.3"
            assert "dry_run" not in metadata_on_disk.image

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

            # Check that metadata on disk remains unchanged (side-effect free)
            metadata_on_disk = runtime.metadata_service.load_metadata(ws)
            assert metadata_on_disk.version == "0.0.2"
            assert metadata_on_disk.image is None

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
            with pytest.raises(ValueError, match=r"Cannot publish a dry-run/simulated build image\. Please run a real build \(local or cloud build without --dry-run\) before publishing\."):
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

            metadata_on_disk = runtime.metadata_service.load_metadata(ws)
            assert metadata_on_disk.image is None
            assert metadata_on_disk.version == "0.0.2"

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

    def test_build_cloud_custom_registry_with_port(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            ws = Path(tmpdir)
            self._write_yaml(ws / "agent_metadata.yaml", {
                "agent_name": "test-agent",
                "entrypoint": "python main.py",
                "build_mode": "cloud",
                "version": "0.0.2",
                "registry_url": "localhost:5000",
            })
            (ws / "main.py").touch()
            (ws / "requirements.txt").touch()
            (ws / "Dockerfile").touch()

            runtime = BuildRuntime(verbose=True)
            result = runtime.build(ws, cloud_provider="huawei", dry_run=True)

            assert result["build_mode"] == "cloud"
            assert result["image_name"] == "localhost:5000/test-agent:0.0.3"

    def test_build_cloud_custom_registry_with_port_and_namespace(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            ws = Path(tmpdir)
            self._write_yaml(ws / "agent_metadata.yaml", {
                "agent_name": "test-agent",
                "entrypoint": "python main.py",
                "build_mode": "cloud",
                "version": "0.0.2",
                "registry_url": "localhost:5000/myproject",
            })
            (ws / "main.py").touch()
            (ws / "requirements.txt").touch()
            (ws / "Dockerfile").touch()

            runtime = BuildRuntime(verbose=True)
            result = runtime.build(ws, cloud_provider="huawei", dry_run=True)

            assert result["build_mode"] == "cloud"
            assert result["image_name"] == "localhost:5000/myproject/test-agent:0.0.3"

    def test_publish_uses_metadata_image_build_mode(self):
        from agentcube.runtime.publish_runtime import PublishRuntime
        from unittest.mock import MagicMock
        with tempfile.TemporaryDirectory() as tmpdir:
            ws = Path(tmpdir)
            self._write_yaml(ws / "agent_metadata.yaml", {
                "agent_name": "test-agent",
                "entrypoint": "python main.py",
                "build_mode": "cloud",
                "version": "0.0.2",
                "image": {
                    "repository_url": "test-agent",
                    "tag": "0.0.2",
                    "build_mode": "local",
                    "build_size": "10MB",
                    "build_time": "1s"
                }
            })
            (ws / "main.py").touch()
            (ws / "requirements.txt").touch()
            (ws / "Dockerfile").touch()

            runtime = PublishRuntime(verbose=True)
            metadata = runtime.metadata_service.load_metadata(ws)

            runtime._prepare_local_image = MagicMock(return_value="test-agent:0.0.2")
            runtime._prepare_cloud_image = MagicMock()

            runtime._prepare_image_for_publishing(ws, metadata, {})

            runtime._prepare_local_image.assert_called_once()
            runtime._prepare_cloud_image.assert_not_called()

    def test_publish_cloud_build_success(self):
        from agentcube.runtime.publish_runtime import PublishRuntime
        from unittest.mock import MagicMock
        with tempfile.TemporaryDirectory() as tmpdir:
            ws = Path(tmpdir)
            self._write_yaml(ws / "agent_metadata.yaml", {
                "agent_name": "test-agent",
                "entrypoint": "python main.py",
                "build_mode": "cloud",
                "version": "0.0.2",
                "image": {
                    "repository_url": "swr.cn-east-3.myhuaweicloud.com/agentcube/test-agent:0.0.2",
                    "tag": "0.0.2",
                    "build_mode": "cloud",
                    "build_size": "45.2MB",
                    "build_time": "12.4s"
                },
                "router_url": "http://router.example.com",
                "workload_manager_url": "http://wlm.example.com",
                "readiness_probe_path": "/healthz",
                "readiness_probe_port": 8080,
                "port": 8080
            })
            (ws / "main.py").touch()
            (ws / "requirements.txt").touch()
            (ws / "Dockerfile").touch()

            runtime = PublishRuntime(verbose=True)
            metadata = runtime.metadata_service.load_metadata(ws)

            runtime._prepare_cloud_image = MagicMock(return_value="swr.cn-east-3.myhuaweicloud.com/agentcube/test-agent:0.0.2")
            runtime._prepare_local_image = MagicMock()

            # Mock agentcube_provider
            runtime.agentcube_provider = MagicMock()
            runtime.agentcube_provider.deploy_agent_runtime.return_value = {
                "deployment_name": "test-agent-deployment",
                "namespace": "default",
            }

            result = runtime.publish(ws, provider="agentcube", agent_endpoint="http://example.com")
            assert result["status"] == "deployed"
            assert result["agent_endpoint"] == "http://example.com"
            runtime._prepare_cloud_image.assert_called_once()
            runtime._prepare_local_image.assert_not_called()
