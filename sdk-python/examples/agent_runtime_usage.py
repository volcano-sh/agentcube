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

# second time: it will try to re-use the pod created before
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


