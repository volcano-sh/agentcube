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

import os
import pytest
from agentcube import CodeInterpreterClient
from agentcube.integrations.langchain import AgentCubeSandbox

# Skip these tests unless the E2E environment variables are set
pytestmark = pytest.mark.skipif(
    not os.getenv("ROUTER_URL") or not os.getenv("WORKLOAD_MANAGER_URL"),
    reason="E2E environment variables (ROUTER_URL, WORKLOAD_MANAGER_URL) not set"
)

@pytest.fixture
async def sandbox():
    """Fixture to manage the lifecycle of an AgentCubeSandbox during E2E tests."""
    client = CodeInterpreterClient(name="e2e-test-sandbox", verbose=False)
    sb = AgentCubeSandbox(client)
    yield sb
    # Cleanup after test
    client.stop()

@pytest.mark.asyncio
async def test_langchain_sandbox_e2e_flow(sandbox):
    """
    E2E test verifying the core BaseSandbox interface against a real backend.
    Matches the flow in examples/test_langchain_sandbox.py
    """

    # 1. Test Command Execution
    cmd = "python3 -c \"print('e2e_success')\""
    response = await sandbox.aexecute(cmd)

    assert response.exit_code == 0
    assert "e2e_success" in response.output

    # 2. Test File Upload
    files_to_upload = [
        ("e2e_test.txt", b"e2e_content"),
        ("data.json", b'{"key": "value"}')
    ]
    upload_results = await sandbox.aupload_files(files_to_upload)

    assert len(upload_results) == 2
    for res in upload_results:
        assert res.error is None
        assert res.path in ["e2e_test.txt", "data.json"]

    # 3. Verify files exist via remote ls
    ls_res = await sandbox.aexecute("ls e2e_test.txt data.json")
    assert ls_res.exit_code == 0
    assert "e2e_test.txt" in ls_res.output
    assert "data.json" in ls_res.output

    # 4. Test File Download
    download_results = await sandbox.adownload_files(["e2e_test.txt"])

    assert len(download_results) == 1
    assert download_results[0].error is None
    assert download_results[0].path == "e2e_test.txt"
    assert download_results[0].content == b"e2e_content"

@pytest.mark.asyncio
async def test_langchain_sandbox_environment_isolation(sandbox):
    """Verify that multiple commands share the same stateful environment."""

    # Set a variable in one command
    await sandbox.aexecute("echo 'persisted_state' > state.txt")

    # Read it in another
    response = await sandbox.aexecute("cat state.txt")

    assert response.exit_code == 0
    assert "persisted_state" in response.output
