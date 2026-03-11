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

from agentcube import AsyncAgentRuntimeClient


async def main():
    # first time: it will create a new pod
    async with AsyncAgentRuntimeClient(
        agent_name="my-agent",
        router_url="http://localhost:18081",
        namespace="default",
        verbose=True,
    ) as client_v1:
        print(client_v1.session_id)

        result_v1 = await client_v1.invoke(payload={"prompt": "Hello World!"})
        print(result_v1)

        session_id = client_v1.session_id

    # second time: reuse the pod created above
    async with AsyncAgentRuntimeClient(
        agent_name="my-agent",
        router_url="http://localhost:18081",
        namespace="default",
        session_id=session_id,
        verbose=True,
    ) as client_v2:
        # same session_id as the first time
        print(client_v2.session_id)

        result_v2 = await client_v2.invoke(payload={"prompt": "Hello World!"})
        print(result_v2)


if __name__ == "__main__":
    asyncio.run(main())
