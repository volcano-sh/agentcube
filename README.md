# AgentCube

> [!NOTE]
> AgentCube is currently in the Proposal and Early Design Phase. Project's initial proposal can be found at <https://github.com/volcano-sh/volcano/issues/4686>. Specific feature sets and implementation details are subject to change based on community consensus and development progress.

## Overview

AgentCube is a proposed subproject in the Volcano community. It is designed to extend Volcano's capabilities to natively support and manage AI Agent workloads, which are rapidly emerging in the fields of Generative AI and Large Language Model (LLM) applications.

Existing workload management patterns and current batch/inference systems are insufficient for the unique requirements posed by these continuously interactive, state-preserving, and intermittently active long-session workloads.

AgentCube aims to provide a specialized control plane and data plane components for AI Agents, focusing on:

1. **Extreme Low-Latency Scheduling**: Optimized for fast startup and interactive response.
2. **Stateful Lifecycle Management**: Implementing smart sleep/resume mechanisms for resource efficiency.
3. **High-Density Resource Utilization**: Advanced bin-packing under the constraint of guaranteed performance isolation.
4. **Command-style API**: Providing a synchronous, imperative API experience for Agent execution.

## Why AgentCube

Volcano, designed for high-performance batch scheduling in the cloud-native ecosystem, is ideal for managing complex, compute-intensive workloads. While AI Agent applications represent the next generation of AI workloads, characterized by unique demands:

* **Intermittent Activity**: Requiring fast resource release when idle and rapid recovery upon interaction.
* **High Latency Sensitivity**: Demanding sub-second responses for optimal user experience.
* **State Persistence**: Requiring context and state to be preserved across long, multi-turn sessions.

Introducing AgentCube allows Volcano to complete its support for the full AI lifecycle, enabling users to efficiently orchestrate and manage AI Agent workloads on Kubernetes. This significantly improves management efficiency and optimizes cluster resource utilization by handling these "bursty" Agent applications.
