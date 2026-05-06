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

"""Tests for input validation helpers."""

from __future__ import annotations

import pytest

from agentcube.runtime.apikey_runtime import (
    ValidationError,
    validate_description,
    validate_namespace,
    validate_prefix,
)


# --- validate_namespace ---

@pytest.mark.parametrize("ns", ["default", "team-ml", "a", "a1", "team-1-ml", "a" * 63])
def test_validate_namespace_accepts_dns1123_labels(ns):
    validate_namespace(ns)  # must not raise


@pytest.mark.parametrize(
    "ns",
    [
        "",
        "Team-ML",        # uppercase
        "-team",          # leading hyphen
        "team-",          # trailing hyphen
        "team_ml",        # underscore
        "a" * 64,         # too long
        "team.ml",        # dot
        "团队",           # non-ASCII
    ],
)
def test_validate_namespace_rejects_invalid(ns):
    with pytest.raises(ValidationError):
        validate_namespace(ns)


# --- validate_description ---

def test_validate_description_accepts_short_text():
    validate_description("hello world")
    validate_description("")  # empty is OK
    validate_description(None)  # None is OK


def test_validate_description_rejects_oversized():
    with pytest.raises(ValidationError):
        validate_description("x" * 257)


def test_validate_description_accepts_max_length():
    validate_description("x" * 256)  # exactly 256 chars


# --- validate_prefix ---

@pytest.mark.parametrize(
    "prefix",
    ["abcdef12", "0" * 8, "f" * 64, "abcd1234deadbeef"],
)
def test_validate_prefix_accepts_lowercase_hex(prefix):
    validate_prefix(prefix)


@pytest.mark.parametrize(
    "prefix",
    [
        "",
        "abc",            # too short
        "ABCDEF12",       # uppercase
        "abcdefg1",       # 'g' not hex
        "x" * 65,         # too long
        "abc 1234",       # whitespace
    ],
)
def test_validate_prefix_rejects_invalid(prefix):
    with pytest.raises(ValidationError):
        validate_prefix(prefix)
