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

"""Sandbox lifecycle example for AgentCube's E2B-compatible API.

This script walks through the full sandbox lifecycle exposed by the E2B
Platform API:

  1. Create a sandbox.
  2. Inspect it via ``get_info``.
  3. Use the context-manager pattern for guaranteed cleanup.
  4. Extend the timeout and refresh the TTL.
  5. List running sandboxes.
  6. Close the sandbox explicitly with error handling.

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
    from e2b_code_interpreter.exceptions import SandboxException
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

    # The e2b SDK reads E2B_DOMAIN to compose its own URLs; derive it from
    # base_url when the user hasn't pinned it explicitly.
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


def create_and_inspect(cfg: dict) -> None:
    """Create a sandbox and print its initial state."""
    print("=== Create and inspect ===")
    sandbox = Sandbox.create(
        api_key=cfg["api_key"],
        template_id=cfg["template_id"],
        timeout=300,
    )
    try:
        info = sandbox.get_info()
        print(f"  sandbox_id  : {sandbox.sandbox_id}")
        print(f"  template_id : {info.template_id}")
        print(f"  state       : {info.state}")
        print(f"  started_at  : {info.started_at}")
        print(f"  end_at      : {info.end_at}")
    finally:
        sandbox.close()
        print("  closed.")


def lifecycle_with_context_manager(cfg: dict) -> None:
    """Use the context manager to ensure the sandbox closes on exit."""
    print("\n=== Context manager ===")
    with Sandbox.create(
        api_key=cfg["api_key"],
        template_id=cfg["template_id"],
        timeout=300,
    ) as sandbox:
        print(f"  sandbox_id : {sandbox.sandbox_id}")
        print("  -- doing work inside the with-block --")
    print("  closed automatically.")


def extend_and_refresh(cfg: dict) -> None:
    """Extend the timeout and refresh the TTL."""
    print("\n=== Extend timeout and refresh TTL ===")
    sandbox = Sandbox.create(
        api_key=cfg["api_key"],
        template_id=cfg["template_id"],
        timeout=300,
    )
    try:
        before = sandbox.get_info().end_at
        print(f"  end_at before set_timeout : {before}")

        sandbox.set_timeout(1200)  # extend to 20 minutes from now
        after_set = sandbox.get_info().end_at
        print(f"  end_at after set_timeout  : {after_set}")

        sandbox.refresh(timeout=300)  # add 5 more minutes from now
        after_refresh = sandbox.get_info().end_at
        print(f"  end_at after refresh      : {after_refresh}")
    finally:
        sandbox.close()
        print("  closed.")


def list_running_sandboxes(cfg: dict) -> None:
    """List all sandboxes owned by this API key."""
    print("\n=== List running sandboxes ===")
    sandboxes = Sandbox.list(api_key=cfg["api_key"])
    print(f"  total: {len(sandboxes)}")
    for sb in sandboxes:
        print(
            f"  - {sb.sandbox_id}  template={sb.template_id}  "
            f"state={sb.state}  ends_at={sb.end_at}"
        )


def cleanup_with_error_handling(cfg: dict) -> None:
    """Show explicit close + SandboxException handling."""
    print("\n=== Explicit close with error handling ===")
    sandbox = None
    try:
        sandbox = Sandbox.create(
            api_key=cfg["api_key"],
            template_id=cfg["template_id"],
            timeout=300,
        )
        print(f"  sandbox_id : {sandbox.sandbox_id}")
        # ... real work would happen here ...
    except SandboxException as exc:
        print(f"  sandbox error: {exc}")
    finally:
        if sandbox is not None:
            sandbox.close()
            print("  closed.")


def main() -> None:
    cfg = configure_environment()
    print(f"E2B_BASE_URL = {cfg['base_url']}")
    print(f"E2B_TEMPLATE_ID = {cfg['template_id']}\n")

    create_and_inspect(cfg)
    lifecycle_with_context_manager(cfg)
    extend_and_refresh(cfg)
    list_running_sandboxes(cfg)
    cleanup_with_error_handling(cfg)

    print("\nDone.")


if __name__ == "__main__":
    main()
