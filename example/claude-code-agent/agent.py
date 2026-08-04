#!/usr/bin/env python3
#
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

import asyncio
import inspect
import os
from typing import Any, AsyncIterator, Callable, Dict, Optional, Tuple


SERVER_HOST = "0.0.0.0"
SERVER_PORT = int(os.environ.get("PORT", "8080"))
WORKSPACE_DIR = os.environ.get("WORKSPACE_DIR", "/workspace")
MAX_TURNS = int(os.environ.get("MAX_TURNS", "10"))
ALLOWED_TOOLS = [
    tool.strip()
    for tool in os.environ.get("ALLOWED_TOOLS", "Read,Edit,Glob").split(",")
    if tool.strip()
]
PERMISSION_MODE = os.environ.get("PERMISSION_MODE", "acceptEdits")


def _load_claude_sdk() -> tuple[Callable[..., AsyncIterator[Any]], type]:
    try:
        from claude_agent_sdk import ClaudeAgentOptions as ClaudeCodeOptions, query
    except ImportError as exc:
        try:
            from claude_code_sdk import ClaudeCodeOptions, query
        except ImportError:
            raise RuntimeError(
                "claude-agent-sdk is not installed. Install requirements.txt or rebuild the example image."
            ) from exc
    return query, ClaudeCodeOptions


def _message_class_name(value: Any) -> str:
    return value.__class__.__name__


def _content_blocks(message: Any) -> list[Any]:
    content = getattr(message, "content", None)
    if content is None:
        return []
    if isinstance(content, list):
        return content
    return [content]


def _block_text(block: Any) -> Optional[str]:
    if isinstance(block, str):
        return block
    text = getattr(block, "text", None)
    if isinstance(text, str):
        return text
    if isinstance(block, dict):
        text = block.get("text")
        if isinstance(text, str):
            return text
    return None


def _tool_call_from_block(block: Any) -> Optional[dict[str, Any]]:
    block_name = _message_class_name(block).lower()
    name = getattr(block, "name", None)
    tool_input = getattr(block, "input", None)

    if isinstance(block, dict):
        name = block.get("name")
        tool_input = block.get("input")
        block_type = str(block.get("type", "")).lower()
    else:
        block_type = block_name

    if not name or "tool" not in block_type:
        return None

    return {
        "name": name,
        "input": tool_input if tool_input is not None else {},
    }


def _usage_to_dict(usage: Any) -> Optional[dict[str, Any]]:
    if usage is None:
        return None
    if isinstance(usage, dict):
        return usage
    if hasattr(usage, "model_dump"):
        return usage.model_dump()
    if hasattr(usage, "__dict__"):
        return dict(usage.__dict__)
    return {"value": str(usage)}


def _model_name() -> str:
    return os.environ.get("ANTHROPIC_MODEL") or os.environ.get("CLAUDE_MODEL", "deepseek-v4-flash")


def _claude_cli_env() -> dict[str, str]:
    env: dict[str, str] = {}
    for key in (
        "ANTHROPIC_API_KEY",
        "ANTHROPIC_AUTH_TOKEN",
        "ANTHROPIC_BASE_URL",
        "ANTHROPIC_MODEL",
        "ANTHROPIC_CUSTOM_MODEL_OPTION",
    ):
        value = os.environ.get(key)
        if value:
            env[key] = value

    if "ANTHROPIC_AUTH_TOKEN" in env and "ANTHROPIC_API_KEY" not in env:
        env["ANTHROPIC_API_KEY"] = env["ANTHROPIC_AUTH_TOKEN"]

    model_name = _model_name()
    env.setdefault("ANTHROPIC_MODEL", model_name)
    env.setdefault("ANTHROPIC_CUSTOM_MODEL_OPTION", model_name)
    return env


async def run_claude_agent(
    *,
    prompt: str,
    max_turns: int = MAX_TURNS,
    query_fn: Optional[Callable[..., AsyncIterator[Any]]] = None,
    options_cls: Optional[type] = None,
) -> dict[str, Any]:
    if query_fn is None or options_cls is None:
        loaded_query, loaded_options = _load_claude_sdk()
        query_fn = query_fn or loaded_query
        options_cls = options_cls or loaded_options

    options = options_cls(
        cwd=WORKSPACE_DIR,
        allowed_tools=ALLOWED_TOOLS,
        permission_mode=PERMISSION_MODE,
        max_turns=max_turns,
        model=_model_name(),
        env=_claude_cli_env(),
        system_prompt=(
            "You are running inside an AgentCube AgentRuntime sandbox. "
            "Use the workspace as the source of truth, keep edits scoped to the user request, "
            "and explain the result clearly."
        ),
    )

    transcript: list[str] = []
    tool_calls: list[dict[str, Any]] = []
    answer = ""
    claude_session_id = None
    usage = None
    total_cost_usd = None

    async for message in query_fn(prompt=prompt, options=options):
        if _message_class_name(message) == "ResultMessage" or hasattr(message, "result"):
            answer = getattr(message, "result", "") or answer
            claude_session_id = getattr(message, "session_id", None)
            usage = _usage_to_dict(getattr(message, "usage", None))
            total_cost_usd = getattr(message, "total_cost_usd", None)
            continue

        for block in _content_blocks(message):
            text = _block_text(block)
            if text:
                transcript.append(text)

            tool_call = _tool_call_from_block(block)
            if tool_call:
                tool_calls.append(tool_call)

    return {
        "answer": answer,
        "claude_session_id": claude_session_id,
        "tool_calls": tool_calls,
        "transcript": transcript,
        "usage": usage,
        "total_cost_usd": total_cost_usd,
    }


def _run_sync(value: Any) -> Any:
    if inspect.isawaitable(value):
        return asyncio.run(value)
    return value


def _parse_invoke_payload(
    payload: Dict[str, Any],
) -> tuple[Optional[str], Optional[int], Optional[tuple[dict[str, Any], int]]]:
    prompt = str(payload.get("prompt", "")).strip()
    if not prompt:
        return None, None, ({"error": "prompt is required"}, 400)

    try:
        max_turns = int(payload.get("max_turns", MAX_TURNS))
    except (TypeError, ValueError):
        return None, None, ({"error": "max_turns must be an integer"}, 400)

    if max_turns < 1:
        return None, None, ({"error": "max_turns must be greater than zero"}, 400)

    return prompt, max_turns, None


def handle_invoke_payload(payload: Dict[str, Any]) -> Tuple[dict[str, Any], int]:
    prompt, max_turns, error = _parse_invoke_payload(payload)
    if error:
        return error

    try:
        result = _run_sync(run_claude_agent(prompt=prompt, max_turns=max_turns))
    except Exception as exc:
        return {"error": str(exc)}, 500
    return result, 200


async def handle_invoke_payload_async(payload: Dict[str, Any]) -> Tuple[dict[str, Any], int]:
    prompt, max_turns, error = _parse_invoke_payload(payload)
    if error:
        return error

    try:
        result = await run_claude_agent(prompt=prompt, max_turns=max_turns)
    except Exception as exc:
        return {"error": str(exc)}, 500
    return result, 200


def create_app():
    try:
        from fastapi import FastAPI, HTTPException
        from pydantic import BaseModel, Field
    except ImportError as exc:
        raise RuntimeError(
            "fastapi and pydantic are required to run the HTTP server. "
            "Install requirements.txt or rebuild the example image."
        ) from exc

    class InvokeRequest(BaseModel):
        prompt: str = Field(..., min_length=1)
        max_turns: int = Field(MAX_TURNS, ge=1, le=100)

    app = FastAPI(
        title="Claude Code AgentRuntime Example",
        description="Runs Claude Code SDK query() inside an AgentCube AgentRuntime sandbox.",
    )

    @app.get("/health")
    async def health() -> dict[str, str]:
        return {"status": "healthy", "agent": "claude-code-agent"}

    @app.get("/")
    async def info() -> dict[str, Any]:
        return {
            "agent": "claude-code-agent",
            "endpoints": ["GET /health", "POST /"],
            "workspace": WORKSPACE_DIR,
            "allowed_tools": ALLOWED_TOOLS,
        }

    @app.post("/")
    async def invoke(request: InvokeRequest) -> dict[str, Any]:
        response, status_code = await handle_invoke_payload_async(request.model_dump())
        if status_code >= 400:
            raise HTTPException(status_code=status_code, detail=response["error"])
        return response

    return app


def main() -> None:
    import uvicorn

    uvicorn.run(create_app(), host=SERVER_HOST, port=SERVER_PORT)


if __name__ == "__main__":
    main()
