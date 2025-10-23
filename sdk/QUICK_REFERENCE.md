# Sandbox Sessions SDK - Quick Reference

## Installation
```bash
pip install requests
```

## Import
```python
from sandbox_sessions_sdk import SessionsClient, Session, SessionStatus
from sandbox_sessions_sdk import (
    SessionNotFoundError, UnauthorizedError, RateLimitError
)
```

## Initialize Client
```python
# Basic
client = SessionsClient(
    api_url='https://api.sandbox.example.com/v1',
    bearer_token='your-jwt-token'
)

# With options
client = SessionsClient(
    api_url='https://api.sandbox.example.com/v1',
    bearer_token='your-jwt-token',
    timeout=30,
    verify_ssl=True
)
```

## Create Session
```python
# Minimal
session = client.create_session()

# With parameters
session = client.create_session(
    ttl=7200,                    # 60-28800 seconds
    image='python:3.11',         # Optional
    metadata={'user': 'alice'},  # Optional
    ssh_public_key='ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIB2VExampleBase64KeyMaterial user@example'  # Optional
)
```

## Get Session
```python
session = client.get_session('session-uuid')

# Access properties
print(session.session_id)     # UUID string
print(session.status)          # SessionStatus.RUNNING or PAUSED
print(session.created_at)      # datetime object
print(session.expires_at)      # datetime object
print(session.metadata)        # dict
```

## List Sessions
```python
# Basic
result = client.list_sessions()

# With pagination
result = client.list_sessions(limit=10, offset=0)

# Access results
print(result['total'])         # Total count
print(result['sessions'])      # List of Session objects
print(result['limit'])         # Limit used
print(result['offset'])        # Offset used
```

## Delete Session
```python
client.delete_session('session-uuid')
```

## Run Code
```python
result = client.run_code(
    session_id='session-uuid',
    code='print("Hello")',
    language='python',   # 'python' | 'javascript' | 'bash'
    timeout=30           # 1..300 seconds
)
print(result['status'], result['exitCode'])
print(result['stdout'])
print(result['stderr'])
```

## Context Manager (Recommended)
```python
with SessionsClient(api_url='...', bearer_token='...') as client:
    session = client.create_session()
    # ... use session ...
    client.delete_session(session.session_id)
# Client automatically closes
```

## Error Handling
```python
try:
    session = client.create_session()
except UnauthorizedError as e:
    print(f"Auth failed: {e.message}")
except SessionNotFoundError as e:
    print(f"Not found: {e.message}")
except RateLimitError as e:
    print(f"Rate limit: {e.limit}, reset: {e.reset}")
except SandboxOperationError as e:
    print(f"Error: {e.message} (code: {e.error_code})")
except SandboxConnectionError as e:
    print(f"Connection failed: {e.message}")
```

## Common Patterns

### Create, Use, Delete
```python
with SessionsClient(...) as client:
    session = client.create_session(ttl=3600)
    
    # Use session ID for other operations
    print(f"Session ready: {session.session_id}")
    
    # Clean up
    client.delete_session(session.session_id)
```

### List and Filter
```python
with SessionsClient(...) as client:
    result = client.list_sessions(limit=100)
    
    # Filter by metadata
    my_sessions = [
        s for s in result['sessions'] 
        if s.metadata.get('user') == 'alice'
    ]
```

### Batch Create
```python
with SessionsClient(...) as client:
    sessions = []
    for env in ['dev', 'staging', 'prod']:
        session = client.create_session(
            metadata={'environment': env}
        )
        sessions.append(session)
    
    # Use sessions...
    
    # Cleanup
    for session in sessions:
        client.delete_session(session.session_id)
```

### Check Expiration
```python
from datetime import datetime, timezone

session = client.get_session('session-uuid')
now = datetime.now(timezone.utc)
seconds_left = (session.expires_at - now).total_seconds()

if seconds_left < 300:  # Less than 5 minutes
    print("Session expiring soon!")
```

## Limits & Constraints

| Parameter | Min | Max | Default |
|-----------|-----|-----|---------|
| TTL | 60 | 28800 | 3600 |
| List Limit | 1 | 100 | 50 |
| List Offset | 0 | ∞ | 0 |
| Code Timeout | 1 | 300 | 60 |
| Language | - | - | python |

## Environment Variables
```bash
export SANDBOX_API_URL=https://api.sandbox.example.com/v1
export SANDBOX_API_TOKEN=your-jwt-token-here
```

## Testing
```bash
# Run examples
python examples/test_sessions_sdk.py

# Run integration tests
python test_sessions_integration.py
```

## HTTP Status Codes

| Code | Exception | Meaning |
|------|-----------|---------|
| 200 | - | Success |
| 400 | SandboxOperationError | Bad request |
| 401 | UnauthorizedError | Not authenticated |
| 404 | SessionNotFoundError | Not found |
| 429 | RateLimitError | Too many requests |
| 500 | SandboxOperationError | Server error |

## Session Status

- `SessionStatus.RUNNING` - Active session
- `SessionStatus.PAUSED` - Paused session

## Complete Example
```python
#!/usr/bin/env python3
from sandbox_sessions_sdk import SessionsClient, SessionNotFoundError

def main():
    # Connect
    with SessionsClient(
        api_url='https://api.sandbox.example.com/v1',
        bearer_token='your-jwt-token'
    ) as client:
        
        # Create
        session = client.create_session(
            ttl=3600,
            image='python:3.11',
            metadata={'project': 'demo'}
        )
        print(f"Created: {session.session_id}")
        
        # Verify
        details = client.get_session(session.session_id)
        print(f"Status: {details.status.value}")
        
        # List
        result = client.list_sessions()
        print(f"Total sessions: {result['total']}")
        
        # Cleanup
        client.delete_session(session.session_id)
        print("Deleted")

if __name__ == '__main__':
    try:
        main()
    except Exception as e:
        print(f"Error: {e}")
```

## Tips

1. ✅ Always use context manager
2. ✅ Handle specific exceptions
3. ✅ Set appropriate TTL
4. ✅ Add descriptive metadata
5. ✅ Delete sessions when done
6. ✅ Check expiration times
7. ✅ Implement retry logic for rate limits

## Documentation
- Full docs: `SESSIONS_SDK.md`
- Examples: `examples/test_sessions_sdk.py`
- Tests: `test_sessions_integration.py`
