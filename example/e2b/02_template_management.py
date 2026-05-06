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

"""Template management example for AgentCube's E2B-compatible API.

Walks through the full template lifecycle:

  1. List existing templates and filter them.
  2. Create a new template.
  3. Poll until the template build reaches ``ready`` (or ``error``).
  4. Get template details.
  5. Update description and aliases.
  6. List builds for the template.
  7. Delete the template.

Environment variables:

    E2B_API_KEY    API key for authentication (required).
    E2B_BASE_URL   AgentCube Router URL (required).
"""

import os
import sys
import time
from typing import Optional

try:
    from e2b import Template
except ImportError:
    print("Error: e2b is not installed.")
    print("Install with: pip install e2b e2b-code-interpreter")
    sys.exit(1)


# Generate a unique-ish name so re-running the script does not collide.
TEMPLATE_NAME = f"example-template-{int(time.time())}"


def configure_environment() -> dict:
    """Resolve env vars used by the example."""
    api_key = os.environ.get("E2B_API_KEY")
    if not api_key:
        print("Error: E2B_API_KEY environment variable is not set.")
        sys.exit(1)

    base_url = os.environ.get("E2B_BASE_URL")
    if not base_url:
        print("Error: E2B_BASE_URL environment variable is not set.")
        sys.exit(1)

    return {"api_key": api_key, "base_url": base_url}


def list_templates(cfg: dict) -> None:
    """List all templates and demonstrate two filters."""
    print("=== List templates ===")
    templates = Template.list(api_key=cfg["api_key"], base_url=cfg["base_url"])
    print(f"  total: {len(templates)}")
    for t in templates:
        aliases = ", ".join(t.aliases) if t.aliases else "-"
        print(
            f"  - {t.template_id}  state={t.state}  public={t.public}  "
            f"aliases=[{aliases}]"
        )

    public_only = [t for t in templates if t.public]
    print(f"\n  public-only count : {len(public_only)}")

    aliased = [t for t in templates if t.aliases and "datascience" in t.aliases]
    print(f"  with 'datascience' alias : {len(aliased)}")


def create_template(cfg: dict) -> Optional[str]:
    """Create a new template; return its template_id on success."""
    print(f"\n=== Create template {TEMPLATE_NAME} ===")
    try:
        template = Template.create(
            api_key=cfg["api_key"],
            base_url=cfg["base_url"],
            name=TEMPLATE_NAME,
            description="Example template created by AgentCube e2b examples.",
            public=True,
            aliases=["example", "demo"],
            memory_mb=4096,
            cpu_count=2,
        )
        print(f"  created    : {template.template_id}")
        print(f"  state      : {template.state}")
        print(f"  created_at : {template.created_at}")
        return template.template_id
    except Exception as exc:  # pragma: no cover
        print(f"  create failed: {exc}")
        return None


def wait_for_template_ready(
    cfg: dict,
    template_id: str,
    timeout: int = 300,
    poll_interval: int = 10,
) -> bool:
    """Poll until the template is in a terminal state or the timeout expires."""
    print(f"\n=== Wait for {template_id} to be ready ===")
    deadline = time.time() + timeout
    last_state = None

    while time.time() < deadline:
        template = Template.get(
            api_key=cfg["api_key"],
            base_url=cfg["base_url"],
            template_id=template_id,
        )
        if template.state != last_state:
            print(f"  state -> {template.state}")
            last_state = template.state

        if template.state == "ready":
            print("  template is ready.")
            return True
        if template.state == "error":
            print("  template build failed.")
            return False

        time.sleep(poll_interval)

    print("  timed out waiting for template.")
    return False


def get_template(cfg: dict, template_id: str) -> None:
    """Print full details for the template."""
    print(f"\n=== Get template {template_id} ===")
    template = Template.get(
        api_key=cfg["api_key"],
        base_url=cfg["base_url"],
        template_id=template_id,
    )
    print(f"  name        : {template.name}")
    print(f"  description : {template.description}")
    print(f"  state       : {template.state}")
    print(f"  public      : {template.public}")
    print(f"  aliases     : {template.aliases}")
    print(f"  memory_mb   : {template.memory_mb}")
    print(f"  cpu_count   : {template.cpu_count}")
    print(f"  created_at  : {template.created_at}")
    print(f"  updated_at  : {template.updated_at}")


def update_template(cfg: dict, template_id: str) -> None:
    """Update description and aliases via the model's ``update`` method."""
    print(f"\n=== Update template {template_id} ===")
    template = Template.get(
        api_key=cfg["api_key"],
        base_url=cfg["base_url"],
        template_id=template_id,
    )
    template.description = "Updated description for the example template."
    template.aliases = ["example", "demo", "updated"]
    updated = template.update()
    print(f"  description : {updated.description}")
    print(f"  aliases     : {updated.aliases}")


def list_builds(cfg: dict, template_id: str) -> None:
    """List build history for a template."""
    print(f"\n=== List builds for {template_id} ===")
    builds = Template.list_builds(
        api_key=cfg["api_key"],
        base_url=cfg["base_url"],
        template_id=template_id,
    )
    print(f"  total: {len(builds)}")
    for b in builds:
        completed = b.completed_at or "-"
        print(
            f"  - {b.build_id}  state={b.state}  "
            f"created={b.created_at}  completed={completed}"
        )


def delete_template(cfg: dict, template_id: str) -> None:
    """Delete the template."""
    print(f"\n=== Delete template {template_id} ===")
    template = Template.get(
        api_key=cfg["api_key"],
        base_url=cfg["base_url"],
        template_id=template_id,
    )
    template.delete()
    print("  deleted.")


def main() -> None:
    cfg = configure_environment()
    print(f"E2B_BASE_URL = {cfg['base_url']}\n")

    list_templates(cfg)

    template_id = create_template(cfg)
    if template_id is None:
        print("\nAborting remaining steps because template creation failed.")
        return

    try:
        ready = wait_for_template_ready(cfg, template_id)
        if ready:
            get_template(cfg, template_id)
            update_template(cfg, template_id)
            list_builds(cfg, template_id)
    finally:
        delete_template(cfg, template_id)

    print("\nDone.")


if __name__ == "__main__":
    main()
