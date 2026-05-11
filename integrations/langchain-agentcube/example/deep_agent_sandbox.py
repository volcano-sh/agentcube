#!/usr/bin/env python3
# Copyright The Volcano Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.

"""Example: create_deep_agent with AgentcubeSandbox. See ../README.md."""

from __future__ import annotations

import argparse
import os
import sys
from pathlib import Path
from typing import Any


def _require_env(name: str) -> str:
    v = os.environ.get(name)
    if not v:
        print(f"Error: environment variable {name} is not set", file=sys.stderr)
        sys.exit(1)
    return v


def _make_chat_model() -> object:
    if os.environ.get("DEEPSEEK_API_KEY"):
        from langchain_openai import ChatOpenAI

        return ChatOpenAI(
            model=os.environ.get("DEEPSEEK_MODEL", "deepseek-chat"),
            api_key=os.environ["DEEPSEEK_API_KEY"],
            base_url=os.environ.get("DEEPSEEK_API_BASE", "https://api.deepseek.com/v1"),
            temperature=0,
        )
    if os.environ.get("ANTHROPIC_API_KEY"):
        from langchain_anthropic import ChatAnthropic

        return ChatAnthropic(
            model=os.environ.get("ANTHROPIC_MODEL", "claude-haiku-4-5-20251001"),
            temperature=0,
        )
    if os.environ.get("OPENAI_API_KEY"):
        from langchain_openai import ChatOpenAI

        kwargs: dict[str, Any] = {
            "model": os.environ.get("OPENAI_MODEL", "gpt-4o-mini"),
            "api_key": os.environ["OPENAI_API_KEY"],
            "temperature": 0,
        }
        if os.environ.get("OPENAI_API_BASE"):
            kwargs["base_url"] = os.environ["OPENAI_API_BASE"]
        return ChatOpenAI(**kwargs)
    print(
        "Error: set DEEPSEEK_API_KEY, ANTHROPIC_API_KEY, or OPENAI_API_KEY.",
        file=sys.stderr,
    )
    sys.exit(1)


def main() -> None:
    parser = argparse.ArgumentParser(description="Deep Agents + AgentcubeSandbox")
    parser.add_argument(
        "--interpreter",
        default=os.environ.get("CODE_INTERPRETER_NAME", "my-interpreter"),
    )
    parser.add_argument(
        "--namespace",
        default=os.environ.get("AGENTCUBE_NAMESPACE", "default"),
    )
    parser.add_argument(
        "--prompt",
        default="Write and run a Python script to print 'Hello, World!'",
    )
    args = parser.parse_args()

    _require_env("WORKLOAD_MANAGER_URL")
    _require_env("ROUTER_URL")
    sys.path.insert(0, str(Path(__file__).resolve().parents[3] / "sdk-python"))

    from agentcube import CodeInterpreterClient
    from deepagents import create_deep_agent
    from langchain_agentcube import AgentcubeSandbox

    model = _make_chat_model()
    with CodeInterpreterClient(
        name=args.interpreter,
        namespace=args.namespace,
        router_url=os.environ["ROUTER_URL"],
        workload_manager_url=os.environ["WORKLOAD_MANAGER_URL"],
        auth_token=os.environ.get("API_TOKEN"),
    ) as client:
        agent = create_deep_agent(
            model=model,
            system_prompt="You are a coding assistant with sandbox access.",
            backend=AgentcubeSandbox(client=client),
        )
        try:
            result = agent.invoke(
                {"messages": [{"role": "user", "content": args.prompt}]},
            )
        except Exception as e:
            print(f"Error: {e}", file=sys.stderr)
            sys.exit(1)
    print(result)


if __name__ == "__main__":
    main()
