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

import os

from agentcube import AgentRuntimeClient


def main() -> None:
    client = AgentRuntimeClient(
        agent_name=os.getenv("AGENT_NAME", "claude-code-agent"),
        router_url=os.getenv("ROUTER_URL", "http://localhost:8081"),
        namespace=os.getenv("NAMESPACE", "default"),
        session_id=os.getenv("AGENTCUBE_SESSION_ID") or None,
        timeout=int(os.getenv("TIMEOUT", "300")),
    )

    result = client.invoke(
        {
            "prompt": os.getenv("PROMPT", "Reply with OK only."),
            "max_turns": int(os.getenv("MAX_TURNS", "3")),
        },
        timeout=int(os.getenv("TIMEOUT", "300")),
    )

    print("session_id=", client.session_id)
    print("result=", result)


if __name__ == "__main__":
    main()
