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
from typing import Any, Optional

from mcp.server.fastmcp import FastMCP

_SESSION_LOCK = threading.RLock()
_SESSIONS: dict[str, Any] = {}


@dataclass(frozen=True)
class ServerEnv:
    router_url: str
    workload_manager_url: str
    namespace: str
    code_interpreter_name: str
    auth_token: Optional[str]
    ttl: int


def _load_env() -> ServerEnv:
    router = os.environ.get("ROUTER_URL", "").strip()
    wm = os.environ.get("WORKLOAD_MANAGER_URL", "").strip()
    if not router:
        raise RuntimeError("ROUTER_URL is required")
    if not wm:
        raise RuntimeError("WORKLOAD_MANAGER_URL is required")
    token = os.environ.get("API_TOKEN") or os.environ.get("AGENTCUBE_API_TOKEN") or None
    if token is not None:
        token = token.strip() or None
    return ServerEnv(
        router_url=router,
        workload_manager_url=wm,
        namespace=os.environ.get("AGENTCUBE_NAMESPACE", "default").strip() or "default",
        code_interpreter_name=os.environ.get("CODE_INTERPRETER_NAME", "my-interpreter").strip()
        or "my-interpreter",
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
    env: ServerEnv,
    CodeInterpreterClient: type,
):
    with _SESSION_LOCK:
        if session_id:
            cached = _SESSIONS.get(session_id)
            if cached is not None:
                return cached
            client = CodeInterpreterClient(
                name=env.code_interpreter_name,
                namespace=env.namespace,
                ttl=env.ttl,
                workload_manager_url=env.workload_manager_url,
                router_url=env.router_url,
                auth_token=env.auth_token,
                session_id=session_id,
            )
            if session_reuse:
                _SESSIONS[session_id] = client
            return client
        client = CodeInterpreterClient(
            name=env.code_interpreter_name,
            namespace=env.namespace,
            ttl=env.ttl,
            workload_manager_url=env.workload_manager_url,
            router_url=env.router_url,
            auth_token=env.auth_token,
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
        "AgentCube Code Interpreter tools. Set ROUTER_URL and WORKLOAD_MANAGER_URL on the server. "
        "Use session_reuse=true to keep a sandbox session for multiple calls; pass session_id on later calls. "
        "Call stop_session when finished if you used session_reuse."
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

    @app.tool()
    def run_code(
        language: str,
        code: str,
        session_id: Optional[str] = None,
        session_reuse: bool = False,
        timeout_seconds: Optional[float] = None,
    ) -> str:
        """Execute code in an AgentCube Code Interpreter session (via Router)."""
        env = _load_env()
        client = _client_for_call(session_id, session_reuse, env, CodeInterpreterClient)
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

    @app.tool()
    def execute_command(
        command: str,
        session_id: Optional[str] = None,
        session_reuse: bool = False,
        timeout_seconds: Optional[float] = None,
    ) -> str:
        """Run a shell command in the Code Interpreter sandbox."""
        env = _load_env()
        client = _client_for_call(session_id, session_reuse, env, CodeInterpreterClient)
        try:
            out = client.execute_command(command, timeout=timeout_seconds)
            return _json_ok({"session_id": client.session_id, "output": out})
        finally:
            _cleanup_after_call(client, session_reuse)

    @app.tool()
    def write_file(
        content: str,
        remote_path: str,
        session_id: Optional[str] = None,
        session_reuse: bool = False,
    ) -> str:
        """Write text to a path inside the interpreter workspace (CodeInterpreterClient.write_file)."""
        env = _load_env()
        client = _client_for_call(session_id, session_reuse, env, CodeInterpreterClient)
        try:
            client.write_file(content, remote_path)
            return _json_ok({"session_id": client.session_id, "remote_path": remote_path, "status": "ok"})
        finally:
            _cleanup_after_call(client, session_reuse)

    @app.tool()
    def list_files(
        path: str = ".",
        session_id: Optional[str] = None,
        session_reuse: bool = False,
    ) -> str:
        """List files in the interpreter workspace (CodeInterpreterClient.list_files)."""
        env = _load_env()
        client = _client_for_call(session_id, session_reuse, env, CodeInterpreterClient)
        try:
            files = client.list_files(path)
            return _json_ok({"session_id": client.session_id, "path": path, "entries": files})
        finally:
            _cleanup_after_call(client, session_reuse)

    @app.tool()
    def stop_session(session_id: str) -> str:
        """Stop and delete a session (CodeInterpreterClient.stop); required after session_reuse."""
        env = _load_env()
        with _SESSION_LOCK:
            client = _SESSIONS.pop(session_id, None)
        if client is None:
            c = CodeInterpreterClient(
                name=env.code_interpreter_name,
                namespace=env.namespace,
                ttl=env.ttl,
                workload_manager_url=env.workload_manager_url,
                router_url=env.router_url,
                auth_token=env.auth_token,
                session_id=session_id,
            )
            c.stop()
            return _json_ok({"session_id": session_id, "status": "stopped"})
        client.stop()
        return _json_ok({"session_id": session_id, "status": "stopped"})

    return app
