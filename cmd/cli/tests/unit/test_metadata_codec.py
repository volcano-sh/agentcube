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

"""Tests for the metadata-annotation codec."""

from __future__ import annotations

import json

import pytest

from agentcube.runtime.apikey_runtime import (
    METADATA_ANNOTATION_KEY,
    parse_metadata_annotation,
    upsert_metadata_entry,
)


def test_parse_returns_empty_when_annotation_absent():
    assert parse_metadata_annotation(None) == {}
    assert parse_metadata_annotation("") == {}


def test_parse_returns_dict_for_valid_json():
    blob = json.dumps({"a" * 64: {"created": "2026-01-01T00:00:00Z", "description": "x"}})
    parsed = parse_metadata_annotation(blob)
    assert parsed["a" * 64]["description"] == "x"


def test_parse_tolerates_corrupted_json():
    # Per spec: corrupted annotation is treated as empty so list still works.
    assert parse_metadata_annotation("not json {{{") == {}


def test_upsert_adds_new_entry():
    h = "a" * 64
    updated = upsert_metadata_entry({}, h, created="2026-05-04T00:00:00Z", description="hi")
    assert updated[h] == {"created": "2026-05-04T00:00:00Z", "description": "hi"}


def test_upsert_overwrites_existing_entry():
    h = "a" * 64
    existing = {h: {"created": "old", "description": "old-d"}}
    updated = upsert_metadata_entry(existing, h, created="new", description="new-d")
    assert updated[h] == {"created": "new", "description": "new-d"}
    # Unrelated entries are preserved.
    other = "b" * 64
    existing2 = {other: {"created": "k", "description": "k-d"}, h: {"created": "old", "description": "old-d"}}
    updated2 = upsert_metadata_entry(existing2, h, created="new", description="new-d")
    assert updated2[other] == {"created": "k", "description": "k-d"}


def test_constants_match_spec():
    assert METADATA_ANNOTATION_KEY == "apikey.agentcube.io/metadata"
