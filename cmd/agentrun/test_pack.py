#!/usr/bin/env python3
"""
Simple test script for the pack functionality.
"""

import sys
from pathlib import Path

# Add the agentrun module to the path
sys.path.insert(0, str(Path(__file__).parent))

from agentrun.runtime.pack_runtime import PackRuntime
from agentrun.services.metadata_service import MetadataService


def test_pack():
    """Test the pack functionality with the hello-agent example."""
    print("🧪 Testing AgentRun pack functionality...")

    # Path to the example agent
    workspace_path = Path(__file__).parent / "examples" / "hello-agent"

    if not workspace_path.exists():
        print(f"❌ Example workspace not found: {workspace_path}")
        return False

    print(f"📁 Using workspace: {workspace_path}")

    try:
        # Initialize pack runtime
        pack_runtime = PackRuntime(verbose=True)

        # Test pack command
        result = pack_runtime.pack(
            workspace_path,
            agent_name="hello-agent-test",
            description="A test agent for packing functionality",
            language="python",
            entrypoint="python main.py",
            port=8080,
            build_mode="local"
        )

        print("✅ Pack completed successfully!")
        print(f"   Agent Name: {result['agent_name']}")
        print(f"   Workspace: {result['workspace_path']}")
        print(f"   Language: {result['language']}")
        print(f"   Build Mode: {result['build_mode']}")

        # Verify metadata was created
        metadata_service = MetadataService(verbose=True)
        metadata = metadata_service.load_metadata(workspace_path)

        print("✅ Metadata validation passed!")
        print(f"   Metadata Agent Name: {metadata.agent_name}")
        print(f"   Metadata Language: {metadata.language}")
        print(f"   Metadata Entrypoint: {metadata.entrypoint}")

        # Check if Dockerfile was created
        dockerfile_path = workspace_path / "Dockerfile"
        if dockerfile_path.exists():
            print("✅ Dockerfile generated successfully!")

            # Show first few lines of Dockerfile
            with open(dockerfile_path, 'r') as f:
                lines = f.readlines()[:5]
                print("   Dockerfile preview:")
                for i, line in enumerate(lines, 1):
                    print(f"     {i}: {line.rstrip()}")
        else:
            print("⚠️  Dockerfile was not created")

        return True

    except Exception as e:
        print(f"❌ Pack test failed: {str(e)}")
        import traceback
        traceback.print_exc()
        return False


if __name__ == "__main__":
    success = test_pack()
    if success:
        print("\n🎉 All pack tests passed!")
        sys.exit(0)
    else:
        print("\n💥 Pack tests failed!")
        sys.exit(1)