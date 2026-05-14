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

"""E2E: LangChain ``AgentcubeSandbox`` (Deep Agents BaseSandbox) against live Router/WM.

Same prerequisites as ``test_codeinterpreter.py``: ``ROUTER_URL``, ``WORKLOAD_MANAGER_URL``,
``AGENTCUBE_NAMESPACE``, optional ``API_TOKEN``. Exercises ``execute``, non-zero exit,
and ``upload_files`` / ``download_files`` (mirrors MCP file roundtrip style).
"""

from __future__ import annotations

import os
import unittest

from agentcube import CodeInterpreterClient
from langchain_agentcube import AgentcubeSandbox


class TestLangchainAgentcubeSandboxE2E(unittest.TestCase):
    """E2E for ``AgentcubeSandbox`` backed by a real Code Interpreter session."""

    def setUp(self):
        self.namespace = os.getenv("AGENTCUBE_NAMESPACE", "agentcube")
        self.workload_manager_url = os.getenv("WORKLOAD_MANAGER_URL")
        self.router_url = os.getenv("ROUTER_URL")
        self.api_token = os.getenv("API_TOKEN")

        if not self.workload_manager_url:
            self.fail("WORKLOAD_MANAGER_URL environment variable not set")
        if not self.router_url:
            self.fail("ROUTER_URL environment variable not set")

        print(
            f"[LangChain sandbox E2E] namespace={self.namespace}, "
            f"wm={self.workload_manager_url}, router={self.router_url}"
        )

    def test_sandbox_execute_echo(self):
        with CodeInterpreterClient(
            name="e2e-code-interpreter",
            namespace=self.namespace,
            workload_manager_url=self.workload_manager_url,
            router_url=self.router_url,
            auth_token=self.api_token,
            verbose=True,
        ) as client:
            backend = AgentcubeSandbox(client=client)
            self.assertTrue(backend.id)
            r = backend.execute("echo lc-sandbox-ok")
            self.assertEqual(r.exit_code, 0, r.output)
            self.assertIn("lc-sandbox-ok", r.output)
            print(f"[LangChain sandbox E2E] execute ok: {r.output!r}")

    def test_sandbox_execute_nonzero_exit(self):
        """``BaseSandbox.execute`` must return ``ExecuteResponse``, not raise."""
        with CodeInterpreterClient(
            name="e2e-code-interpreter",
            namespace=self.namespace,
            workload_manager_url=self.workload_manager_url,
            router_url=self.router_url,
            auth_token=self.api_token,
            verbose=True,
        ) as client:
            backend = AgentcubeSandbox(client=client)
            r = backend.execute("sh -c 'exit 7'")
            self.assertEqual(r.exit_code, 7, r.output)
            print(f"[LangChain sandbox E2E] nonzero exit as expected: {r.exit_code}")

    def test_sandbox_upload_download_roundtrip(self):
        marker = f"lc-roundtrip-{os.getpid()}\n".encode("utf-8")
        remote = f"lc_e2e_roundtrip_{os.getpid()}.txt"
        with CodeInterpreterClient(
            name="e2e-code-interpreter",
            namespace=self.namespace,
            workload_manager_url=self.workload_manager_url,
            router_url=self.router_url,
            auth_token=self.api_token,
            verbose=True,
        ) as client:
            backend = AgentcubeSandbox(client=client)
            up = backend.upload_files([(remote, marker)])
            self.assertEqual(len(up), 1)
            self.assertIsNone(up[0].error, up[0])

            dl = backend.download_files([remote])
            self.assertEqual(len(dl), 1)
            self.assertIsNone(dl[0].error, dl[0])
            self.assertEqual(dl[0].content, marker)
            print(f"[LangChain sandbox E2E] upload/download ok remote={remote!r}")

    def test_sandbox_absolute_path_normalized(self):
        """Deep Agents often use absolute paths; ensure strip + upload/download works."""
        marker = b"abs-path-ok\n"
        remote_abs = f"/lc_abs_{os.getpid()}.txt"
        with CodeInterpreterClient(
            name="e2e-code-interpreter",
            namespace=self.namespace,
            workload_manager_url=self.workload_manager_url,
            router_url=self.router_url,
            auth_token=self.api_token,
            verbose=True,
        ) as client:
            backend = AgentcubeSandbox(client=client)
            up = backend.upload_files([(remote_abs, marker)])
            self.assertIsNone(up[0].error, up[0])
            dl = backend.download_files([remote_abs])
            self.assertIsNone(dl[0].error, dl[0])
            self.assertEqual(dl[0].content, marker)
            print("[LangChain sandbox E2E] absolute path normalized ok")


if __name__ == "__main__":
    unittest.main(verbosity=2)
