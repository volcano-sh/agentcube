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

"""Force a Secret-write failure by removing patch RBAC mid-test, verify rollback.

Strategy: use a Role + RoleBinding so the test kubeconfig has only
``configmaps: get,patch,create`` after the first create succeeds, leaving
``secrets: get`` only — the second create's PATCH on the Secret will 403,
triggering rollback.
"""

from __future__ import annotations

import base64
import json

import pytest
from kubernetes import client
from typer.testing import CliRunner

from agentcube.cli.apikey_commands import apikey_app

runner = CliRunner()


@pytest.mark.skipif(True, reason="requires a custom kubeconfig with limited RBAC; "
                                  "wire up via INTEGRATION_LIMITED_KUBECONFIG when needed")
def test_create_rolls_back_configmap_on_secret_failure(
    ephemeral_namespace, core_api,
):
    """Skipped by default; flip the marker once the limited-kubeconfig fixture exists.

    Manual reproduction:
      1. Create Role/RoleBinding granting only ``configmaps: get,patch,create``.
      2. Run ``kubectl agentcube apikey create``; expect exit 1.
      3. Verify ConfigMap has no new entries.
    """
    # Implementation deferred until the limited-RBAC fixture is wired in;
    # rollback semantics are covered by tests/cli/test_apikey_create.py.
    pass
