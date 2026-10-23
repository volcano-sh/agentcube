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


agent_name = os.getenv("AGENT_RUNTIME_NAME", "simple-agentruntime")
namespace = os.getenv("SANDBOX_NAMESPACE", "default")

# Create a new AgentRuntime session.
agent_client_v1 = AgentRuntimeClient(
    agent_name=agent_name,
    namespace=namespace,
    verbose=True,
)
session_id = agent_client_v1.session_id
print(agent_client_v1.session_id)

result_v1 = agent_client_v1.invoke(
    payload={"input": "Hello World!"},
    path="echo",
)
print(result_v1)
agent_client_v1.close()  # The remote session remains reusable.

# Reuse the same AgentRuntime session with a new client.
agent_client_v2 = AgentRuntimeClient(
    agent_name=agent_name,
    namespace=namespace,
    session_id=session_id,
    verbose=True,
)
print(agent_client_v2.session_id)

result_v2 = agent_client_v2.invoke(
    payload={"input": "Hello again!"},
    path="echo",
)
print(result_v2)
agent_client_v2.close()
