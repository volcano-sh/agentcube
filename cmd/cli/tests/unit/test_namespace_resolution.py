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

"""Tests for the four-level namespace resolution rule."""

from __future__ import annotations

import pytest

from agentcube.runtime.apikey_runtime import resolve_namespace


def test_explicit_namespace_wins_over_everything(monkeypatch):
    monkeypatch.setenv("E2B_DEFAULT_NAMESPACE", "from-env")
    cm_data = {"defaultNamespace": "from-cm"}
    assert resolve_namespace("from-flag", cm_data) == "from-flag"


def test_configmap_default_used_when_no_flag(monkeypatch):
    monkeypatch.setenv("E2B_DEFAULT_NAMESPACE", "from-env")
    cm_data = {"defaultNamespace": "from-cm"}
    assert resolve_namespace(None, cm_data) == "from-cm"


def test_env_used_when_no_flag_and_no_cm_default(monkeypatch):
    monkeypatch.setenv("E2B_DEFAULT_NAMESPACE", "from-env")
    assert resolve_namespace(None, {}) == "from-env"


def test_falls_back_to_default_when_nothing_set(monkeypatch):
    monkeypatch.delenv("E2B_DEFAULT_NAMESPACE", raising=False)
    assert resolve_namespace(None, {}) == "default"


def test_empty_string_in_configmap_treated_as_unset(monkeypatch):
    monkeypatch.setenv("E2B_DEFAULT_NAMESPACE", "from-env")
    cm_data = {"defaultNamespace": ""}
    assert resolve_namespace(None, cm_data) == "from-env"


def test_empty_env_treated_as_unset(monkeypatch):
    monkeypatch.setenv("E2B_DEFAULT_NAMESPACE", "")
    assert resolve_namespace(None, {}) == "default"
