#!/usr/bin/env python3
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

"""Manage AgentCube's Go toolchain baseline.

The project keeps the Go baseline in go.mod and mirrors it into Docker builder
image tags. GitHub Actions should read the Go version from go.mod.
"""

from __future__ import annotations

import argparse
import json
import re
import sys
import urllib.request
from pathlib import Path


GO_DIRECTIVE_RE = re.compile(r"^go[ \t]+([0-9]+\.[0-9]+(?:\.[0-9]+)?)[ \t]*$", re.MULTILINE)
TOOLCHAIN_RE = re.compile(r"^toolchain[ \t]+go(\S+)[ \t]*$", re.MULTILINE)
TOOLCHAIN_LINE_RE = re.compile(r"^toolchain[ \t]+\S+[ \t]*(?:\r?\n|$)", re.MULTILINE)
GOLANG_IMAGE_RE = re.compile(
    r"^(FROM(?:\s+--platform=\S+)?\s+golang:)"
    r"([0-9]+\.[0-9]+(?:\.[0-9]+)?)"
    r"([^\s]*)",
    re.MULTILINE,
)
SETUP_GO_RE = re.compile(r"^[ \t]*(?:-[ \t]*)?uses:[ \t]*actions/setup-go@", re.MULTILINE)
GO_VERSION_RE = re.compile(r"^[ \t]*go-version[ \t]*:", re.MULTILINE)
GO_VERSION_FILE_RE = re.compile(r"^[ \t]*go-version-file[ \t]*:[ \t]*go\.mod\b", re.MULTILINE)
VERSION_RE = re.compile(r"^[0-9]+\.[0-9]+(?:\.[0-9]+)?$")


def read_text(path: Path) -> str:
    return path.read_text(encoding="utf-8")


def write_if_changed(path: Path, content: str, changed: list[Path]) -> None:
    old = read_text(path)
    if old == content:
        return
    path.write_text(content, encoding="utf-8")
    changed.append(path)


def go_mod_path(repo: Path) -> Path:
    return repo / "go.mod"


def read_go_version(repo: Path) -> str:
    content = read_text(go_mod_path(repo))
    match = GO_DIRECTIVE_RE.search(content)
    if not match:
        raise ValueError("go.mod does not contain a parseable go directive")
    return match.group(1)


def read_toolchain_version(repo: Path) -> str | None:
    content = read_text(go_mod_path(repo))
    match = TOOLCHAIN_RE.search(content)
    return match.group(1) if match else None


def latest_stable_go() -> str:
    with urllib.request.urlopen("https://go.dev/dl/?mode=json", timeout=30) as response:
        releases = json.load(response)

    for release in releases:
        if release.get("stable"):
            version = str(release["version"])
            return version[2:] if version.startswith("go") else version

    raise ValueError("go.dev did not return a stable Go release")


def validate_version(version: str) -> None:
    if not VERSION_RE.match(version):
        raise ValueError(f"invalid Go version: {version!r}")


def update_go_mod(repo: Path, version: str, changed: list[Path]) -> None:
    path = go_mod_path(repo)
    content = read_text(path)

    if not GO_DIRECTIVE_RE.search(content):
        raise ValueError("go.mod does not contain a parseable go directive")

    content = GO_DIRECTIVE_RE.sub(f"go {version}", content, count=1)
    content = TOOLCHAIN_LINE_RE.sub("", content)
    content = re.sub(r"\n{3,}", "\n\n", content)
    write_if_changed(path, content, changed)


def update_dockerfiles(repo: Path, version: str, changed: list[Path]) -> None:
    docker_dir = repo / "docker"
    if not docker_dir.is_dir():
        raise ValueError(f"missing docker directory: {docker_dir}")

    found = False
    for path in sorted(docker_dir.glob("Dockerfile*")):
        content = read_text(path)

        def replace(match: re.Match[str]) -> str:
            nonlocal found
            found = True
            prefix, _old_version, suffix = match.groups()
            return f"{prefix}{version}{suffix}"

        write_if_changed(path, GOLANG_IMAGE_RE.sub(replace, content), changed)

    if not found:
        raise ValueError("no golang builder images found under docker/Dockerfile*")


def update_repo(repo: Path, version: str) -> list[Path]:
    validate_version(version)
    changed: list[Path] = []
    update_go_mod(repo, version, changed)
    update_dockerfiles(repo, version, changed)
    return changed


def verify_dockerfiles(repo: Path, go_version: str) -> list[str]:
    errors: list[str] = []
    docker_dir = repo / "docker"
    found = []

    for path in sorted(docker_dir.glob("Dockerfile*")):
        content = read_text(path)
        for match in GOLANG_IMAGE_RE.finditer(content):
            version, suffix = match.group(2), match.group(3)
            rel = path.relative_to(repo)
            found.append(rel)
            print(f"Docker builder: {rel}: golang:{version}{suffix}")
            if version != go_version:
                errors.append(f"{rel} uses golang:{version}{suffix}, expected Go {go_version}")

    if not found:
        errors.append("no golang builder images found under docker/Dockerfile*")

    return errors


def verify_workflows(repo: Path) -> list[str]:
    errors: list[str] = []
    workflows = repo / ".github" / "workflows"

    workflow_files = list(workflows.glob("*.yml")) + list(workflows.glob("*.yaml"))
    for path in sorted(workflow_files):
        content = read_text(path)
        if not SETUP_GO_RE.search(content):
            continue

        rel = path.relative_to(repo)
        has_file = bool(GO_VERSION_FILE_RE.search(content))
        has_inline = bool(GO_VERSION_RE.search(content))
        print(f"setup-go workflow: {rel}: go-version-file={has_file} inline-go-version={has_inline}")

        if not has_file:
            errors.append(f"{rel} uses actions/setup-go without go-version-file: go.mod")
        if has_inline:
            errors.append(f"{rel} has inline go-version; prefer go-version-file: go.mod")

    return errors


def verify_repo(repo: Path, check_latest: bool, require_latest: bool) -> int:
    errors: list[str] = []
    go_version = read_go_version(repo)
    toolchain_version = read_toolchain_version(repo)

    print(f"go.mod go directive: {go_version}")
    if toolchain_version:
        print(f"go.mod toolchain directive: {toolchain_version}")
        if toolchain_version != go_version:
            errors.append(f"go.mod toolchain {toolchain_version} differs from go directive {go_version}")
    else:
        print("go.mod toolchain directive: <none>")

    errors.extend(verify_dockerfiles(repo, go_version))
    errors.extend(verify_workflows(repo))

    if check_latest or require_latest:
        latest = latest_stable_go()
        print(f"latest stable Go release: {latest}")
        if latest != go_version:
            message = f"project Go {go_version} differs from latest stable Go {latest}"
            if require_latest:
                errors.append(message)
            else:
                print(f"NOTICE: {message}")

    if errors:
        for error in errors:
            print(f"ERROR: {error}", file=sys.stderr)
        return 1

    print("Go toolchain alignment: OK")
    return 0


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repo-root", default=".", help="Repository root. Defaults to current directory.")

    subparsers = parser.add_subparsers(dest="command", required=True)
    subparsers.add_parser("current", help="Print the current go.mod Go directive.")
    subparsers.add_parser("latest", help="Print the latest stable Go release from go.dev.")

    update = subparsers.add_parser("update", help="Update go.mod and Docker builder image tags.")
    update.add_argument("--version", help="Target Go version without the leading 'go'.")
    update.add_argument("--latest", action="store_true", help="Use the latest stable Go release from go.dev.")

    verify = subparsers.add_parser("verify", help="Verify Go toolchain baseline alignment.")
    verify.add_argument("--check-latest", action="store_true", help="Report the latest stable Go version.")
    verify.add_argument("--require-latest", action="store_true", help="Fail if go.mod is behind latest stable Go.")

    return parser.parse_args()


def main() -> int:
    args = parse_args()
    repo = Path(args.repo_root).resolve()

    try:
        if args.command == "current":
            print(read_go_version(repo))
            return 0

        if args.command == "latest":
            print(latest_stable_go())
            return 0

        if args.command == "update":
            if args.latest == bool(args.version):
                raise ValueError("choose exactly one of --latest or --version")

            version = latest_stable_go() if args.latest else args.version
            assert version is not None
            changed = update_repo(repo, version)
            print(f"target Go version: {version}")
            if changed:
                print("updated files:")
                for path in changed:
                    print(f"- {path.relative_to(repo)}")
            else:
                print("no files changed")
            return 0

        if args.command == "verify":
            return verify_repo(repo, args.check_latest, args.require_latest)

    except Exception as exc:  # noqa: BLE001 - CLI should print compact errors.
        print(f"ERROR: {exc}", file=sys.stderr)
        return 1

    raise AssertionError(f"unhandled command: {args.command}")


if __name__ == "__main__":
    raise SystemExit(main())
