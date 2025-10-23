"""
Test/Example usage of the Sandbox Sessions SDK

This file demonstrates how to use the SessionsClient to interact
with the Sandbox API's session management endpoints.
"""

import os
import time
from datetime import datetime
from sandbox_sessions_sdk import (
    SessionsClient, 
    Session,
    SessionStatus,
    SandboxAPIError,
    SandboxConnectionError,
    SessionNotFoundError,
    UnauthorizedError,
    RateLimitError
)


def example_basic_usage():
    """Basic example of creating and managing a session"""
    print("=" * 60)
    print("Example 1: Basic Session Management")
    print("=" * 60)
    
    # Initialize client
    client = SessionsClient(
        api_url='http://localhost:8080/v1',
        bearer_token='your-bearer-token-here'
    )
    
    try:
        # Create a session
        print("\n1. Creating a new session...")
        session = client.create_session(
            ttl=3600,  # 1 hour
            image='python:3.11',
            metadata={'example': 'basic_usage'}
        )
        print(f"   ✓ Session created: {session.session_id}")
        print(f"   - Status: {session.status.value}")
        print(f"   - Expires at: {session.expires_at}")
        
        # Get session details
        print("\n2. Fetching session details...")
        fetched = client.get_session(session.session_id)
        print(f"   ✓ Session found")
        print(f"   - Created: {fetched.created_at}")
        print(f"   - Metadata: {fetched.metadata}")
        
        # Delete session
        print("\n3. Deleting session...")
        client.delete_session(session.session_id)
        print(f"   ✓ Session deleted")
        
    except SandboxAPIError as e:
        print(f"   ✗ Error: {e.message}")
    finally:
        client.close()


def example_context_manager():
    """Example using context manager for automatic cleanup"""
    print("\n" + "=" * 60)
    print("Example 2: Using Context Manager")
    print("=" * 60)
    
    with SessionsClient(
        api_url='http://localhost:8080/v1',
        bearer_token='your-bearer-token-here'
    ) as client:
        
        print("\n1. Creating session with custom TTL...")
        session = client.create_session(
            ttl=7200,  # 2 hours
            metadata={'example': 'context_manager', 'priority': 'high'}
        )
        print(f"   ✓ Session ID: {session.session_id}")
        
        # Client automatically closes when exiting context


def example_list_sessions():
    """Example of listing and paginating through sessions"""
    print("\n" + "=" * 60)
    print("Example 3: Listing Sessions")
    print("=" * 60)
    
    with SessionsClient(
        api_url='http://localhost:8080/v1',
        bearer_token='your-bearer-token-here'
    ) as client:
        
        # Create a few test sessions
        print("\n1. Creating multiple test sessions...")
        session_ids = []
        for i in range(3):
            session = client.create_session(
                ttl=1800,
                metadata={'example': 'listing', 'index': i}
            )
            session_ids.append(session.session_id)
            print(f"   ✓ Created session {i+1}: {session.session_id[:8]}...")
        
        # List all sessions
        print("\n2. Listing all sessions...")
        result = client.list_sessions(limit=10, offset=0)
        print(f"   ✓ Total sessions: {result['total']}")
        print(f"   ✓ Returned: {len(result['sessions'])} sessions")
        
        for session in result['sessions']:
            print(f"   - {session.session_id}")
            print(f"     Status: {session.status.value}")
            print(f"     Metadata: {session.metadata}")
        
        # Pagination example
        if result['total'] > 5:
            print("\n3. Paginating (showing next 5)...")
            result2 = client.list_sessions(limit=5, offset=5)
            print(f"   ✓ Returned {len(result2['sessions'])} more sessions")
        
        # Cleanup
        print("\n4. Cleaning up test sessions...")
        for session_id in session_ids:
            client.delete_session(session_id)
            print(f"   ✓ Deleted {session_id[:8]}...")


def example_error_handling():
    """Example demonstrating error handling"""
    print("\n" + "=" * 60)
    print("Example 4: Error Handling")
    print("=" * 60)
    
    client = SessionsClient(
        api_url='http://localhost:8080/v1',
        bearer_token='your-bearer-token-here'
    )
    
    try:
        # Try to get non-existent session
        print("\n1. Attempting to get non-existent session...")
        try:
            client.get_session('00000000-0000-0000-0000-000000000000')
        except SessionNotFoundError as e:
            print(f"   ✓ Caught SessionNotFoundError: {e.message}")
        
        # Try invalid TTL
        print("\n2. Attempting to create session with invalid TTL...")
        try:
            client.create_session(ttl=30)  # Too short (min is 60)
        except ValueError as e:
            print(f"   ✓ Caught ValueError: {e}")
        
        # Try invalid pagination
        print("\n3. Attempting to list with invalid limit...")
        try:
            client.list_sessions(limit=150)  # Too high (max is 100)
        except ValueError as e:
            print(f"   ✓ Caught ValueError: {e}")
        
    finally:
        client.close()


def example_session_lifecycle():
    """Example showing complete session lifecycle"""
    print("\n" + "=" * 60)
    print("Example 5: Complete Session Lifecycle")
    print("=" * 60)
    
    with SessionsClient(
        api_url='http://localhost:8080/v1',
        bearer_token='your-bearer-token-here'
    ) as client:
        
        # Create session with specific image and metadata
        print("\n1. Creating session with detailed configuration...")
        session = client.create_session(
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
        print(f"   ✓ Session created: {session.session_id}")
        print(f"   - Image: ubuntu:22.04")
        print(f"   - TTL: 3600 seconds")
        print(f"   - Metadata entries: {len(session.metadata)}")
        
        # Check session status
        print("\n2. Checking session status...")
        current = client.get_session(session.session_id)
        time_until_expiry = (current.expires_at - datetime.now(current.expires_at.tzinfo)).total_seconds()
        print(f"   ✓ Status: {current.status.value}")
        print(f"   - Time until expiry: {time_until_expiry:.0f} seconds")
        
        # In a real application, you would:
        # - Execute commands in the session
        # - Upload/download files
        # - Run code
        # etc.
        
        # Cleanup
        print("\n3. Terminating session...")
        client.delete_session(session.session_id)
        print(f"   ✓ Session terminated and cleaned up")


def example_batch_operations():
    """Example of batch operations on sessions"""
    print("\n" + "=" * 60)
    print("Example 6: Batch Operations")
    print("=" * 60)
    
    with SessionsClient(
        api_url='http://localhost:8080/v1',
        bearer_token='your-bearer-token-here'
    ) as client:
        
        # Create multiple sessions
        print("\n1. Creating batch of sessions...")
        sessions = []
        for env in ['development', 'staging', 'production']:
            session = client.create_session(
                ttl=1800,
                image='python:3.11',
                metadata={'environment': env, 'batch': 'test'}
            )
            sessions.append(session)
            print(f"   ✓ Created {env} session: {session.session_id[:8]}...")
        
        # Get details for all sessions
        print("\n2. Fetching details for all sessions...")
        for session in sessions:
            details = client.get_session(session.session_id)
            print(f"   - {details.metadata['environment']}: {details.status.value}")
        
        # Cleanup all sessions
        print("\n3. Cleaning up all sessions...")
        for session in sessions:
            client.delete_session(session.session_id)
        print(f"   ✓ Deleted {len(sessions)} sessions")


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
        example_list_sessions()
        example_error_handling()
        example_session_lifecycle()
        example_batch_operations()
        
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
