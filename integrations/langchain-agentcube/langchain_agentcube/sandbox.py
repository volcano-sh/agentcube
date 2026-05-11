# Copyright The Volcano Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.

"""AgentCube Code Interpreter as a Deep Agents ``BaseSandbox`` backend."""

from __future__ import annotations

import os
import tempfile
from typing import TYPE_CHECKING

from deepagents.backends.protocol import (
    ExecuteResponse,
    FileDownloadResponse,
    FileUploadResponse,
)
from deepagents.backends.sandbox import BaseSandbox

if TYPE_CHECKING:
    from agentcube import CodeInterpreterClient


def _normalize_remote_path(path: str) -> str:
    """Map paths from Deep Agents (often absolute) to session workspace-relative paths."""
    return path.replace("\\", "/").strip().lstrip("/")


class AgentcubeSandbox(BaseSandbox):
    """Wraps an existing :class:`~agentcube.CodeInterpreterClient` session for ``create_deep_agent(..., backend=...)``."""

    def __init__(
        self,
        *,
        client: CodeInterpreterClient,
        default_timeout: int | None = 30 * 60,
    ) -> None:
        self._client = client
        self._default_timeout = default_timeout

    @property
    def id(self) -> str:
        sid = self._client.session_id
        return sid if sid else "agentcube-unknown"

    def execute(
        self,
        command: str,
        *,
        timeout: int | None = None,
    ) -> ExecuteResponse:
        eff = timeout if timeout is not None else self._default_timeout
        to = float(eff) if eff is not None else None
        r = self._client.execute_command_result(command, timeout=to)
        out = r.get("stdout") or ""
        stderr = (r.get("stderr") or "").strip()
        if stderr:
            out += f"\n<stderr>{stderr}</stderr>"
        return ExecuteResponse(
            output=out,
            exit_code=int(r.get("exit_code", -1)),
            truncated=False,
        )

    def upload_files(self, files: list[tuple[str, bytes]]) -> list[FileUploadResponse]:
        responses: list[FileUploadResponse] = []
        for path, content in files:
            rel = _normalize_remote_path(path)
            if not rel:
                responses.append(FileUploadResponse(path=path, error="invalid_path"))
                continue
            tmp_path: str | None = None
            try:
                fd, tmp_path = tempfile.mkstemp(prefix="agentcube-upload-", suffix=".bin")
                with os.fdopen(fd, "wb") as f:
                    f.write(content)
                self._client.upload_file(tmp_path, rel)
                responses.append(FileUploadResponse(path=path, error=None))
            except Exception as e:
                responses.append(FileUploadResponse(path=path, error=str(e)))
            finally:
                if tmp_path:
                    try:
                        os.unlink(tmp_path)
                    except OSError:
                        pass
        return responses

    def download_files(self, paths: list[str]) -> list[FileDownloadResponse]:
        responses: list[FileDownloadResponse] = []
        for path in paths:
            rel = _normalize_remote_path(path)
            if not rel:
                responses.append(
                    FileDownloadResponse(path=path, content=None, error="invalid_path")
                )
                continue
            fd, tmp_path = tempfile.mkstemp(prefix="agentcube-dl-", suffix=".bin")
            os.close(fd)
            try:
                try:
                    self._client.download_file(rel, tmp_path)
                except Exception as e:  # noqa: BLE001
                    responses.append(
                        FileDownloadResponse(path=path, content=None, error=str(e))
                    )
                    continue
                with open(tmp_path, "rb") as f:
                    data = f.read()
                responses.append(FileDownloadResponse(path=path, content=data, error=None))
            finally:
                try:
                    os.unlink(tmp_path)
                except OSError:
                    pass
        return responses
