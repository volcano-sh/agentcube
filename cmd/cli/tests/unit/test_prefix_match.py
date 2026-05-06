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

"""Tests for revoke prefix-match resolution."""

from __future__ import annotations

import pytest

from agentcube.runtime.apikey_runtime import find_matching_hashes


HASH_A = "abcd1234" + "0" * 56
HASH_B = "abcd1234" + "f" * 56
HASH_C = "deadbeef" + "0" * 56
HASHES = {HASH_A, HASH_B, HASH_C}


def test_unique_match():
    assert find_matching_hashes("deadbeef", HASHES) == [HASH_C]


def test_full_hash_match():
    assert find_matching_hashes(HASH_A, HASHES) == [HASH_A]


def test_no_match_returns_empty():
    assert find_matching_hashes("ffffffff", HASHES) == []


def test_ambiguous_returns_all_candidates_sorted():
    matches = find_matching_hashes("abcd1234", HASHES)
    assert matches == sorted([HASH_A, HASH_B])


def test_uses_lowercase_input_only():
    # Validation lives elsewhere; the matcher itself just does prefix compare.
    # Caller is required to have validated the prefix already.
    assert find_matching_hashes("ABCD1234", HASHES) == []
