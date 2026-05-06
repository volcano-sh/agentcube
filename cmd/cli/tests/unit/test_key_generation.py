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

"""Tests for raw API key generation."""

from __future__ import annotations

import re

from agentcube.runtime.apikey_runtime import generate_raw_key

# 32 url-safe characters is what `secrets.token_urlsafe(24)` returns
# (24 random bytes -> ceil(24*4/3) = 32 base64url chars, no padding).
RAW_KEY_RE = re.compile(r"^e2b_[A-Za-z0-9_-]{32}$")


def test_generate_raw_key_format():
    key = generate_raw_key()
    assert RAW_KEY_RE.match(key) is not None, key


def test_generate_raw_key_uniqueness():
    keys = {generate_raw_key() for _ in range(50)}
    assert len(keys) == 50  # collision-free in practice


def test_generate_raw_key_length():
    assert len(generate_raw_key()) == len("e2b_") + 32
