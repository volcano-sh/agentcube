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

"""Tests for SHA-256 hashing of raw API keys."""

from __future__ import annotations

import hashlib

from agentcube.runtime.apikey_runtime import hash_key


def test_hash_key_known_vector():
    raw = "e2b_test"
    expected = hashlib.sha256(raw.encode("utf-8")).hexdigest()
    assert hash_key(raw) == expected


def test_hash_key_is_lowercase_hex_64_chars():
    h = hash_key("anything")
    assert len(h) == 64
    assert h == h.lower()
    assert all(c in "0123456789abcdef" for c in h)


def test_hash_key_is_stable():
    assert hash_key("repeat") == hash_key("repeat")
    assert hash_key("a") != hash_key("b")


def test_hash_key_handles_unicode():
    h = hash_key("e2b_键")
    assert len(h) == 64
