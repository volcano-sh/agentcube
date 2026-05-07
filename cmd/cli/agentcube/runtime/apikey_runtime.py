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

"""Pure logic for the apikey CLI subcommands.

Functions in this module are free of Kubernetes I/O and Typer state, so they
can be unit tested in isolation.
"""

from __future__ import annotations

import hashlib
import json
import os
import re
import secrets
from typing import Any, Dict, Iterable, List, Mapping, Optional

CONFIGMAP_DEFAULT_NS_KEY = "defaultNamespace"
ENV_DEFAULT_NS = "E2B_DEFAULT_NAMESPACE"
HARDCODED_DEFAULT_NS = "default"


def resolve_namespace(
    flag_value: Optional[str],
    configmap_data: Mapping[str, str],
) -> str:
    """Return the effective namespace following the spec's priority rules.

    Priority: ``--namespace`` > ConfigMap ``defaultNamespace`` >
    ``$E2B_DEFAULT_NAMESPACE`` > literal ``"default"``.

    Empty strings are treated as unset so the next fallback kicks in.
    """
    if flag_value:
        return flag_value
    cm_default = configmap_data.get(CONFIGMAP_DEFAULT_NS_KEY, "")
    if cm_default:
        return cm_default
    env_default = os.environ.get(ENV_DEFAULT_NS, "")
    if env_default:
        return env_default
    return HARDCODED_DEFAULT_NS

KEY_PREFIX = "e2b_"
KEY_RANDOM_BYTES = 24  # -> 32 url-safe chars after token_urlsafe

DNS1123_LABEL_RE = re.compile(r"^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$")
HEX_RE = re.compile(r"^[0-9a-f]+$")

DESCRIPTION_MAX_LEN = 256
PREFIX_MIN_LEN = 8
PREFIX_MAX_LEN = 64


class ValidationError(ValueError):
    """Raised when CLI input fails format validation (exit code 2)."""


def generate_raw_key() -> str:
    """Generate a fresh raw API key.

    Format: ``e2b_<32 url-safe random chars>``. The ``e2b_`` prefix is a
    human-readable origin marker; the Router only ever sees its SHA-256 hash.
    """
    return KEY_PREFIX + secrets.token_urlsafe(KEY_RANDOM_BYTES)


def hash_key(raw: str) -> str:
    """Return the lowercase 64-char hex SHA-256 digest of ``raw``."""
    return hashlib.sha256(raw.encode("utf-8")).hexdigest()


def validate_namespace(ns: str) -> None:
    """Validate ``ns`` against the DNS-1123 label format Kubernetes requires."""
    if not isinstance(ns, str) or not ns:
        raise ValidationError("namespace must be a non-empty string")
    if len(ns) > 63:
        raise ValidationError(
            f"namespace too long: {len(ns)} chars (max 63)"
        )
    if not DNS1123_LABEL_RE.match(ns):
        raise ValidationError(
            f"namespace {ns!r} is not a valid DNS-1123 label "
            "(lowercase alphanumeric and '-', must start/end with alphanumeric)"
        )


def validate_description(desc: Optional[str]) -> None:
    """Validate ``desc`` is None, empty, or <= 256 chars."""
    if desc is None or desc == "":
        return
    if not isinstance(desc, str):
        raise ValidationError("description must be a string")
    if len(desc) > DESCRIPTION_MAX_LEN:
        raise ValidationError(
            f"description too long: {len(desc)} chars (max {DESCRIPTION_MAX_LEN})"
        )


def validate_prefix(prefix: str) -> None:
    """Validate revoke prefix: 8-64 lowercase hex chars."""
    if not isinstance(prefix, str) or not prefix:
        raise ValidationError("prefix must be a non-empty string")
    if len(prefix) < PREFIX_MIN_LEN or len(prefix) > PREFIX_MAX_LEN:
        raise ValidationError(
            f"prefix length {len(prefix)} out of range "
            f"[{PREFIX_MIN_LEN}, {PREFIX_MAX_LEN}]"
        )
    if not HEX_RE.match(prefix):
        raise ValidationError(
            f"prefix {prefix!r} must be lowercase hex (0-9, a-f) only"
        )


def find_matching_hashes(prefix: str, hashes: Iterable[str]) -> List[str]:
    """Return all hashes that start with ``prefix``, sorted.

    Caller must have validated ``prefix`` with :func:`validate_prefix` first.
    """
    return sorted(h for h in hashes if h.startswith(prefix))


METADATA_ANNOTATION_KEY = "apikey.agentcube.io/metadata"


def parse_metadata_annotation(raw: Optional[str]) -> Dict[str, Dict[str, Any]]:
    """Parse the JSON map stored in the metadata annotation.

    Returns ``{}`` if the annotation is missing, empty, or corrupted.
    """
    if not raw:
        return {}
    try:
        parsed = json.loads(raw)
    except json.JSONDecodeError:
        return {}
    if not isinstance(parsed, dict):
        return {}
    # Defensive: drop entries that aren't dict-shaped.
    return {k: v for k, v in parsed.items() if isinstance(v, dict)}


def upsert_metadata_entry(
    metadata: Dict[str, Dict[str, Any]],
    hash_value: str,
    created: str,
    description: str,
) -> Dict[str, Dict[str, Any]]:
    """Return a new metadata map with ``hash_value`` updated.

    Does not mutate the input map. ``description`` may be empty.
    """
    out = dict(metadata)
    out[hash_value] = {"created": created, "description": description or ""}
    return out
