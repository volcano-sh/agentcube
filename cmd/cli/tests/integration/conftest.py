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

"""Shared fixtures for integration tests against a real K8s cluster."""

from __future__ import annotations

import os
import secrets
import time
from typing import Iterator

import pytest
from kubernetes import client, config
from kubernetes.client.rest import ApiException


def _kubeconfig() -> str:
    """Return the kubeconfig path for integration tests, or skip the suite."""
    path = os.environ.get("INTEGRATION_KUBECONFIG")
    if not path:
        pytest.skip("INTEGRATION_KUBECONFIG not set; skipping integration tests")
    return path


@pytest.fixture
def kubeconfig() -> str:
    return _kubeconfig()


@pytest.fixture
def core_api(kubeconfig) -> client.CoreV1Api:
    config.load_kube_config(config_file=kubeconfig)
    return client.CoreV1Api()


@pytest.fixture
def ephemeral_namespace(core_api) -> Iterator[str]:
    """Create a unique namespace for the test, delete it on teardown."""
    name = f"agentcube-system-test-{secrets.token_hex(4)}"
    body = client.V1Namespace(metadata=client.V1ObjectMeta(name=name))
    core_api.create_namespace(body=body)
    try:
        yield name
    finally:
        try:
            core_api.delete_namespace(name=name)
        except ApiException:
            pass
        # Block briefly so subsequent tests don't see the half-terminated ns.
        deadline = time.time() + 30
        while time.time() < deadline:
            try:
                core_api.read_namespace(name=name)
                time.sleep(0.5)
            except ApiException:
                return
