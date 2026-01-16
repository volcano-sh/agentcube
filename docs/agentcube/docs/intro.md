---
sidebar_position: 1
---

# AgentCube

**AgentCube** is a specialized subproject within the [Volcano](https://volcano.sh/) community, designed to natively support and manage **AI Agent workloads** on Kubernetes.

As Generative AI and Large Language Models (LLMs) evolve, the way we run workloads is changing. Standard batch or inference systems often fall short when dealing with AI Agents, which require:

- **Interactive, long-running sessions**: Agents often engage in multi-turn conversations.
- **State preservation**: Keeping context alive across interactions.
- **Intermittent activity**: Agents may be idle for long periods but need to resume instantly.
- **Low latency**: Users expect sub-second responses.

## Why AgentCube?

AgentCube bridges the gap between traditional workload management and the unique needs of AI Agents by providing:

1. **Extreme Low-Latency Scheduling**: Optimized for fast startup and interactive response.
2. **Stateful Lifecycle Management**: Implements smart sleep/resume mechanisms to save resources when agents are idle.
3. **High-Density Resource Utilization**: Advanced bin-packing that ensures performance isolation while maximizing cluster efficiency.
4. **Command-style API**: Offers a synchronous, imperative API experience for executing agent tasks.

## Core Components

- **Workload Manager**: Orchestrates the lifecycle of agent sandboxes.
- **AgentCube Router**: Handles routing and session management for agent interactions.
- **AgentD**: A runtime component that manages the execution environment within the sandbox.

## Get Started

Ready to dive in? Head over to the [Getting Started Guide](./getting-started.md) to install AgentCube on your cluster and deploy your first agent.
