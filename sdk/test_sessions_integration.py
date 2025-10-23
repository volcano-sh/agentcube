#!/usr/bin/env python3
"""
Integration tests for Sandbox Sessions SDK

Run these tests against a live API server to verify functionality.

Usage:
    export SANDBOX_API_URL=http://localhost:8080/v1
    export SANDBOX_API_TOKEN=your-token-here
    python test_sessions_integration.py
"""

import os
import sys
import time
import unittest
from datetime import datetime

from sandbox_sessions_sdk import (
    SessionsClient,
    Session,
    SessionStatus,
    SandboxAPIError,
    SandboxConnectionError,
    SessionNotFoundError,
    UnauthorizedError,
    RateLimitError,
    SandboxOperationError
)


class TestSessionsIntegration(unittest.TestCase):
    """Integration tests for SessionsClient"""
    
    @classmethod
    def setUpClass(cls):
        """Set up test client"""
        cls.api_url = os.environ.get('SANDBOX_API_URL', 'http://localhost:8080/v1')
        cls.bearer_token = os.environ.get('SANDBOX_API_TOKEN')
        
        if not cls.bearer_token:
            raise ValueError("SANDBOX_API_TOKEN environment variable is required")
        
        cls.client = SessionsClient(
            api_url=cls.api_url,
            bearer_token=cls.bearer_token,
            timeout=30
        )
        
        print(f"\nTesting against: {cls.api_url}")
    
    @classmethod
    def tearDownClass(cls):
        """Clean up test client"""
        cls.client.close()
    
    def test_01_create_session_minimal(self):
        """Test creating a session with minimal parameters"""
        session = self.client.create_session()
        
        self.assertIsInstance(session, Session)
        self.assertIsNotNone(session.session_id)
        self.assertEqual(session.status, SessionStatus.RUNNING)
        self.assertIsInstance(session.created_at, datetime)
        self.assertIsInstance(session.expires_at, datetime)
        
        # Cleanup
        self.client.delete_session(session.session_id)
    
    def test_02_create_session_with_all_params(self):
        """Test creating a session with all parameters"""
        session = self.client.create_session(
            ttl=7200,
            image='python:3.11',
            metadata={
                'test': 'integration',
                'user': 'test-user',
                'timestamp': datetime.utcnow().isoformat()
            }
        )
        
        self.assertIsInstance(session, Session)
        self.assertEqual(session.metadata.get('test'), 'integration')
        
        # Cleanup
        self.client.delete_session(session.session_id)
    
    def test_03_get_session(self):
        """Test getting session details"""
        # Create a session
        session = self.client.create_session(
            metadata={'test': 'get_session'}
        )
        
        # Get the session
        fetched = self.client.get_session(session.session_id)
        
        self.assertEqual(fetched.session_id, session.session_id)
        self.assertEqual(fetched.status, session.status)
        self.assertEqual(fetched.metadata.get('test'), 'get_session')
        
        # Cleanup
        self.client.delete_session(session.session_id)
    
    def test_04_get_nonexistent_session(self):
        """Test getting a session that doesn't exist"""
        with self.assertRaises(SessionNotFoundError):
            self.client.get_session('00000000-0000-0000-0000-000000000000')
    
    def test_05_delete_session(self):
        """Test deleting a session"""
        # Create a session
        session = self.client.create_session()
        
        # Delete it
        self.client.delete_session(session.session_id)
        
        # Verify it's gone
        with self.assertRaises(SessionNotFoundError):
            self.client.get_session(session.session_id)
    
    def test_06_delete_nonexistent_session(self):
        """Test deleting a session that doesn't exist"""
        with self.assertRaises(SessionNotFoundError):
            self.client.delete_session('00000000-0000-0000-0000-000000000000')
    
    def test_07_list_sessions(self):
        """Test listing sessions"""
        # Create some test sessions
        sessions = []
        for i in range(3):
            session = self.client.create_session(
                metadata={'test': 'list_sessions', 'index': i}
            )
            sessions.append(session)
        
        try:
            # List sessions
            result = self.client.list_sessions(limit=10)
            
            self.assertIn('sessions', result)
            self.assertIn('total', result)
            self.assertIn('limit', result)
            self.assertIn('offset', result)
            self.assertGreaterEqual(result['total'], 3)
            self.assertIsInstance(result['sessions'], list)
            
            # Verify our sessions are in the list
            session_ids = [s.session_id for s in result['sessions']]
            for session in sessions:
                if session.session_id in session_ids:
                    break
            else:
                self.fail("Created sessions not found in list")
        
        finally:
            # Cleanup
            for session in sessions:
                try:
                    self.client.delete_session(session.session_id)
                except:
                    pass
    
    def test_08_list_sessions_pagination(self):
        """Test session listing pagination"""
        # Get first page
        page1 = self.client.list_sessions(limit=5, offset=0)
        
        self.assertLessEqual(len(page1['sessions']), 5)
        self.assertEqual(page1['limit'], 5)
        self.assertEqual(page1['offset'], 0)
        
        # Get second page if there are enough sessions
        if page1['total'] > 5:
            page2 = self.client.list_sessions(limit=5, offset=5)
            self.assertEqual(page2['offset'], 5)
    
    def test_09_invalid_ttl(self):
        """Test creating session with invalid TTL"""
        # TTL too short
        with self.assertRaises(ValueError):
            self.client.create_session(ttl=30)
        
        # TTL too long
        with self.assertRaises(ValueError):
            self.client.create_session(ttl=50000)
    
    def test_10_invalid_list_params(self):
        """Test listing with invalid parameters"""
        # Limit too high
        with self.assertRaises(ValueError):
            self.client.list_sessions(limit=150)
        
        # Negative offset
        with self.assertRaises(ValueError):
            self.client.list_sessions(offset=-1)
    
    def test_11_context_manager(self):
        """Test using client as context manager"""
        with SessionsClient(
            api_url=self.api_url,
            bearer_token=self.bearer_token
        ) as client:
            session = client.create_session()
            self.assertIsNotNone(session.session_id)
            
            # Cleanup
            client.delete_session(session.session_id)
    
    def test_12_session_to_dict(self):
        """Test Session.to_dict() method"""
        session = self.client.create_session(
            metadata={'test': 'to_dict'}
        )
        
        try:
            session_dict = session.to_dict()
            
            self.assertIsInstance(session_dict, dict)
            self.assertEqual(session_dict['sessionId'], session.session_id)
            self.assertEqual(session_dict['status'], session.status.value)
        
        finally:
            self.client.delete_session(session.session_id)
    
    def test_13_session_lifecycle(self):
        """Test complete session lifecycle"""
        # Create
        session = self.client.create_session(
            ttl=3600,
            image='ubuntu:22.04',
            metadata={
                'test': 'lifecycle',
                'user': 'test-user',
                'created': datetime.utcnow().isoformat()
            }
        )
        
        try:
            # Verify created
            self.assertIsNotNone(session.session_id)
            self.assertEqual(session.status, SessionStatus.RUNNING)
            
            # Get and verify
            fetched = self.client.get_session(session.session_id)
            self.assertEqual(fetched.session_id, session.session_id)
            
            # Verify in list
            result = self.client.list_sessions(limit=100)
            session_ids = [s.session_id for s in result['sessions']]
            self.assertIn(session.session_id, session_ids)
            
        finally:
            # Delete
            self.client.delete_session(session.session_id)
            
            # Verify deleted
            with self.assertRaises(SessionNotFoundError):
                self.client.get_session(session.session_id)


def run_tests():
    """Run the integration tests"""
    # Check environment
    if not os.environ.get('SANDBOX_API_TOKEN'):
        print("❌ Error: SANDBOX_API_TOKEN environment variable is required")
        print("\nUsage:")
        print("  export SANDBOX_API_URL=http://localhost:8080/v1")
        print("  export SANDBOX_API_TOKEN=your-token-here")
        print("  python test_sessions_integration.py")
        return False
    
    # Run tests
    suite = unittest.TestLoader().loadTestsFromTestCase(TestSessionsIntegration)
    runner = unittest.TextTestRunner(verbosity=2)
    result = runner.run(suite)
    
    return result.wasSuccessful()


if __name__ == '__main__':
    print("=" * 70)
    print("Sandbox Sessions SDK - Integration Tests")
    print("=" * 70)
    
    success = run_tests()
    
    print("\n" + "=" * 70)
    if success:
        print("✓ All tests passed!")
    else:
        print("✗ Some tests failed")
    print("=" * 70)
    
    sys.exit(0 if success else 1)
