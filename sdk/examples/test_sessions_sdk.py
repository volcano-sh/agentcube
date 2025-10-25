"""
Test/Example usage of the Sandbox SDK

This file demonstrates how to use the SandboxClient to interact
with the Sandbox API's sandbox management endpoints.
"""

import os
import time
from datetime import datetime
from sandbox_sessions_sdk import (
    SandboxClient,
    SandboxAPIError,
    SandboxConnectionError,
    SessionNotFoundError,
    UnauthorizedError,
    RateLimitError,
)


def example_basic_usage():
    """Basic example of creating and managing a sandbox"""
    print("=" * 60)
    print("Example 1: Basic Sandbox Management")
    print("=" * 60)
    
    # Initialize client
    client = SandboxClient(
        api_url='http://localhost:8080/v1',
        bearer_token='your-bearer-token-here'
    )
    
    try:
        # Create a sandbox
        print("\n1. Creating a new sandbox...")
        sandbox = client.create_sandbox(
            ttl=3600,  # 1 hour
            image='python:3.11',
            metadata={'example': 'basic_usage'}
        )
        print(f"   ✓ Sandbox created: {sandbox.sandbox_id}")
        print(f"   - Status: {sandbox.status.value}")
        print(f"   - Expires at: {sandbox.expires_at}")
        
        # Get sandbox details
        print("\n2. Fetching sandbox details...")
        fetched = client.get_sandbox(sandbox.sandbox_id)
        print(f"   ✓ Sandbox found")
        print(f"   - Created: {fetched.created_at}")
        print(f"   - Metadata: {fetched.metadata}")
        
        # Delete sandbox
        print("\n3. Deleting sandbox...")
        client.delete_sandbox(sandbox.sandbox_id)
        print(f"   ✓ Sandbox deleted")
        
    except SandboxAPIError as e:
        print(f"   ✗ Error: {e.message}")
    finally:
        client.close()


def example_context_manager():
    """Example using context manager for automatic cleanup"""
    print("\n" + "=" * 60)
    print("Example 2: Using Context Manager")
    print("=" * 60)
    
    with SandboxClient(
        api_url='http://localhost:8080/v1',
        bearer_token='your-bearer-token-here'
    ) as client:
        
        print("\n1. Creating sandbox with custom TTL...")
        sandbox = client.create_sandbox(
            ttl=7200,  # 2 hours
            metadata={'example': 'context_manager', 'priority': 'high'}
        )
        print(f"   ✓ Sandbox ID: {sandbox.sandbox_id}")
        
        # Client automatically closes when exiting context


def example_list_sandboxes():
    """Example of listing and paginating through sandboxes"""
    print("\n" + "=" * 60)
    print("Example 3: Listing Sandboxes")
    print("=" * 60)
    
    with SandboxClient(
        api_url='http://localhost:8080/v1',
        bearer_token='your-bearer-token-here'
    ) as client:
        
        # Create a few test sandboxes
        print("\n1. Creating multiple test sandboxes...")
        sandbox_ids = []
        for i in range(3):
            sandbox = client.create_sandbox(
                ttl=1800,
                metadata={'example': 'listing', 'index': i}
            )
            sandbox_ids.append(sandbox.sandbox_id)
            print(f"   ✓ Created sandbox {i+1}: {sandbox.sandbox_id[:8]}...")
        
        # List all sandboxes
        print("\n2. Listing all sandboxes...")
        result = client.list_sandboxes(limit=10, offset=0)
        print(f"   ✓ Total sandboxes: {result['total']}")
        print(f"   ✓ Returned: {len(result['sandboxes'])} sandboxes")
        
        for sandbox in result['sandboxes']:
            print(f"   - {sandbox.sandbox_id}")
            print(f"     Status: {sandbox.status.value}")
            print(f"     Metadata: {sandbox.metadata}")
        
        # Pagination example
        if result['total'] > 5:
            print("\n3. Paginating (showing next 5)...")
            result2 = client.list_sandboxes(limit=5, offset=5)
            print(f"   ✓ Returned {len(result2['sandboxes'])} more sandboxes")
        
        # Cleanup
        print("\n4. Cleaning up test sandboxes...")
        for sandbox_id in sandbox_ids:
            client.delete_sandbox(sandbox_id)
            print(f"   ✓ Deleted {sandbox_id[:8]}...")


def example_error_handling():
    """Example demonstrating error handling"""
    print("\n" + "=" * 60)
    print("Example 4: Error Handling")
    print("=" * 60)
    
    client = SandboxClient(
        api_url='http://localhost:8080/v1',
        bearer_token='your-bearer-token-here'
    )
    
    try:
        # Try to get non-existent sandbox
        print("\n1. Attempting to get non-existent sandbox...")
        try:
            client.get_sandbox('00000000-0000-0000-0000-000000000000')
        except SessionNotFoundError as e:
            print(f"   ✓ Caught SessionNotFoundError: {e.message}")
        
        # Try invalid TTL
        print("\n2. Attempting to create sandbox with invalid TTL...")
        try:
            client.create_sandbox(ttl=30)  # Too short (min is 60)
        except ValueError as e:
            print(f"   ✓ Caught ValueError: {e}")
        
        # Try invalid pagination
        print("\n3. Attempting to list with invalid limit...")
        try:
            client.list_sandboxes(limit=150)  # Too high (max is 100)
        except ValueError as e:
            print(f"   ✓ Caught ValueError: {e}")
        
    finally:
        client.close()


def example_sandbox_lifecycle():
    """Example showing complete sandbox lifecycle"""
    print("\n" + "=" * 60)
    print("Example 5: Complete Sandbox Lifecycle")
    print("=" * 60)
    
    with SandboxClient(
        api_url='http://localhost:8080/v1',
        bearer_token='your-bearer-token-here'
    ) as client:
        
        # Create sandbox with specific image and metadata
        print("\n1. Creating sandbox with detailed configuration...")
        sandbox = client.create_sandbox(
            ttl=3600,
            image='ubuntu:22.04',
            metadata={
                'user': 'alice',
                'project': 'data-analysis',
                'environment': 'production',
                'cost_center': 'engineering',
                'timestamp': datetime.utcnow().isoformat()
            }
        )
        print(f"   ✓ Sandbox created: {sandbox.sandbox_id}")
        print(f"   - Image: ubuntu:22.04")
        print(f"   - TTL: 3600 seconds")
        print(f"   - Metadata entries: {len(sandbox.metadata)}")
        
        # Check sandbox status
        print("\n2. Checking sandbox status...")
        current = client.get_sandbox(sandbox.sandbox_id)
        time_until_expiry = (current.expires_at - datetime.now(current.expires_at.tzinfo)).total_seconds()
        print(f"   ✓ Status: {current.status.value}")
        print(f"   - Time until expiry: {time_until_expiry:.0f} seconds")
        
        # In a real application, you would:
        # - Execute commands in the session
        # - Upload/download files
        # - Run code
        # etc.
        
        # Cleanup
        print("\n3. Terminating sandbox...")
        client.delete_sandbox(sandbox.sandbox_id)
        print(f"   ✓ Sandbox terminated and cleaned up")


def example_run_code():
    """Example running code via REST /sandboxes/{sandboxId}/code"""
    print("\n" + "=" * 60)
    print("Example 7: Run Code via REST")
    print("=" * 60)

    with SandboxClient(
        api_url='http://localhost:8080/v1',
        bearer_token='your-bearer-token-here'
    ) as client:
        # Create a sandbox (Python image recommended for Python code)
        sandbox = client.create_sandbox(ttl=600, image='python:3.11', metadata={'example': 'run_code'})
        print(f"   ✓ Sandbox: {sandbox.sandbox_id[:8]}...")

        # Run Python code
        print("\n1. Running Python code...")
        py = client.run_code(
            sandbox_id=sandbox.sandbox_id,
            language='python',
            code='import platform; print("PY:", platform.python_version())',
            timeout=30,
        )
        print(f"   ✓ Status: {py['status']} (exit={py['exitCode']})")
        if py.get('stdout'):
            print("   stdout:")
            print("   " + py['stdout'].replace('\n', '\n   ').rstrip())
        if py.get('stderr'):
            print("   stderr:")
            print("   " + py['stderr'].replace('\n', '\n   ').rstrip())

        # Run a Bash snippet
        print("\n2. Running Bash snippet...")
        sh = client.run_code(
            sandbox_id=sandbox.sandbox_id,
            language='bash',
            code='echo BASH: $(uname -s) && ls -1 | head -n 5',
            timeout=20,
        )
        print(f"   ✓ Status: {sh['status']} (exit={sh['exitCode']})")
        print("   " + sh.get('stdout', '').replace('\n', '\n   ').rstrip())

        # Cleanup
        client.delete_sandbox(sandbox.sandbox_id)
        print("\n   ✓ Cleaned up sandbox")


def example_batch_operations():
    """Example of batch operations on sandboxes"""
    print("\n" + "=" * 60)
    print("Example 6: Batch Operations")
    print("=" * 60)
    
    with SandboxClient(
        api_url='http://localhost:8080/v1',
        bearer_token='your-bearer-token-here'
    ) as client:
        
        # Create multiple sandboxes
        print("\n1. Creating batch of sandboxes...")
        sandboxes = []
        for env in ['development', 'staging', 'production']:
            sandbox = client.create_sandbox(
                ttl=1800,
                image='python:3.11',
                metadata={'environment': env, 'batch': 'test'}
            )
            sandboxes.append(sandbox)
            print(f"   ✓ Created {env} sandbox: {sandbox.sandbox_id[:8]}...")
        
        # Get details for all sandboxes
        print("\n2. Fetching details for all sandboxes...")
        for sandbox in sandboxes:
            details = client.get_sandbox(sandbox.sandbox_id)
            print(f"   - {details.metadata['environment']}: {details.status.value}")
        
        # Cleanup all sandboxes
        print("\n3. Cleaning up all sandboxes...")
        for sandbox in sandboxes:
            client.delete_sandbox(sandbox.sandbox_id)
        print(f"   ✓ Deleted {len(sandboxes)} sandboxes")


def example_pause_resume():
    """Example of pausing and resuming a sandbox"""
    print("\n" + "=" * 60)
    print("Example 7: Pause and Resume Sandbox")
    print("=" * 60)

    with SandboxClient(
        api_url='http://localhost:8080/v1',
        bearer_token='your-bearer-token-here'
    ) as client:
        sandbox = client.create_sandbox(ttl=900, image='python:3.11', metadata={'example': 'pause_resume'})
        print(f"   ✓ Created: {sandbox.sandbox_id}")

        # Pause
        paused = client.pause_sandbox(sandbox.sandbox_id)
        print(f"   ✓ Paused → status: {paused.status.value}")

        # Resume
        resumed = client.resume_sandbox(sandbox.sandbox_id)
        print(f"   ✓ Resumed → status: {resumed.status.value}")

        # Cleanup
        client.delete_sandbox(sandbox.sandbox_id)
        print("   ✓ Deleted sandbox")


def main():
    """Run all examples"""
    print("\n" + "🚀 " * 20)
    print("Sandbox Sessions SDK - Examples")
    print("🚀 " * 20)
    
    # Note: These examples will fail if the API server is not running
    # or if authentication is not properly configured
    
    try:
        example_basic_usage()
        example_context_manager()
        example_list_sandboxes()
        example_error_handling()
        example_sandbox_lifecycle()
        example_batch_operations()
        example_run_code()
        example_pause_resume()
        
        print("\n" + "=" * 60)
        print("✓ All examples completed!")
        print("=" * 60)
        
    except SandboxConnectionError as e:
        print(f"\n❌ Connection Error: {e.message}")
        print("   Make sure the API server is running at the correct URL")
    except UnauthorizedError as e:
        print(f"\n❌ Authentication Error: {e.message}")
        print("   Make sure your bearer token is valid")
    except Exception as e:
        print(f"\n❌ Unexpected Error: {e}")
        import traceback
        traceback.print_exc()


if __name__ == '__main__':
    # You can set these environment variables:
    # export SANDBOX_API_URL=http://localhost:8080/v1
    # export SANDBOX_API_TOKEN=your-jwt-token-here
    
    api_url = os.environ.get('SANDBOX_API_URL', 'http://localhost:8080/v1')
    bearer_token = os.environ.get('SANDBOX_API_TOKEN')
    
    if not bearer_token:
        print("⚠️  Warning: SANDBOX_API_TOKEN not set")
        print("   Set environment variable or update the bearer_token in the code")
        print("\n   Example:")
        print("   export SANDBOX_API_TOKEN='your-jwt-token-here'")
        print("   python test_sessions_sdk.py")
    else:
        main()
