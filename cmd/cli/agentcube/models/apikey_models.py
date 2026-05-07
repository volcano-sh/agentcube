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

"""Dataclasses for the apikey CLI subcommands.

Field names match the JSON wire format used by `-o json`. Keep them in
sync with docs/superpowers/specs/2026-05-04-kubectl-agentcube-apikey-design.md.
"""

from __future__ import annotations

from dataclasses import dataclass, field


@dataclass
class ApiKey:
    """A row in `kubectl agentcube apikey list`.

    Attributes:
        hash:        Full SHA-256 hex digest (64 chars, lowercase).
        namespace:   Logical namespace bound to the key, or "-" if missing.
        status:      "valid" / "revoked" / "expired" / "orphaned (...)".
        created:     RFC3339 timestamp string, or "-" if unknown.
        description: Free-text note, possibly empty.
    """

    hash: str
    namespace: str
    status: str
    created: str
    description: str = ""


@dataclass
class ApiKeyCreateResult:
    """Output of `kubectl agentcube apikey create`."""

    raw_key: str
    hash: str
    namespace: str
    created: str
