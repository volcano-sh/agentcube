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

"""Typer subcommands for E2B API key lifecycle (create / list / revoke).

Wire format and on-cluster data model are defined by
docs/superpowers/specs/2026-05-04-kubectl-agentcube-apikey-design.md
and the Router authenticator at pkg/router/e2b/auth.go.
"""

from __future__ import annotations

import json
import sys
from dataclasses import asdict
from datetime import datetime, timezone
from typing import Any, Dict, Optional

import typer
from kubernetes.client.rest import ApiException
from rich.console import Console
from rich.table import Table

from agentcube.models.apikey_models import ApiKey, ApiKeyCreateResult
from agentcube.runtime.apikey_runtime import (
    METADATA_ANNOTATION_KEY,
    ValidationError,
    find_matching_hashes,
    generate_raw_key,
    hash_key,
    parse_metadata_annotation,
    resolve_namespace,
    upsert_metadata_entry,
    validate_description,
    validate_namespace,
    validate_prefix,
)
from agentcube.services.k8s_provider import (
    KubernetesProvider,
    NamespaceNotFoundError,
)

# Errors and verbose progress lines must never collide with stdout (where
# the raw API key, table, or JSON payload appears).
_stderr = Console(stderr=True, highlight=False)
_stdout = Console(stderr=False, highlight=False)

apikey_app = typer.Typer(
    name="apikey",
    help="Manage E2B API keys (create, list, revoke).",
    no_args_is_help=True,
    rich_markup_mode=None,  # avoid colour escape codes leaking into JSON
)

# ---------------- shared helpers ----------------

DEFAULT_SECRET_NAMESPACE = "agentcube-system"
DEFAULT_SECRET_NAME = "e2b-api-keys"
DEFAULT_CONFIGMAP_NAME = "e2b-api-key-config"


def _now_rfc3339() -> str:
    return datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def _emit_error_text(msg: str, hint: Optional[str] = None) -> None:
    _stderr.print(f"Error: {msg}")
    if hint:
        _stderr.print(f"       Hint: {hint}")


def _emit_error_json(error: str, message: str, **extra: Any) -> None:
    payload: Dict[str, Any] = {"error": error, "message": message}
    payload.update(extra)
    _stdout.print(json.dumps(payload, sort_keys=True))
    _stderr.print(f"Error: {message}")


def _exit(code: int) -> None:
    raise typer.Exit(code)


def _build_provider(
    secret_namespace: str,
    kubeconfig: Optional[str],
    verbose: bool,
) -> KubernetesProvider:
    """Construct a KubernetesProvider for the apikey commands.

    Always disables auto-namespace creation so a missing
    ``agentcube-system`` is surfaced rather than silently created.
    """
    return KubernetesProvider(
        namespace=secret_namespace,
        verbose=verbose,
        kubeconfig=kubeconfig,
        auto_create_namespace=False,
    )


def _bootstrap(
    provider: KubernetesProvider,
    secret_namespace: str,
    secret_name: str,
    configmap_name: str,
    verbose: bool,
):
    """Validate the namespace and ensure Secret + ConfigMap exist."""
    if verbose:
        _stderr.print(f"-> verifying namespace {secret_namespace!r} exists")
    provider.verify_namespace_exists(secret_namespace)
    if verbose:
        _stderr.print(f"-> get-or-create Secret {secret_name!r}")
    secret = provider.get_or_create_secret(secret_namespace, secret_name)
    if verbose:
        _stderr.print(f"-> get-or-create ConfigMap {configmap_name!r}")
    configmap = provider.get_or_create_configmap(secret_namespace, configmap_name)
    return secret, configmap


# ---------------- create ----------------

@apikey_app.command("create")
def create_command(
    namespace: Optional[str] = typer.Option(
        None, "--namespace",
        help="Logical namespace to bind the key to. Falls back to "
             "ConfigMap defaultNamespace, then $E2B_DEFAULT_NAMESPACE, then 'default'.",
    ),
    description: Optional[str] = typer.Option(
        None, "--description",
        help="Optional human-readable note (max 256 chars).",
    ),
    output: str = typer.Option(
        "text", "-o", "--output",
        help="Output format: 'text' (default) or 'json'.",
    ),
    kubeconfig: Optional[str] = typer.Option(
        None, "--kubeconfig", help="Path to kubeconfig (defaults to $KUBECONFIG)."
    ),
    secret_namespace: str = typer.Option(
        DEFAULT_SECRET_NAMESPACE, "--secret-namespace",
        help="Namespace holding the Secret/ConfigMap.",
    ),
    secret_name: str = typer.Option(
        DEFAULT_SECRET_NAME, "--secret-name",
    ),
    configmap_name: str = typer.Option(
        DEFAULT_CONFIGMAP_NAME, "--configmap-name",
    ),
    verbose: bool = typer.Option(False, "-v", "--verbose"),
) -> None:
    """Provision a new E2B API key."""
    if output not in ("text", "json"):
        _emit_error_text(f"unsupported output format: {output!r}")
        _exit(2)

    # --- validation (exit 2) ---
    try:
        if namespace is not None:
            validate_namespace(namespace)
        validate_description(description)
    except ValidationError as e:
        if output == "json":
            _emit_error_json("usage_error", str(e))
        else:
            _emit_error_text(str(e))
        _exit(2)

    # --- bootstrap (exit 1 on K8s failure) ---
    try:
        provider = _build_provider(secret_namespace, kubeconfig, verbose)
        _, configmap = _bootstrap(
            provider, secret_namespace, secret_name, configmap_name, verbose,
        )
    except NamespaceNotFoundError as e:
        msg = (f"namespace {secret_namespace!r} not found. "
               "Is AgentCube installed in this cluster?")
        if output == "json":
            _emit_error_json("namespace_missing", msg)
        else:
            _emit_error_text(
                msg,
                hint="Override with --secret-namespace if you customized the install.",
            )
        _exit(1)
    except ApiException as e:
        _handle_apiexception(e, "create", output)
        _exit(1)
    except Exception as e:
        _emit_error_text(f"internal error: {e}")
        _exit(1)

    # --- generate + resolve namespace ---
    raw_key = generate_raw_key()
    h = hash_key(raw_key)
    cm_data = configmap.data or {}
    effective_ns = resolve_namespace(namespace, cm_data)
    created = _now_rfc3339()

    # --- write order: ConfigMap first, then Secret (see spec data flow) ---
    try:
        provider.patch_configmap_data(
            namespace=secret_namespace,
            name=configmap_name,
            data={h: effective_ns},
        )
    except ApiException as e:
        _handle_apiexception(e, "create", output)
        _exit(1)

    try:
        secret_now = provider.get_or_create_secret(secret_namespace, secret_name)
        annotations = (
            (secret_now.metadata.annotations or {})
            if secret_now.metadata
            else {}
        )
        existing_meta = parse_metadata_annotation(
            annotations.get(METADATA_ANNOTATION_KEY)
        )
        new_meta = upsert_metadata_entry(
            existing_meta, h, created=created, description=description or "",
        )
        provider.patch_secret_data(
            namespace=secret_namespace,
            name=secret_name,
            data={h: "valid"},
            annotations={METADATA_ANNOTATION_KEY: json.dumps(new_meta, sort_keys=True)},
        )
    except ApiException as e:
        # Rollback: remove the ConfigMap entry we just wrote.
        try:
            provider.remove_configmap_data_key(
                namespace=secret_namespace, name=configmap_name, key=h,
            )
            rollback_msg = (
                "Failed to write Secret entry; rolled back ConfigMap. No key issued."
            )
        except Exception:
            rollback_msg = (
                "Failed to write Secret AND failed to roll back ConfigMap. "
                f"Orphan ConfigMap entry remains for hash {h[:12]}.... "
                "Run `kubectl agentcube apikey list` to see it."
            )
        if output == "json":
            _emit_error_json("internal_error", rollback_msg, hash=h)
        else:
            _emit_error_text(rollback_msg)
        _exit(1)

    # --- success output ---
    result = ApiKeyCreateResult(
        raw_key=raw_key, hash=h, namespace=effective_ns, created=created,
    )
    if output == "json":
        payload = {
            "api_key": result.raw_key,
            "hash": result.hash,
            "namespace": result.namespace,
            "status": "valid",
            "created": result.created,
        }
        _stdout.print(json.dumps(payload, sort_keys=True))
        return

    # text output
    _stdout.print(f"API Key:     {result.raw_key}")
    _stdout.print(f"Hash:        {result.hash}")
    _stdout.print(f"Namespace:   {result.namespace}")
    _stdout.print("Status:      valid")
    _stdout.print("")
    _stdout.print("WARNING: this is the only time the raw key is shown.")
    _stdout.print("         Store it securely - it cannot be retrieved later.")


# ---------------- shared error mapping ----------------

def _handle_apiexception(e: ApiException, subcommand: str, output: str) -> None:
    """Map a Kubernetes ApiException to a user-facing error message."""
    if e.status == 403:
        verbs = _RBAC_HINTS[subcommand]
        msg = "forbidden - kubeconfig lacks required RBAC."
        if output == "json":
            _emit_error_json("forbidden", msg, required_rbac=verbs)
        else:
            _emit_error_text(msg)
            _stderr.print("")
            _stderr.print(f"Required RBAC for `apikey {subcommand}`:")
            for line in _format_rbac_block(verbs).splitlines():
                _stderr.print(f"  {line}")
        return
    if e.status == 409:
        msg = "conflict updating Secret/ConfigMap. Please re-run."
        if output == "json":
            _emit_error_json("conflict", msg)
        else:
            _emit_error_text(msg)
        return
    msg = f"Kubernetes API error: {e.status} {e.reason}"
    if output == "json":
        _emit_error_json("internal_error", msg)
    else:
        _emit_error_text(msg)


_RBAC_HINTS = {
    "create": {
        "secrets": ["get", "create", "patch"],
        "configmaps": ["get", "create", "patch"],
        "namespaces": ["get"],
    },
    "list": {
        "secrets": ["get"],
        "configmaps": ["get"],
        "namespaces": ["get"],
    },
    "revoke": {
        "secrets": ["get", "patch"],
        "configmaps": ["get"],
        "namespaces": ["get"],
    },
}


def _format_rbac_block(verbs: Dict[str, list]) -> str:
    lines = []
    for resource, vs in verbs.items():
        lines.append(f'apiGroups: [""]')
        lines.append(f'resources:  [{resource}]')
        lines.append(f'verbs:      [{", ".join(vs)}]')
        lines.append("")
    return "\n".join(lines)


# ---------------- list ----------------

@apikey_app.command("list")
def list_command(
    namespace: Optional[str] = typer.Option(
        None, "--namespace", help="Filter by logical namespace.",
    ),
    status: str = typer.Option(
        "valid", "--status",
        help="Filter by status: valid | revoked | expired | all (default: valid).",
    ),
    output: str = typer.Option(
        "table", "-o", "--output", help="Output format: 'table' (default) or 'json'.",
    ),
    kubeconfig: Optional[str] = typer.Option(None, "--kubeconfig"),
    secret_namespace: str = typer.Option(DEFAULT_SECRET_NAMESPACE, "--secret-namespace"),
    secret_name: str = typer.Option(DEFAULT_SECRET_NAME, "--secret-name"),
    configmap_name: str = typer.Option(DEFAULT_CONFIGMAP_NAME, "--configmap-name"),
    verbose: bool = typer.Option(False, "-v", "--verbose"),
) -> None:
    """List E2B API keys."""
    if status not in ("valid", "revoked", "expired", "all"):
        _emit_error_text(
            f"unsupported status filter: {status!r}. "
            "Use one of: valid, revoked, expired, all."
        )
        _exit(2)
    if output not in ("table", "json"):
        _emit_error_text(f"unsupported output format: {output!r}")
        _exit(2)

    try:
        provider = _build_provider(secret_namespace, kubeconfig, verbose)
        secret, configmap = _bootstrap(
            provider, secret_namespace, secret_name, configmap_name, verbose,
        )
        secret_data = provider.read_secret_decoded_data(secret_namespace, secret_name)
    except NamespaceNotFoundError:
        msg = (f"namespace {secret_namespace!r} not found. "
               "Is AgentCube installed in this cluster?")
        if output == "json":
            _emit_error_json("namespace_missing", msg)
        else:
            _emit_error_text(msg)
        _exit(1)
    except ApiException as e:
        _handle_apiexception(e, "list", output)
        _exit(1)

    cm_data = configmap.data or {}
    annotations = (
        (secret.metadata.annotations or {}) if secret.metadata else {}
    )
    metadata = parse_metadata_annotation(annotations.get(METADATA_ANNOTATION_KEY))

    rows = _join_rows(secret_data, cm_data, metadata)
    rows = _filter_rows(rows, namespace_filter=namespace, status_filter=status)

    if output == "json":
        _stdout.print(json.dumps([asdict(r) for r in rows], sort_keys=True))
        return

    table = Table(
        "HASH", "NAMESPACE", "STATUS", "CREATED", "DESCRIPTION",
        title=None, header_style="bold",
    )
    for r in rows:
        table.add_row(
            r.hash[:12] + "...",
            r.namespace, r.status, r.created, r.description or "-",
        )
    _stdout.print(table)


def _join_rows(
    secret_data: Dict[str, str],
    configmap_data: Dict[str, str],
    metadata: Dict[str, Dict[str, Any]],
) -> "list[ApiKey]":
    """Join Secret + ConfigMap views, surfacing orphans explicitly."""
    rows: list[ApiKey] = []
    cm_keys = {k for k in configmap_data.keys() if k != "defaultNamespace"}
    all_hashes = set(secret_data.keys()) | cm_keys
    for h in sorted(all_hashes):
        in_secret = h in secret_data
        in_cm = h in cm_keys
        if in_secret and not in_cm:
            status = f"{secret_data[h]} (orphaned: no namespace mapping)"
        elif in_cm and not in_secret:
            status = "orphaned (no secret entry)"
        else:
            status = secret_data[h]
        meta = metadata.get(h, {})
        rows.append(ApiKey(
            hash=h,
            namespace=configmap_data.get(h, "-"),
            status=status,
            created=meta.get("created", "-"),
            description=meta.get("description", ""),
        ))
    return rows


def _filter_rows(
    rows: "list[ApiKey]",
    namespace_filter: Optional[str],
    status_filter: str,
) -> "list[ApiKey]":
    out = []
    for r in rows:
        if namespace_filter is not None and r.namespace != namespace_filter:
            continue
        if status_filter != "all":
            base = r.status.split(" ", 1)[0]
            if base != status_filter:
                continue
        out.append(r)
    return out


# ---------------- revoke ----------------

@apikey_app.command("revoke")
def revoke_command(
    prefix: str = typer.Argument(..., help="Hash prefix (8-64 lowercase hex chars)."),
    force: bool = typer.Option(False, "-f", "--force", help="Skip confirmation."),
    output: str = typer.Option(
        "text", "-o", "--output", help="Output format: 'text' (default) or 'json'.",
    ),
    kubeconfig: Optional[str] = typer.Option(None, "--kubeconfig"),
    secret_namespace: str = typer.Option(DEFAULT_SECRET_NAMESPACE, "--secret-namespace"),
    secret_name: str = typer.Option(DEFAULT_SECRET_NAME, "--secret-name"),
    configmap_name: str = typer.Option(DEFAULT_CONFIGMAP_NAME, "--configmap-name"),
    verbose: bool = typer.Option(False, "-v", "--verbose"),
) -> None:
    """Revoke an E2B API key by hash prefix."""
    if output not in ("text", "json"):
        _emit_error_text(f"unsupported output format: {output!r}")
        _exit(2)

    try:
        validate_prefix(prefix)
    except ValidationError as e:
        if output == "json":
            _emit_error_json("usage_error", str(e))
        else:
            _emit_error_text(str(e))
        _exit(2)

    try:
        provider = _build_provider(secret_namespace, kubeconfig, verbose)
        secret, _ = _bootstrap(
            provider, secret_namespace, secret_name, configmap_name, verbose,
        )
        secret_data = provider.read_secret_decoded_data(secret_namespace, secret_name)
    except NamespaceNotFoundError:
        msg = (f"namespace {secret_namespace!r} not found. "
               "Is AgentCube installed in this cluster?")
        if output == "json":
            _emit_error_json("namespace_missing", msg)
        else:
            _emit_error_text(msg)
        _exit(1)
    except ApiException as e:
        _handle_apiexception(e, "revoke", output)
        _exit(1)

    matches = find_matching_hashes(prefix, secret_data.keys())

    if not matches:
        msg = f"no key matches prefix {prefix!r}"
        if output == "json":
            _emit_error_json("not_found", msg)
        else:
            _emit_error_text(msg)
        _exit(1)

    if len(matches) > 1:
        msg = f"prefix {prefix!r} matches {len(matches)} keys"
        if output == "json":
            _emit_error_json("ambiguous_prefix", msg, candidates=matches)
        else:
            _emit_error_text(msg)
            _stderr.print("       Candidates:")
            for h in matches:
                _stderr.print(f"         {h}")
            _stderr.print("       Hint: provide more characters to disambiguate.")
        _exit(1)

    target = matches[0]
    current_status = secret_data[target]

    if current_status == "revoked":
        if output == "json":
            payload = {"hash": target, "status": "revoked", "changed": False}
            _stdout.print(json.dumps(payload, sort_keys=True))
        else:
            _stdout.print(
                f"Key {target[:12]}... was already revoked (no change applied)."
            )
        return  # exit 0

    if not force:
        if not typer.confirm(f"Revoke key {target[:12]}...?", default=False):
            raise typer.Abort()

    try:
        provider.patch_secret_data(
            namespace=secret_namespace,
            name=secret_name,
            data={target: "revoked"},
            annotations={},
        )
    except ApiException as e:
        _handle_apiexception(e, "revoke", output)
        _exit(1)

    if output == "json":
        payload = {"hash": target, "status": "revoked", "changed": True}
        _stdout.print(json.dumps(payload, sort_keys=True))
    else:
        _stdout.print(f"Key {target[:12]}... revoked.")
