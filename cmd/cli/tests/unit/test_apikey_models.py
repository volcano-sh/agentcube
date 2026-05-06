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

"""Tests for cmd/cli/agentcube/models/apikey_models.py."""

from __future__ import annotations

from dataclasses import asdict

import pytest

from agentcube.models.apikey_models import ApiKey, ApiKeyCreateResult


def test_apikey_fields_match_wire_format():
    key = ApiKey(
        hash="a" * 64,
        namespace="team-ml",
        status="valid",
        created="2026-05-04T12:00:00Z",
        description="my key",
    )
    assert asdict(key) == {
        "hash": "a" * 64,
        "namespace": "team-ml",
        "status": "valid",
        "created": "2026-05-04T12:00:00Z",
        "description": "my key",
    }


def test_apikey_description_defaults_to_empty_string():
    key = ApiKey(hash="a" * 64, namespace="default", status="valid", created="-")
    assert key.description == ""


def test_apikey_create_result_carries_raw_key():
    result = ApiKeyCreateResult(
        raw_key="e2b_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
        hash="a" * 64,
        namespace="team-ml",
        created="2026-05-04T12:00:00Z",
    )
    assert result.raw_key.startswith("e2b_")
    assert len(result.hash) == 64
