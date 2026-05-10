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

"""
Tests for env support in agent_metadata.yaml.

Verifies that the `env` field is correctly parsed from metadata,
persisted on save, and forwarded to pod containers during publish.
"""

import tempfile
from pathlib import Path
from unittest.mock import MagicMock, patch

import yaml

from agentcube.services.metadata_service import AgentMetadata, MetadataService


# ---------------------------------------------------------------------------
# AgentMetadata model tests
# ---------------------------------------------------------------------------

class TestAgentMetadataEnvField:
    """Test that the env field is accepted and validated on the model."""

    def test_env_field_default_is_none(self):
        meta = AgentMetadata(agent_name="a", entrypoint="python main.py")
        assert meta.env is None

    def test_env_field_accepts_dict(self):
        meta = AgentMetadata(
            agent_name="a",
            entrypoint="python main.py",
            env={"LLM_BASE_URL": "http://llm", "LLM_API_KEY": "secret"},
        )
        assert meta.env == {"LLM_BASE_URL": "http://llm", "LLM_API_KEY": "secret"}

    def test_env_field_empty_dict(self):
        meta = AgentMetadata(
            agent_name="a",
            entrypoint="python main.py",
            env={},
        )
        assert meta.env == {}


# ---------------------------------------------------------------------------
# MetadataService round-trip tests
# ---------------------------------------------------------------------------

class TestMetadataServiceEnvRoundTrip:
    """Test load/save preserves the env field."""

    def _write_yaml(self, path: Path, data: dict):
        with open(path, "w") as f:
            yaml.dump(data, f)

    def test_load_metadata_with_env(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            ws = Path(tmpdir)
            self._write_yaml(ws / "agent_metadata.yaml", {
                "agent_name": "test-agent",
                "entrypoint": "python main.py",
                "env": {"FOO": "bar", "BAZ": "qux"},
            })
            svc = MetadataService()
            meta = svc.load_metadata(ws)
            assert meta.env == {"FOO": "bar", "BAZ": "qux"}

    def test_load_metadata_without_env(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            ws = Path(tmpdir)
            self._write_yaml(ws / "agent_metadata.yaml", {
                "agent_name": "test-agent",
                "entrypoint": "python main.py",
            })
            svc = MetadataService()
            meta = svc.load_metadata(ws)
            assert meta.env is None

    def test_save_and_reload_preserves_env(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            ws = Path(tmpdir)
            meta = AgentMetadata(
                agent_name="test-agent",
                entrypoint="python main.py",
                env={"KEY": "value"},
            )
            svc = MetadataService()
            svc.save_metadata(ws, meta)

            reloaded = svc.load_metadata(ws)
            assert reloaded.env == {"KEY": "value"}

    def test_save_without_env_omits_field(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            ws = Path(tmpdir)
            meta = AgentMetadata(
                agent_name="test-agent",
                entrypoint="python main.py",
            )
            svc = MetadataService()
            svc.save_metadata(ws, meta)

            with open(ws / "agent_metadata.yaml") as f:
                raw = yaml.safe_load(f)
            assert "env" not in raw


# ---------------------------------------------------------------------------
# PublishRuntime env forwarding tests
# ---------------------------------------------------------------------------

class TestPublishRuntimeEnvForwarding:
    """Test that env from metadata is forwarded to providers."""

    def _make_metadata(self, env=None):
        return AgentMetadata(
            agent_name="test-agent",
            entrypoint="python main.py",
            port=8080,
            build_mode="local",
            version="0.0.1",
            registry_url="registry.example.com/test",
            workload_manager_url="http://wm:8080",
            router_url="http://router:8080",
            readiness_probe_path="/health",
            readiness_probe_port=8080,
            agent_endpoint="http://localhost:8080",
            image={"repository_url": "registry.example.com/test:latest"},
            env=env,
        )

    @patch("agentcube.runtime.publish_runtime.AgentCubeProvider")
    @patch("agentcube.runtime.publish_runtime.DockerService")
    @patch("agentcube.runtime.publish_runtime.MetadataService")
    def test_env_forwarded_to_agentcube_provider(
        self, MockMetaSvc, MockDockerSvc, MockACProvider
    ):
        from agentcube.runtime.publish_runtime import PublishRuntime

        env = {"LLM_BASE_URL": "http://llm", "LLM_API_KEY": "sk-123"}
        meta = self._make_metadata(env=env)

        mock_meta_svc = MockMetaSvc.return_value
        mock_meta_svc.load_metadata.return_value = meta

        mock_docker = MockDockerSvc.return_value
        mock_docker.push_image.return_value = {"pushed_image": "registry.example.com/test:latest"}

        mock_provider = MagicMock()
        mock_provider.deploy_agent_runtime.return_value = {
            "deployment_name": "test-agent",
            "namespace": "default",
            "status": "deployed",
            "type": "AgentRuntime",
        }
        MockACProvider.return_value = mock_provider

        runtime = PublishRuntime(verbose=False, provider="agentcube")
        runtime.metadata_service = mock_meta_svc
        runtime.docker_service = mock_docker

        with tempfile.TemporaryDirectory() as tmpdir:
            runtime.publish(Path(tmpdir), provider="agentcube")

        call_kwargs = mock_provider.deploy_agent_runtime.call_args
        passed_env = call_kwargs.kwargs.get("env_vars") or call_kwargs[1].get("env_vars")
        assert passed_env is not None
        assert "LLM_BASE_URL" in passed_env
        assert passed_env["LLM_BASE_URL"] == "http://llm"
        assert passed_env["LLM_API_KEY"] == "sk-123"

    @patch("agentcube.runtime.publish_runtime.KubernetesProvider")
    @patch("agentcube.runtime.publish_runtime.DockerService")
    @patch("agentcube.runtime.publish_runtime.MetadataService")
    def test_env_forwarded_to_k8s_provider(
        self, MockMetaSvc, MockDockerSvc, MockK8sProvider
    ):
        from agentcube.runtime.publish_runtime import PublishRuntime

        env = {"DB_HOST": "db.internal"}
        meta = self._make_metadata(env=env)

        mock_meta_svc = MockMetaSvc.return_value
        mock_meta_svc.load_metadata.return_value = meta

        mock_docker = MockDockerSvc.return_value
        mock_docker.push_image.return_value = {"pushed_image": "registry.example.com/test:latest"}

        mock_provider = MagicMock()
        mock_provider.deploy_agent.return_value = {
            "deployment_name": "test-agent",
            "service_name": "test-agent",
            "namespace": "default",
            "replicas": 1,
            "container_port": 8080,
            "node_port": 30080,
            "service_url": "http://localhost:30080",
        }
        mock_provider.wait_for_deployment_ready.return_value = None
        MockK8sProvider.return_value = mock_provider

        runtime = PublishRuntime(verbose=False, provider="k8s")
        runtime.metadata_service = mock_meta_svc
        runtime.docker_service = mock_docker

        with tempfile.TemporaryDirectory() as tmpdir:
            runtime.publish(Path(tmpdir), provider="k8s")

        call_kwargs = mock_provider.deploy_agent.call_args
        passed_env = call_kwargs.kwargs.get("env_vars") or call_kwargs[1].get("env_vars")
        assert passed_env is not None
        assert "DB_HOST" in passed_env
        assert passed_env["DB_HOST"] == "db.internal"

    @patch("agentcube.runtime.publish_runtime.AgentCubeProvider")
    @patch("agentcube.runtime.publish_runtime.DockerService")
    @patch("agentcube.runtime.publish_runtime.MetadataService")
    def test_no_env_passes_none(self, MockMetaSvc, MockDockerSvc, MockACProvider):
        from agentcube.runtime.publish_runtime import PublishRuntime

        meta = self._make_metadata(env=None)

        mock_meta_svc = MockMetaSvc.return_value
        mock_meta_svc.load_metadata.return_value = meta

        mock_docker = MockDockerSvc.return_value
        mock_docker.push_image.return_value = {"pushed_image": "registry.example.com/test:latest"}

        mock_provider = MagicMock()
        mock_provider.deploy_agent_runtime.return_value = {
            "deployment_name": "test-agent",
            "namespace": "default",
            "status": "deployed",
            "type": "AgentRuntime",
        }
        MockACProvider.return_value = mock_provider

        runtime = PublishRuntime(verbose=False, provider="agentcube")
        runtime.metadata_service = mock_meta_svc
        runtime.docker_service = mock_docker

        with tempfile.TemporaryDirectory() as tmpdir:
            runtime.publish(Path(tmpdir), provider="agentcube")

        call_kwargs = mock_provider.deploy_agent_runtime.call_args
        passed_env = call_kwargs.kwargs.get("env_vars") or call_kwargs[1].get("env_vars")
        assert passed_env is None
