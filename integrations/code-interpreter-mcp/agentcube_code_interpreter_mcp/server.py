# Copyright The Volcano Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.

"""FastMCP server backed by AgentCube CodeInterpreterClient."""

from __future__ import annotations

import os
import threading
from collections.abc import Callable
from dataclasses import dataclass
from typing import Annotated, Any, Optional

import requests
from mcp.server.fastmcp import FastMCP
from pydantic import Field

# In-process cache of live CodeInterpreterClient instances for session_reuse.
# Not shared across processes or replicas — keep Deployment replicas at 1 or use sticky routing.
_SESSION_LOCK = threading.RLock()
_SESSIONS: dict[str, Any] = {}


def _env_nonempty(key: str, default: str) -> str:
    """Read env var; treat empty/whitespace-only as missing and return default."""
    return (os.environ.get(key, default) or default).strip() or default


@dataclass(frozen=True)
class Server:
    router_url: str
    workload_manager_url: str
    namespace: str
    code_interpreter_name: str
    auth_token: Optional[str]
    ttl: int


def _load_server() -> Server:
    router = os.environ.get("ROUTER_URL", "").strip()
    wm = os.environ.get("WORKLOAD_MANAGER_URL", "").strip()
    if not router:
        raise RuntimeError("ROUTER_URL is required")
    if not wm:
        raise RuntimeError("WORKLOAD_MANAGER_URL is required")
    token = os.environ.get("API_TOKEN") or os.environ.get("AGENTCUBE_API_TOKEN") or None
    if token is not None:
        token = token.strip() or None
    return Server(
        router_url=router,
        workload_manager_url=wm,
        namespace=_env_nonempty("AGENTCUBE_NAMESPACE", "default"),
        code_interpreter_name=_env_nonempty("CODE_INTERPRETER_NAME", "my-interpreter"),
        auth_token=token,
        ttl=int(os.environ.get("CODE_INTERPRETER_SESSION_TTL", "3600")),
    )


def _import_client():
    try:
        from agentcube import CodeInterpreterClient
    except ImportError as e:  # pragma: no cover - runtime guard
        raise RuntimeError(
            "agentcube SDK is not installed. Install with: pip install -e /path/to/sdk-python"
        ) from e
    return CodeInterpreterClient


def _client_for_call(
    session_id: Optional[str],
    session_reuse: bool,
    cfg: Server,
    CodeInterpreterClient: type,
):
    with _SESSION_LOCK:
        if session_id:
            cached = _SESSIONS.get(session_id)
            if cached is not None:
                return cached
            client = CodeInterpreterClient(
                name=cfg.code_interpreter_name,
                namespace=cfg.namespace,
                ttl=cfg.ttl,
                workload_manager_url=cfg.workload_manager_url,
                router_url=cfg.router_url,
                auth_token=cfg.auth_token,
                session_id=session_id,
            )
            if session_reuse:
                _SESSIONS[session_id] = client
            return client
        client = CodeInterpreterClient(
            name=cfg.code_interpreter_name,
            namespace=cfg.namespace,
            ttl=cfg.ttl,
            workload_manager_url=cfg.workload_manager_url,
            router_url=cfg.router_url,
            auth_token=cfg.auth_token,
        )
        if session_reuse and client.session_id:
            _SESSIONS[client.session_id] = client
        return client


def _cleanup_after_call(client: Any, session_reuse: bool) -> None:
    if session_reuse:
        return
    sid = getattr(client, "session_id", None)
    try:
        client.stop()
    finally:
        if sid:
            with _SESSION_LOCK:
                _SESSIONS.pop(sid, None)


def _is_stale_session_error(exc: BaseException) -> bool:
    """Router returns 404 for unknown/expired session (see pkg/router session not found)."""
    if isinstance(exc, requests.HTTPError):
        r = exc.response
        if r is not None and r.status_code in (404, 410):
            return True
    return False


def _drop_session_cache(session_id: str) -> None:
    with _SESSION_LOCK:
        _SESSIONS.pop(session_id, None)


def _call_with_session_recovery(
    session_id: Optional[str],
    session_reuse: bool,
    cfg: Server,
    CodeInterpreterClient: type,
    op: Callable[[Any], Any],
) -> tuple[Any, dict[str, Any], str]:
    """Run ``op(client)``; if the caller passed ``session_id`` and Router says it is gone, open a new session once."""
    meta: dict[str, Any] = {}
    client = _client_for_call(session_id, session_reuse, cfg, CodeInterpreterClient)
    try:
        try:
            out = op(client)
            return out, meta, client.session_id or ""
        except Exception as e:
            if not session_id or not _is_stale_session_error(e):
                raise
            _drop_session_cache(session_id)
            try:
                client.stop()
            except Exception:
                pass
            client = _client_for_call(None, session_reuse, cfg, CodeInterpreterClient)
            meta["session_recovered"] = True
            meta["previous_session_id"] = session_id
            meta["hint"] = (
                "Old session expired or missing; new sandbox below. "
            )
            out = op(client)
            return out, meta, client.session_id or ""
    finally:
        _cleanup_after_call(client, session_reuse)


def create_mcp_server(
    *,
    host: str = "127.0.0.1",
    port: int = 8000,
    instructions: str | None = None,
) -> FastMCP:
    instr = instructions or (
        "AgentCube sandbox. Multi-step: session_reuse=true, pass session_id from prior JSON; "
        "each run_code is a new process—only files persist. stop_session when done. "
        "If a session_id is expired, the server recreates the sandbox once (workspace files are lost)."
    )
    app = FastMCP(
        "agentcube-code-interpreter",
        instructions=instr,
        host=host,
        port=port,
        stateless_http=True,
        json_response=True,
    )
    CodeInterpreterClient = _import_client()

    @app.tool(structured_output=False)
    def run_code(
        language: Annotated[
            str,
            Field(
                description="Language (python, bash)."
            ),
        ],
        code: Annotated[str, Field(description="Code or script to run.")],
        session_id: Annotated[
            Optional[str],
            Field(
                default=None,
                description="Prior session_id to continue; omit for new session.",
            ),
        ] = None,
        session_reuse: Annotated[
            bool,
            Field(
                default=False,
                description="Keep sandbox; pass session_id next; stop_session when done. Default tears down after.",
            ),
        ] = False,
        timeout_seconds: Annotated[
            Optional[float],
            Field(
                default=None,
                description="Run timeout (seconds).",
            ),
        ] = None,
    ) -> dict[str, Any]:
        """Run code in the sandbox."""
        cfg = _load_server()

        def _op(c: Any) -> Any:
            return c.run_code(language, code, timeout=timeout_seconds)

        out, meta, sid = _call_with_session_recovery(
            session_id, session_reuse, cfg, CodeInterpreterClient, _op
        )
        payload: dict[str, Any] = {"session_id": sid, "output": out}
        payload.update(meta)
        return payload

    @app.tool(structured_output=False)
    def execute_command(
        command: Annotated[str, Field(description="Shell command in sandbox.")],
        session_id: Annotated[
            Optional[str],
            Field(
                default=None,
                description="Prior session_id; omit for new session.",
            ),
        ] = None,
        session_reuse: Annotated[
            bool,
            Field(
                default=False,
                description="Keep sandbox; pass session_id next; stop_session when done.",
            ),
        ] = False,
        timeout_seconds: Annotated[
            Optional[float],
            Field(default=None, description="Timeout (seconds)."),
        ] = None,
    ) -> dict[str, Any]:
        """Run shell in sandbox."""
        cfg = _load_server()

        def _op(c: Any) -> Any:
            return c.execute_command(command, timeout=timeout_seconds)

        out, meta, sid = _call_with_session_recovery(
            session_id, session_reuse, cfg, CodeInterpreterClient, _op
        )
        payload: dict[str, Any] = {"session_id": sid, "output": out}
        payload.update(meta)
        return payload

    @app.tool(structured_output=False)
    def write_file(
        content: Annotated[str, Field(description="File text.")],
        remote_path: Annotated[
            str,
            Field(
                description="Path under workspace (e.g. data/input.txt)."
            ),
        ],
        session_id: Annotated[
            Optional[str],
            Field(default=None, description="Prior session_id; omit for new session."),
        ] = None,
        session_reuse: Annotated[
            bool,
            Field(
                default=False,
                description="Keep session for follow-up; pass session_id; stop_session when done.",
            ),
        ] = False,
    ) -> dict[str, Any]:
        """Write a file in the workspace."""
        cfg = _load_server()

        def _op(c: Any) -> None:
            c.write_file(content, remote_path)

        _, meta, sid = _call_with_session_recovery(
            session_id, session_reuse, cfg, CodeInterpreterClient, _op
        )
        payload: dict[str, Any] = {"session_id": sid, "remote_path": remote_path, "status": "ok"}
        payload.update(meta)
        return payload

    @app.tool(structured_output=False)
    def list_files(
        path: Annotated[
            str,
            Field(
                default=".",
                description="Dir to list (default .).",
            ),
        ] = ".",
        session_id: Annotated[
            Optional[str],
            Field(default=None, description="Prior session_id; omit for new session."),
        ] = None,
        session_reuse: Annotated[
            bool,
            Field(
                default=False,
                description="Keep session for more tools; else tear down after.",
            ),
        ] = False,
    ) -> dict[str, Any]:
        """List workspace directory."""
        cfg = _load_server()

        def _op(c: Any) -> Any:
            return c.list_files(path)

        files, meta, sid = _call_with_session_recovery(
            session_id, session_reuse, cfg, CodeInterpreterClient, _op
        )
        payload: dict[str, Any] = {"session_id": sid, "path": path, "entries": files}
        payload.update(meta)
        return payload

    @app.tool(structured_output=False)
    def upload_file(
        local_path: Annotated[
            str,
            Field(
                description="Path to a file on the host running this MCP server (not the sandbox).",
            ),
        ],
        remote_path: Annotated[
            str,
            Field(
                description="Destination in the workspace (relative to session working directory).",
            ),
        ],
        session_id: Annotated[
            Optional[str],
            Field(default=None, description="Prior session_id; omit for new session."),
        ] = None,
        session_reuse: Annotated[
            bool,
            Field(
                default=False,
                description="Keep session for follow-up; pass session_id; stop_session when done.",
            ),
        ] = False,
    ) -> dict[str, Any]:
        """Upload a local file to the workspace (multipart, same as CodeInterpreterClient.upload_file)."""
        cfg = _load_server()

        def _op(c: Any) -> None:
            c.upload_file(local_path, remote_path)

        _, meta, sid = _call_with_session_recovery(
            session_id, session_reuse, cfg, CodeInterpreterClient, _op
        )
        payload: dict[str, Any] = {
            "session_id": sid,
            "local_path": local_path,
            "remote_path": remote_path,
            "status": "ok",
        }
        payload.update(meta)
        return payload

    @app.tool(structured_output=False)
    def download_file(
        remote_path: Annotated[
            str,
            Field(
                description="File in the workspace (relative to session working directory).",
            ),
        ],
        local_path: Annotated[
            str,
            Field(
                description="Destination path on the host running this MCP server (not the sandbox).",
            ),
        ],
        session_id: Annotated[
            Optional[str],
            Field(default=None, description="Prior session_id; omit for new session."),
        ] = None,
        session_reuse: Annotated[
            bool,
            Field(
                default=False,
                description="Keep session for follow-up; pass session_id; stop_session when done.",
            ),
        ] = False,
    ) -> dict[str, Any]:
        """Download a workspace file to a local path (same as CodeInterpreterClient.download_file)."""
        cfg = _load_server()

        def _op(c: Any) -> None:
            c.download_file(remote_path, local_path)

        _, meta, sid = _call_with_session_recovery(
            session_id, session_reuse, cfg, CodeInterpreterClient, _op
        )
        payload: dict[str, Any] = {
            "session_id": sid,
            "remote_path": remote_path,
            "local_path": local_path,
            "status": "ok",
        }
        payload.update(meta)
        return payload

    @app.tool(structured_output=False)
    def stop_session(
        session_id: Annotated[
            str,
            Field(description="session_id to stop (from session_reuse)."),
        ],
    ) -> dict[str, Any]:
        """Stop a reused session."""
        cfg = _load_server()
        with _SESSION_LOCK:
            client = _SESSIONS.pop(session_id, None)
        if client is None:
            c = CodeInterpreterClient(
                name=cfg.code_interpreter_name,
                namespace=cfg.namespace,
                ttl=cfg.ttl,
                workload_manager_url=cfg.workload_manager_url,
                router_url=cfg.router_url,
                auth_token=cfg.auth_token,
                session_id=session_id,
            )
            c.stop()
            return {"session_id": session_id, "status": "stopped"}
        client.stop()
        return {"session_id": session_id, "status": "stopped"}

    return app
