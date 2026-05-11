# LangChain + AgentCube sandbox

Wire AgentCube **Code Interpreter** to [LangChain Deep Agents](https://docs.langchain.com/oss/python/deepagents/sandboxes) as the `backend`.

**Prerequisites**: AgentCube cluster with a `CodeInterpreter` CR deployed; see [getting-started](../../docs/getting-started.md). Local **Python >= 3.11**.

---

## 1. Install

From the repository root:

```bash
pip install -e ./sdk-python
pip install -e ./integrations/langchain-agentcube
pip install langchain-openai       # DeepSeek and OpenAI-compatible APIs
# pip install langchain-anthropic  # only if you use Anthropic
```

---

## 2. Cluster environment variables

After `kubectl port-forward` to `workloadmanager` and `agentcube-router`:

```bash
export WORKLOAD_MANAGER_URL="http://localhost:8080"
export ROUTER_URL="http://localhost:8081"
export AGENTCUBE_NAMESPACE="default"    # same namespace as the CodeInterpreter CR
# export API_TOKEN="..."               # if your cluster requires it
# export CODE_INTERPRETER_NAME=my-ci   # optional; default my-interpreter
```

---

## 3. LLM API keys

**DeepSeek** (OpenAI-compatible; `pip install langchain-openai`):

```bash
export DEEPSEEK_API_KEY="sk-..."
# Optional: DEEPSEEK_API_BASE (default https://api.deepseek.com/v1), DEEPSEEK_MODEL (default deepseek-chat)
```

**Anthropic Claude** (`pip install langchain-anthropic`):

```bash
export ANTHROPIC_API_KEY="sk-ant-..."
# Optional: ANTHROPIC_MODEL (default claude-haiku-4-5-20251001)
```

**OpenAI GPT** (same `langchain-openai` package):

```bash
export OPENAI_API_KEY="sk-..."
# Optional: OPENAI_MODEL (default gpt-4o-mini); set OPENAI_API_BASE for proxies or compatible gateways
```

If several keys are set, the example script prefers **DeepSeek, then Claude, then GPT**. For DeepSeek you can instead set only `OPENAI_API_KEY` + `OPENAI_API_BASE=https://api.deepseek.com/v1` + `OPENAI_MODEL=deepseek-chat` without `DEEPSEEK_API_KEY`.

---

## 4. Run the example

`agentcube-cli` and the SDK both use the top-level package name `agentcube`. If you installed the CLI in editable mode, `import agentcube` from the repo root may resolve to the CLI. The example inserts this repo's `sdk-python` first on `sys.path` before importing the SDK. **Do not substitute the CLI package for the SDK**: the CLI does not expose `CodeInterpreterClient`.

```bash
python integrations/langchain-agentcube/example/deep_agent_sandbox.py
```

Flags: `--interpreter`, `--namespace`, `--prompt`. A full fix is to rename the CLI top-level package (e.g. `agentcube_cli`) in the future.

---

Everything below uses **agentcube-sdk** plus Router / Workload Manager.
