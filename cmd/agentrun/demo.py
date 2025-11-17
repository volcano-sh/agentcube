#!/usr/bin/env python3
"""
AgentRun CLI MVP Demo

This script demonstrates the complete AgentRun CLI workflow:
1. Pack an agent
2. Build a container image (mock for demo)
3. Publish to AgentCube (mock for demo)
4. Invoke the agent (mock for demo)
"""

import subprocess
import sys
import time
from pathlib import Path


def run_command(cmd, description, check=True):
    """Run a command and display the result."""
    print(f"\n🔧 {description}")
    print(f"   Command: {cmd}")
    print("   " + "="*50)

    try:
        result = subprocess.run(
            cmd,
            shell=True,
            capture_output=True,
            text=True,
            check=check
        )

        if result.stdout:
            print(result.stdout)

        return True
    except subprocess.CalledProcessError as e:
        print(f"❌ Error: {e}")
        if e.stderr:
            print(f"   Error output: {e.stderr}")
        return False


def main():
    """Main demo function."""
    print("🎉 AgentRun CLI MVP Demo")
    print("=" * 60)
    print("This demo showcases the complete AgentRun CLI workflow.")
    print("For the MVP, some operations (build, publish) are mocked.")
    print("=" * 60)

    # Check if we're in the right directory
    if not Path("agentrun").exists():
        print("❌ Error: Please run this demo from the cli-agentrun directory")
        sys.exit(1)

    # Check if virtual environment is activated
    try:
        import agentrun
        print(f"✅ AgentRun CLI found: {agentrun.__version__}")
    except ImportError:
        print("❌ Error: AgentRun CLI not installed. Please run:")
        print("   source venv/bin/activate && pip install -e .")
        sys.exit(1)

    # Step 1: Pack the agent
    print(f"\n{'='*20} Step 1: Pack Agent {'='*20}")
    success = run_command(
        "agentrun pack -f examples/hello-agent --agent-name 'demo-agent' --description 'Demo agent for AgentRun CLI' --verbose",
        "Packing the hello agent"
    )

    if not success:
        print("❌ Pack failed, aborting demo")
        sys.exit(1)

    # Step 2: Show the generated metadata
    print(f"\n{'='*20} Step 2: Generated Metadata {'='*20}")
    metadata_file = Path("examples/hello-agent/agent_metadata.yaml")
    if metadata_file.exists():
        print("📄 Generated agent_metadata.yaml:")
        with open(metadata_file, 'r') as f:
            content = f.read()
            print(content)
    else:
        print("❌ Metadata file not found")

    # Step 3: Show the generated Dockerfile
    print(f"\n{'='*20} Step 3: Generated Dockerfile {'='*20}")
    dockerfile = Path("examples/hello-agent/Dockerfile")
    if dockerfile.exists():
        print("📄 Generated Dockerfile (first 15 lines):")
        with open(dockerfile, 'r') as f:
            lines = f.readlines()[:15]
            for i, line in enumerate(lines, 1):
                print(f"   {i:2d}: {line.rstrip()}")
    else:
        print("❌ Dockerfile not found")

    # Step 4: Mock build (since Docker might not be available)
    print(f"\n{'='*20} Step 4: Build Image {'='*20}")
    print("🔧 Building container image (simulated for demo)")
    print("   In a real environment, this would:")
    print("   - Check Docker availability")
    print("   - Build the image using Docker")
    print("   - Store image information in metadata")

    # Simulate build metadata update
    time.sleep(2)
    print("   ✅ Build completed (simulated)")

    # Step 5: Mock publish
    print(f"\n{'='*20} Step 5: Publish to AgentCube {'='*20}")
    print("🔧 Publishing to AgentCube (simulated for demo)")
    print("   In a real environment, this would:")
    print("   - Push the image to a registry")
    print("   - Register the agent with AgentCube")
    print("   - Return agent ID and endpoint")

    # Simulate publish
    time.sleep(2)
    print("   ✅ Agent published (simulated)")
    print("   🆔 Agent ID: agent-demo-12345")
    print("   🌐 Endpoint: https://api.agentcube.example.com/agents/agent-demo-12345/invoke")

    # Step 6: Mock invoke
    print(f"\n{'='*20} Step 6: Invoke Agent {'='*20}")
    print("🔧 Invoking agent (simulated for demo)")

    # Create mock publish metadata for invoke to work
    metadata_file = Path("examples/hello-agent/agent_metadata.yaml")
    if metadata_file.exists():
        with open(metadata_file, 'a') as f:
            f.write("\nagent_id: agent-demo-12345\n")
            f.write("agent_endpoint: https://api.agentcube.example.com/agents/agent-demo-12345/invoke\n")
            f.write("version: v1.0.0\n")

    success = run_command(
        'agentrun invoke -f examples/hello-agent --payload \'{"prompt": "Hello Agent!", "name": "Demo User"}\' --verbose',
        "Invoking the published agent"
    )

    # Step 7: Check status
    print(f"\n{'='*20} Step 7: Check Agent Status {'='*20}")
    run_command(
        "agentrun status -f examples/hello-agent --verbose",
        "Checking agent status"
    )

    # Summary
    print(f"\n{'='*20} Demo Summary {'='*20}")
    print("✅ AgentRun CLI MVP demo completed successfully!")
    print("\n🎯 What we demonstrated:")
    print("   ✅ Pack: Validated and packaged the agent")
    print("   ✅ Build: Generated Dockerfile and prepared for container build")
    print("   ✅ Publish: Simulated AgentCube registration")
    print("   ✅ Invoke: Simulated agent invocation")
    print("   ✅ Status: Checked agent status")

    print("\n🚀 Next steps:")
    print("   1. Install Docker to enable real container builds")
    print("   2. Set up AgentCube API endpoint for real publishing")
    print("   3. Try with your own agent projects")
    print("   4. Explore additional CLI options with --help")

    print(f"\n📚 Learn more:")
    print("   - agentrun --help: Show all commands")
    print("   - agentrun pack --help: Pack command options")
    print("   - agentrun build --help: Build command options")
    print("   - agentrun publish --help: Publish command options")
    print("   - agentrun invoke --help: Invoke command options")


if __name__ == "__main__":
    main()