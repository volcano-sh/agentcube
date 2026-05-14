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

"""
Browser Agent — an AI agent that handles web search and analysis tasks
by delegating browser automation to a Playwright MCP tool running in an
AgentCube sandbox.

Architecture:
    Client ──HTTP──▶ Browser Agent ──Router──▶ Playwright MCP Tool (sandbox)
                     (this service)             (BrowserUse/AgentRuntime pod)

The agent receives natural-language requests, connects to the Playwright MCP
server through the AgentCube Router, lets the LLM choose browser tools, and
returns a concise summary.
"""

from __future__ import annotations

import json
import logging
import os
from typing import Optional

import httpx
import uvicorn
from fastapi import FastAPI, HTTPException
from langchain_core.messages import HumanMessage, SystemMessage, ToolMessage
from langchain_openai import ChatOpenAI
from mcp import ClientSession, types
from mcp.client.streamable_http import streamable_http_client
from pydantic import BaseModel, Field

logging.basicConfig(
    level=os.environ.get("LOG_LEVEL", "INFO"),
    format="%(asctime)s [%(levelname)s] %(name)s - %(message)s",
)
log = logging.getLogger(__name__)

# ========================= Configuration =========================
OPENAI_API_KEY = os.environ.get("OPENAI_API_KEY", "")
OPENAI_API_BASE = os.environ.get("OPENAI_API_BASE", "https://api.openai.com/v1")
OPENAI_MODEL = os.environ.get("OPENAI_MODEL", "gpt-4o")

ROUTER_URL = os.environ.get("ROUTER_URL", "http://agentcube-router.agentcube.svc.cluster.local:8080")
PLAYWRIGHT_MCP_NAME = os.environ.get("PLAYWRIGHT_MCP_NAME", "browser-use-tool")
PLAYWRIGHT_MCP_NAMESPACE = os.environ.get("PLAYWRIGHT_MCP_NAMESPACE", "default")
PLAYWRIGHT_MCP_KIND = os.environ.get("PLAYWRIGHT_MCP_KIND", "BrowserUse")
AGENTCUBE_SESSION_HEADER = "x-agentcube-session-id"

# Timeout for browser tasks
BROWSER_TASK_TIMEOUT = int(os.environ.get("BROWSER_TASK_TIMEOUT", "300"))
MAX_TOOL_ROUNDS = int(os.environ.get("MAX_TOOL_ROUNDS", "10"))

SERVER_HOST = "0.0.0.0"
SERVER_PORT = int(os.environ.get("PORT", "8000"))

RESOURCE_PATH_BY_KIND = {
    "agentruntime": "agent-runtimes",
    "browseruse": "browser-uses",
}


def _resource_path_for_kind(kind: str) -> str:
    normalized_kind = kind.strip().lower()
    resource_path = RESOURCE_PATH_BY_KIND.get(normalized_kind)
    if resource_path:
        return resource_path

    supported = ", ".join(sorted(["AgentRuntime", "BrowserUse"]))
    raise ValueError(
        f"Unsupported PLAYWRIGHT_MCP_KIND '{kind}'. Supported values: {supported}"
    )


# ========================= LLM Setup =========================
PLANNER_SYSTEM_PROMPT = """\
You are a browser task planner. Given a user request, produce a clear,
actionable browser task description for a Playwright MCP browser tool.

Rules:
- Output ONLY a JSON object: {"task": "<browser task description>", "allowed_domains": []}
- The task must be specific and self-contained
- If the request involves a known website, include the URL in the task
- For search requests, instruct the agent to use a search engine
- Keep the task concise but include all details from the user request
- allowed_domains can be left empty (allow all) or restricted for safety

Examples:
  User: "Find the latest Kubernetes release notes"
    Output: {
        "task": "Go to https://kubernetes.io/releases/ and extract the latest "
                        "release version number and key highlights from the release notes.",
        "allowed_domains": ["kubernetes.io"]
    }

  User: "Search for the best Python web frameworks in 2025"
    Output: {
        "task": "Search Google for 'best Python web frameworks 2025', open the "
                        "top 3 results, and summarize the recommended frameworks with "
                        "pros and cons.",
        "allowed_domains": []
    }
"""


def get_llm() -> ChatOpenAI:
    return ChatOpenAI(
        model=OPENAI_MODEL,
        api_key=OPENAI_API_KEY,
        base_url=OPENAI_API_BASE,
        temperature=0.2,
    )


# ========================= Request / Response Models =========================
class ChatRequest(BaseModel):
    message: str = Field(..., description="User's natural-language request", min_length=1)
    session_id: Optional[str] = Field(
        None,
        description="AgentCube session ID for reusing the same browser sandbox",
    )
    max_steps: int = Field(10, description="Maximum browser agent steps", ge=1, le=200)


class ChatResponse(BaseModel):
    answer: str
    success: bool
    session_id: Optional[str] = None
    urls_visited: list[str] = []
    steps: int = 0


def _tool_to_openai_schema(tool: types.Tool) -> dict:
    return {
        "type": "function",
        "function": {
            "name": tool.name,
            "description": tool.description or tool.name,
            "parameters": tool.inputSchema or {"type": "object", "properties": {}},
        },
    }


def _render_tool_result(result: types.CallToolResult) -> str:
    parts: list[str] = []
    if getattr(result, "structuredContent", None):
        parts.append(json.dumps(result.structuredContent, ensure_ascii=True))
    for item in result.content:
        if isinstance(item, types.TextContent):
            parts.append(item.text)
        elif isinstance(item, types.ImageContent):
            parts.append(f"[image:{item.mimeType} {len(item.data)} bytes]")
        else:
            parts.append(str(item))
    return "\n".join(part for part in parts if part).strip() or ""


def _message_content_to_text(content: object) -> str:
    if isinstance(content, str):
        return content.strip()
    if isinstance(content, list):
        parts: list[str] = []
        for item in content:
            if isinstance(item, str):
                parts.append(item)
                continue
            if isinstance(item, dict):
                text = item.get("text") or item.get("content")
                if isinstance(text, str):
                    parts.append(text)
                    continue
            text = getattr(item, "text", None)
            if isinstance(text, str):
                parts.append(text)
        return "\n".join(part.strip() for part in parts if part and part.strip())
    return str(content).strip() if content is not None else ""


class PlaywrightMCPClient:
    """Client for calling the Playwright MCP tool via AgentCube Router."""

    def __init__(self):
        resource_path = _resource_path_for_kind(PLAYWRIGHT_MCP_KIND)
        self.base_url = (
            f"{ROUTER_URL}/v1/namespaces/{PLAYWRIGHT_MCP_NAMESPACE}"
            f"/{resource_path}/{PLAYWRIGHT_MCP_NAME}/invocations/mcp"
        )
        self.session_id: Optional[str] = None

    async def _synthesize_final_answer(self, messages: list, user_message: str) -> str:
        final_message = await get_llm().ainvoke(
            [
                SystemMessage(
                    content=(
                        "You must stop using tools now. Based only on the browser evidence already "
                        "collected in the conversation, answer the user's request directly. If the "
                        "evidence is incomplete, say what you know and what remains uncertain."
                    )
                ),
                *messages,
                HumanMessage(
                    content=(
                        "Stop browsing and provide the best possible final answer for this request: "
                        f"{user_message}"
                    )
                ),
            ]
        )
        return _message_content_to_text(final_message.content)

    async def run_task(
        self,
        user_message: str,
        planned_task: str,
        allowed_domains: Optional[list[str]] = None,
        session_id: Optional[str] = None,
        max_steps: int = MAX_TOOL_ROUNDS,
        max_rounds: Optional[int] = None,
    ) -> dict:
        captured_session_id = session_id or self.session_id
        transport_client_holder: dict[str, httpx.AsyncClient] = {}
        tool_round_limit = max_rounds or max_steps

        async def inject_session_header(request: httpx.Request) -> None:
            if captured_session_id:
                request.headers[AGENTCUBE_SESSION_HEADER] = captured_session_id

        async def capture_response(response: httpx.Response) -> None:
            nonlocal captured_session_id
            new_session_id = response.headers.get(AGENTCUBE_SESSION_HEADER)
            if new_session_id:
                captured_session_id = new_session_id
                transport_client = transport_client_holder.get("client")
                if transport_client is not None:
                    transport_client.headers[AGENTCUBE_SESSION_HEADER] = new_session_id

        headers = {}
        if captured_session_id:
            headers[AGENTCUBE_SESSION_HEADER] = captured_session_id

        async with httpx.AsyncClient(
            timeout=BROWSER_TASK_TIMEOUT,
            headers=headers,
            event_hooks={
                "request": [inject_session_header],
                "response": [capture_response],
            },
        ) as transport_client:
            transport_client_holder["client"] = transport_client
            async with streamable_http_client(
                self.base_url, http_client=transport_client
            ) as (
                read_stream,
                write_stream,
                _,
            ):
                async with ClientSession(read_stream, write_stream) as session:
                    await session.initialize()
                    tools_response = await session.list_tools()
                    tool_schemas = [_tool_to_openai_schema(tool) for tool in tools_response.tools]

                    llm = get_llm().bind_tools(tool_schemas)
                    system_prompt = (
                        "You are a browser research agent. Use the available Playwright MCP tools "
                        "to search the web, navigate pages, inspect snapshots, and gather evidence. "
                        "Prefer browser_snapshot after navigation to understand the page. "
                        "Use browser_navigate for URLs, browser_type and browser_press_key for search boxes, "
                        "and browser_click only when you have the exact target from a snapshot. "
                        "When you have enough information, answer directly and do not call more tools."
                    )
                    if allowed_domains:
                        system_prompt += (
                            " Restrict browsing to these domains when possible: "
                            + ", ".join(allowed_domains)
                            + "."
                        )

                    messages = [
                        SystemMessage(content=system_prompt),
                        HumanMessage(
                            content=(
                                f"User request: {user_message}\n\n"
                                f"Planned browser task: {planned_task}"
                            )
                        ),
                    ]

                    steps = 0
                    stop_reason: Optional[str] = None
                    for _ in range(tool_round_limit):
                        ai_message = await llm.ainvoke(messages)
                        messages.append(ai_message)

                        if not ai_message.tool_calls:
                            self.session_id = captured_session_id
                            return {
                                "success": True,
                                "result": _message_content_to_text(ai_message.content),
                                "steps": steps,
                                "session_id": captured_session_id,
                            }

                        remaining_steps = max_steps - steps
                        if remaining_steps <= 0:
                            stop_reason = "tool step limit reached"
                            break

                        for tool_call in ai_message.tool_calls[:remaining_steps]:
                            steps += 1
                            result = await session.call_tool(
                                tool_call["name"],
                                arguments=tool_call.get("args", {}),
                            )
                            rendered = _render_tool_result(result)
                            messages.append(
                                ToolMessage(
                                    content=rendered,
                                    tool_call_id=tool_call["id"],
                                )
                            )

                        if len(ai_message.tool_calls) > remaining_steps:
                            stop_reason = "tool step limit reached"
                            break

                    if stop_reason is None:
                        stop_reason = "tool loop exceeded maximum rounds"

                    final_answer = await self._synthesize_final_answer(messages, user_message)

                    self.session_id = captured_session_id
                    return {
                        "success": bool(final_answer),
                        "result": final_answer,
                        "error": stop_reason,
                        "steps": steps,
                        "session_id": captured_session_id,
                    }


# ========================= FastAPI App =========================
app = FastAPI(title="Browser Agent", description="AI agent with Playwright MCP tool")
browser_client = PlaywrightMCPClient()


@app.post("/chat", response_model=ChatResponse)
async def chat(req: ChatRequest):
    """Handle a user chat message by planning and executing a browser task."""
    if not OPENAI_API_KEY:
        raise HTTPException(status_code=500, detail="OPENAI_API_KEY not configured")

    log.info("Received chat request: %s", req.message[:100])

    # Step 1: Use LLM to plan the browser task
    llm = get_llm()
    planning_response = await llm.ainvoke(
        [
            SystemMessage(content=PLANNER_SYSTEM_PROMPT),
            HumanMessage(content=req.message),
        ]
    )

    try:
        # Extract JSON from LLM response (handle markdown code blocks)
        content = planning_response.content.strip()
        if content.startswith("```"):
            content = content.split("\n", 1)[1].rsplit("```", 1)[0].strip()
        plan = json.loads(content)
        task = plan["task"]
        allowed_domains = plan.get("allowed_domains", [])
    except (json.JSONDecodeError, KeyError) as e:
        log.warning("Failed to parse LLM plan, using raw message: %s", e)
        task = req.message
        allowed_domains = []

    log.info("Planned browser task: %s", task[:200])

    # Step 2: Execute browser task via the Playwright MCP tool
    try:
        result = await browser_client.run_task(
            user_message=req.message,
            planned_task=task,
            allowed_domains=allowed_domains,
            session_id=req.session_id,
            max_steps=req.max_steps,
        )
    except httpx.TimeoutException:
        return ChatResponse(
            answer="The browser task timed out. Try a simpler request.",
            success=False,
        )
    except HTTPException:
        raise
    except Exception as e:
        log.exception("Browser task execution failed")
        return ChatResponse(answer=f"Error: {e}", success=False)

    # Step 3: Format the result for the user
    success = result.get("success", False)
    raw_result = result.get("result", "No result returned")

    if success and raw_result:
        # Use LLM to produce a user-friendly summary
        summary_response = await llm.ainvoke(
            [
                SystemMessage(
                    content="Summarize the following browser task result into a clear, "
                    "helpful answer for the user. Be concise."
                ),
                HumanMessage(
                    content=f"User request: {req.message}\n\nBrowser result:\n{raw_result}"
                ),
            ]
        )
        answer = summary_response.content
    elif raw_result and raw_result != "No result returned":
        answer = raw_result
    else:
        error = result.get("error", "Unknown error")
        answer = f"The browser task did not succeed: {error}" if not success else raw_result

    return ChatResponse(
        answer=answer,
        success=success,
        session_id=result.get("session_id"),
        urls_visited=result.get("urls_visited", []),
        steps=result.get("steps", 0),
    )


@app.get("/health")
async def health():
    return {"status": "ok", "service": "browser-agent"}


if __name__ == "__main__":
    uvicorn.run(
        "browser_agent:app",
        host=SERVER_HOST,
        port=SERVER_PORT,
        log_level="info",
    )
