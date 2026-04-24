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
import asyncio
from agentcube import CodeInterpreterClient
from agentcube.integrations.langchain import AgentCubeSandbox

async def test_sandbox_provider():
    """
    Test script to verify AgentCube as a LangChain-compatible sandbox provider.
    This demonstrates the BaseSandbox interface compliance.
    """
    print("🛠️ Initializing AgentCube LangChain Sandbox Provider...")
    
    # 1. Setup the client
    # Ensure ROUTER_URL is set in your environment
    try:
        client = CodeInterpreterClient(name="test-sandbox", verbose=False)
    except Exception as e:
        print(f"❌ Failed to initialize client: {e}")
        return
    
    # 2. Initialize the Sandbox Provider
    # This object implements the LangChain BaseSandbox interface
    sandbox = AgentCubeSandbox(client)
    print(f"✅ Sandbox initialized with ID: {sandbox.id}")

    try:
        # 3. Test isolated execution (BaseSandbox.execute)
        print("\n📝 Testing code execution...")
        cmd = "python3 -c \"print('Hello from AgentCube Sandbox!'); import os; print(f'Working dir: {os.getcwd()}')\""
        response = await sandbox.aexecute(cmd)
        
        print(f"--- Output ---\n{response.output}")
        print(f"Exit Code: {response.exit_code}")
        
        if response.exit_code == 0:
            print("✅ Execution successful.")

        # 4. Test file management (BaseSandbox.upload_files)
        print("\n📂 Testing file upload...")
        # Note: Using text content as SDK only supports str for now (per maintainer request)
        files_to_upload = [
            ("greeting.txt", b"Hello LangChain!"),
            ("config.json", b'{"status": "isolated"}')
        ]
        upload_results = await sandbox.aupload_files(files_to_upload)
        for res in upload_results:
            if not res.error:
                print(f"✅ Uploaded: {res.path}")
            else:
                print(f"❌ Upload failed for {res.path}: {res.error}")

        # 5. Verify files exist and download them (BaseSandbox.download_files)
        print("\n🔍 Verifying and downloading files...")
        # Check files via execution
        ls_res = await sandbox.aexecute("ls -lh greeting.txt config.json")
        print(ls_res.output)

        # Download back
        download_results = await sandbox.adownload_files(["greeting.txt"])
        for res in download_results:
            if not res.error:
                print(f"✅ Downloaded {res.path}: '{res.content.decode()}'")
            else:
                print(f"❌ Download failed for {res.path}: {res.error}")

    finally:
        # 6. Cleanup the session
        print("\n🧹 Cleaning up session...")
        client.stop()
        print("✨ Done.")

if __name__ == "__main__":
    # Check for ROUTER_URL before running
    if not os.getenv("ROUTER_URL"):
        print("⚠️ Warning: ROUTER_URL environment variable is not set.")
        print("Please set it before running (e.g., export ROUTER_URL=http://localhost:8080)")
    
    asyncio.run(test_sandbox_provider())
