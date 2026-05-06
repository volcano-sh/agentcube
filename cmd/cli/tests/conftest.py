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

"""Shared pytest fixtures for kubectl-agentcube apikey tests."""

from __future__ import annotations

import json
from typing import Any, Dict
from unittest.mock import MagicMock

import pytest
from kubernetes import client


@pytest.fixture
def fake_secret_body() -> Dict[str, Any]:
    """A V1Secret-like dict with two valid hashes and one revoked."""
    return {
        "metadata": {
            "name": "e2b-api-keys",
            "namespace": "agentcube-system",
            "annotations": {
                "apikey.agentcube.io/metadata": json.dumps({
                    "a" * 64: {"created": "2026-01-01T00:00:00Z", "description": "key-a"},
                    "b" * 64: {"created": "2026-02-01T00:00:00Z", "description": "key-b"},
                }),
            },
        },
        "data": {
            "a" * 64: "dmFsaWQ=",       # base64("valid")
            "b" * 64: "cmV2b2tlZA==",   # base64("revoked")
        },
    }


@pytest.fixture
def fake_configmap_body() -> Dict[str, Any]:
    return {
        "metadata": {
            "name": "e2b-api-key-config",
            "namespace": "agentcube-system",
        },
        "data": {
            "a" * 64: "team-ml",
            "b" * 64: "team-ml",
            "defaultNamespace": "default",
        },
    }


@pytest.fixture
def fake_core_v1() -> MagicMock:
    """A MagicMock standing in for kubernetes.client.CoreV1Api."""
    api = MagicMock(spec=client.CoreV1Api)
    return api
