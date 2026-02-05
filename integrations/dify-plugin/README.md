# Agentcube Dify Plugin

AgentCube is designed to extend Volcano's capabilities to natively support and manage AI Agent workloads. This plugin integrates AgentCube with Dify, providing a powerful and isolated code execution sandbox.

## Tools

### Agentcube Code Interpreter

The **Agentcube Code Interpreter** tool offers an isolated code execution sandbox based on Volcano AgentCube. It allows you to perform various code interpreter actions within a secure environment.

#### Capabilities

*   **Execute Python Code**: Run Python scripts securely.
*   **Execute Terminal Commands**: Perform shell commands in the sandbox.

#### Features

*   **Session Management**: Automatically create new sessions or reuse existing ones via `session_id`.
*   **Isolation**: Secure execution environment provided by Volcano AgentCube.

## Configuration Parameters

| Parameter | Type | Required | Description |
| :--- | :--- | :--- | :--- |
| `router_url` | String | **Yes** | The AgentCube router URL for data plane communication. |
| `workload_manager_url` | String | **Yes** | The AgentCube workload manager URL for control plane operations. |
| `language` | Select | No | Programming language of the code (required if `code` is provided). Defaults to `python`. AgentCube includes a built-in Python interpreter. To use custom Python environments or other languages, you must upload and configure a custom code interpreter in AgentCube. |
| `code` | String | No* | The source code to be executed. |
| `command` | String | No* | Terminal command to be executed. If provided, it will be executed before the code. |
| `session_id` | String | No | Unique identifier of the code interpreter session to use. If not provided, a new session is created. |
| `session_reuse` | Boolean | No | Whether to reuse the session for next invocation. If true, the session will not be stopped after execution. |
| `code_interpreter_id` | String | No | ID of the AgentCube code interpreter to use. If not set, it defaults to the built-in Python code interpreter. |

> **Note**: At least one of `code` or `command` must be provided.

## Usage

1.  **Install**: Install the Agentcube plugin in Dify.
2.  **Add Tool**: In your Dify Agent or Workflow, add the **Agentcube Code Interpreter** tool.
3.  **Configure**: Set the `router_url` and `workload_manager_url`. These are typically provided by your system administrator or AgentCube deployment.
4.  **Execute**:
    *   To run code: Select `python` as the language and input your code in the `code` field.
    *   To run commands: Input your shell command in the `command` field.
