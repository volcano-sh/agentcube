# Copyright The Volcano Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.

"""FastMCP server backed by AgentCube CodeInterpreterClient."""

from __future__ import annotations

import json
import os
import threading
from dataclasses import dataclass
from typing import Annotated, Any, Optional

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


def _json_ok(payload: dict[str, Any]) -> str:
    return json.dumps(payload, ensure_ascii=False)


def create_mcp_server(
    *,
    host: str = "127.0.0.1",
    port: int = 8000,
    instructions: str | None = None,
) -> FastMCP:
    instr = instructions or (
        "Run code and shell commands in an isolated AgentCube Code Interpreter sandbox. "
        "For multi-step tasks (write file then run code, list then read, etc.), set session_reuse=true "
        "and pass session_id from the previous JSON response on each follow-up call so the same sandbox "
        "workspace stays available. IMPORTANT: each run_code starts a new Python process—variables do not "
        "carry between run_code calls; only workspace files persist while the session is open. "
        "When finished with a reused session, call stop_session with that session_id."
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
                description="Interpreter language for CodeInterpreterClient.run_code (e.g. python, bash)."
            ),
        ],
        code: Annotated[str, Field(description="Source or script body to execute in the sandbox.")],
        session_id: Annotated[
            Optional[str],
            Field(
                default=None,
                description=(
                    "Continue an existing sandbox: use the session_id string from a prior tool JSON response. "
                    "Omit to start a new session."
                ),
            ),
        ] = None,
        session_reuse: Annotated[
            bool,
            Field(
                default=False,
                description=(
                    "If false (default), the server stops the sandbox after this call—later calls cannot rely "
                    "on files from this session. If true, the sandbox stays alive for follow-up tools; you MUST "
                    "pass the returned session_id on the next call and call stop_session when done to free resources."
                ),
            ),
        ] = False,
        timeout_seconds: Annotated[
            Optional[float],
            Field(
                default=None,
                description="Optional execution timeout in seconds for this run_code invocation.",
            ),
        ] = None,
    ) -> str:
        """Execute code in an AgentCube Code Interpreter session (via Router)."""
        cfg = _load_server()
        client = _client_for_call(session_id, session_reuse, cfg, CodeInterpreterClient)
        try:
            out = client.run_code(language, code, timeout=timeout_seconds)
            return _json_ok(
                {
                    "session_id": client.session_id,
                    "output": out,
                }
            )
        finally:
            _cleanup_after_call(client, session_reuse)

    @app.tool(structured_output=False)
    def execute_command(
        command: Annotated[str, Field(description="Shell command to run inside the interpreter sandbox.")],
        session_id: Annotated[
            Optional[str],
            Field(
                default=None,
                description="Existing session_id from a prior tool response; omit for a new session.",
            ),
        ] = None,
        session_reuse: Annotated[
            bool,
            Field(
                default=False,
                description=(
                    "False: stop sandbox after this call. True: keep sandbox for follow-up; pass session_id next "
                    "and stop_session when finished."
                ),
            ),
        ] = False,
        timeout_seconds: Annotated[
            Optional[float],
            Field(default=None, description="Optional timeout in seconds for this command."),
        ] = None,
    ) -> str:
        """Run a shell command in the Code Interpreter sandbox."""
        cfg = _load_server()
        client = _client_for_call(session_id, session_reuse, cfg, CodeInterpreterClient)
        try:
            out = client.execute_command(command, timeout=timeout_seconds)
            return _json_ok({"session_id": client.session_id, "output": out})
        finally:
            _cleanup_after_call(client, session_reuse)

    @app.tool(structured_output=False)
    def write_file(
        content: Annotated[str, Field(description="File contents to write (text).")],
        remote_path: Annotated[
            str,
            Field(
                description="Path relative to the interpreter workspace (e.g. data/input.txt)."
            ),
        ],
        session_id: Annotated[
            Optional[str],
            Field(default=None, description="Existing session_id; omit to create a new session."),
        ] = None,
        session_reuse: Annotated[
            bool,
            Field(
                default=False,
                description=(
                    "Set true when more steps will read or run against this file; then pass session_id on "
                    "follow-up tools and stop_session when done. If false, the session ends after this write."
                ),
            ),
        ] = False,
    ) -> str:
        """Write text to a path inside the interpreter workspace (CodeInterpreterClient.write_file)."""
        cfg = _load_server()
        client = _client_for_call(session_id, session_reuse, cfg, CodeInterpreterClient)
        try:
            client.write_file(content, remote_path)
            return _json_ok({"session_id": client.session_id, "remote_path": remote_path, "status": "ok"})
        finally:
            _cleanup_after_call(client, session_reuse)

    @app.tool(structured_output=False)
    def list_files(
        path: Annotated[
            str,
            Field(
                default=".",
                description="Directory path inside the workspace to list (default: current/workspace root).",
            ),
        ] = ".",
        session_id: Annotated[
            Optional[str],
            Field(default=None, description="Existing session_id; omit to create a new session."),
        ] = None,
        session_reuse: Annotated[
            bool,
            Field(
                default=False,
                description="True to keep the session after listing for further tools; false to tear down after.",
            ),
        ] = False,
    ) -> str:
        """List files in the interpreter workspace (CodeInterpreterClient.list_files)."""
        cfg = _load_server()
        client = _client_for_call(session_id, session_reuse, cfg, CodeInterpreterClient)
        try:
            files = client.list_files(path)
            return _json_ok({"session_id": client.session_id, "path": path, "entries": files})
        finally:
            _cleanup_after_call(client, session_reuse)

    @app.tool(structured_output=False)
    def stop_session(
        session_id: Annotated[
            str,
            Field(description="The session_id to terminate; must match a session you opened with session_reuse."),
        ],
    ) -> str:
        """Stop and delete a session (CodeInterpreterClient.stop); required after session_reuse."""
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
            return _json_ok({"session_id": session_id, "status": "stopped"})
        client.stop()
        return _json_ok({"session_id": session_id, "status": "stopped"})

    return app
