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

"""Code-interpreter workflow example for AgentCube's E2B-compatible API.

Models a typical AI agent flow that uses one sandbox across multiple
``run_code`` calls, exercises filesystem read/write, and inspects execution
errors without letting them crash the host script.

Three demos are run sequentially, each on its own sandbox:

  1. ``analyze_data_workflow`` - generate sample data, compute statistics,
     and persist a report through the sandbox filesystem.
  2. ``multi_turn_workflow``   - show that kernel state (defined variables)
     persists across consecutive ``run_code`` calls within one sandbox.
  3. ``error_handling_demo``   - run code that raises and inspect the
     ``Execution.error`` instead of relying on Python exceptions.

Environment variables:

    E2B_API_KEY      API key for authentication (required).
    E2B_BASE_URL     AgentCube Router URL (required).
    E2B_DOMAIN       Host[:port] used by the SDK (auto-derived if unset).
    E2B_TEMPLATE_ID  Template to instantiate (default: default/code-interpreter).
"""

import os
import sys

try:
    from e2b_code_interpreter import Sandbox
except ImportError:
    print("Error: e2b-code-interpreter is not installed.")
    print("Install with: pip install e2b e2b-code-interpreter")
    sys.exit(1)


def configure_environment() -> dict:
    """Resolve env vars and configure SDK-internal variables."""
    api_key = os.environ.get("E2B_API_KEY")
    if not api_key:
        print("Error: E2B_API_KEY environment variable is not set.")
        sys.exit(1)

    base_url = os.environ.get("E2B_BASE_URL")
    if not base_url:
        print("Error: E2B_BASE_URL environment variable is not set.")
        sys.exit(1)

    template_id = os.environ.get("E2B_TEMPLATE_ID", "default/code-interpreter")

    if "E2B_DOMAIN" not in os.environ:
        os.environ["E2B_DOMAIN"] = (
            base_url.replace("https://", "").replace("http://", "")
        )
    if base_url.startswith("https"):
        os.environ.setdefault("E2B_HTTPS", "true")

    return {
        "api_key": api_key,
        "base_url": base_url,
        "template_id": template_id,
    }


def _print_execution(label: str, execution) -> None:
    """Pretty-print an Execution returned by ``run_code``."""
    print(f"  -- {label} --")
    if execution.logs.stdout:
        for line in execution.logs.stdout:
            print(f"    stdout: {line.rstrip()}")
    if execution.logs.stderr:
        for line in execution.logs.stderr:
            print(f"    stderr: {line.rstrip()}")
    if execution.error is not None:
        print(f"    error : {execution.error.name}: {execution.error.value}")
    if execution.results:
        print(f"    results: {len(execution.results)} object(s)")


def analyze_data_workflow(cfg: dict) -> None:
    """A small, realistic data-analysis flow inside one sandbox."""
    print("=== Demo 1: data-analysis workflow ===")
    with Sandbox.create(
        api_key=cfg["api_key"],
        template_id=cfg["template_id"],
        timeout=600,
    ) as sandbox:
        print(f"  sandbox_id : {sandbox.sandbox_id}")

        gen = sandbox.run_code(
            "import random, statistics\n"
            "values = [random.gauss(0, 1) for _ in range(1000)]\n"
            "print(f'count={len(values)}')\n"
        )
        _print_execution("generate data", gen)

        stats = sandbox.run_code(
            "summary = {\n"
            "    'mean'  : statistics.mean(values),\n"
            "    'stdev' : statistics.stdev(values),\n"
            "    'min'   : min(values),\n"
            "    'max'   : max(values),\n"
            "}\n"
            "print(summary)\n"
            "summary\n"
        )
        _print_execution("compute summary", stats)

        sandbox.files.write("/tmp/report.txt", "AgentCube e2b workflow report\n")
        with_existing = sandbox.run_code(
            "with open('/tmp/report.txt', 'a') as f:\n"
            "    for k, v in summary.items():\n"
            "        f.write(f'{k}: {v:.4f}\\n')\n"
            "print('appended')\n"
        )
        _print_execution("append to report", with_existing)

        report = sandbox.files.read("/tmp/report.txt")
        print("  -- report contents --")
        for line in report.splitlines():
            print(f"    {line}")


def multi_turn_workflow(cfg: dict) -> None:
    """Two run_code calls share kernel state inside one sandbox."""
    print("\n=== Demo 2: multi-turn kernel state ===")
    with Sandbox.create(
        api_key=cfg["api_key"],
        template_id=cfg["template_id"],
        timeout=300,
    ) as sandbox:
        print(f"  sandbox_id : {sandbox.sandbox_id}")

        define = sandbox.run_code("agent_state = {'turn': 1, 'history': []}")
        _print_execution("define agent_state", define)

        update = sandbox.run_code(
            "agent_state['turn'] += 1\n"
            "agent_state['history'].append('user asked X')\n"
            "agent_state\n"
        )
        _print_execution("update agent_state (state preserved across turns)", update)


def error_handling_demo(cfg: dict) -> None:
    """run_code captures the runtime error instead of raising in Python."""
    print("\n=== Demo 3: error handling ===")
    with Sandbox.create(
        api_key=cfg["api_key"],
        template_id=cfg["template_id"],
        timeout=300,
    ) as sandbox:
        print(f"  sandbox_id : {sandbox.sandbox_id}")

        bad = sandbox.run_code("raise ValueError('boom')")
        _print_execution("raise ValueError", bad)

        recovered = sandbox.run_code("print('still alive')")
        _print_execution("after the error", recovered)


def main() -> None:
    cfg = configure_environment()
    print(f"E2B_BASE_URL = {cfg['base_url']}")
    print(f"E2B_TEMPLATE_ID = {cfg['template_id']}\n")

    analyze_data_workflow(cfg)
    multi_turn_workflow(cfg)
    error_handling_demo(cfg)

    print("\nDone.")


if __name__ == "__main__":
    main()
