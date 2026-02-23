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

from agentcube import AgentRuntimeClient

# first time: it will create a new pod
agent_client_v1 = AgentRuntimeClient(
    agent_name="my-agent",
    router_url="http://localhost:18081",
    namespace="default",
    verbose=True,
)
print(agent_client_v1.session_id)

result_v1 = agent_client_v1.invoke(
    payload={"prompt": "Hello World!"},
)
print(result_v1)

# second time: it will try to reuse the pod created before
agent_client_v2 = AgentRuntimeClient(
    agent_name="my-agent",
    router_url="http://localhost:18081",
    namespace="default",
    session_id=agent_client_v1.session_id,
    verbose=True,
)
# same with the first time
print(agent_client_v2.session_id)

result_v2 = agent_client_v2.invoke(
    payload={"prompt": "Hello World!"},
)
print(result_v2)


