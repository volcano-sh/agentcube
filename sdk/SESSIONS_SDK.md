# Sandbox Sessions SDK - Python Client

A Python SDK for managing sandbox sessions via the Sandbox REST API.

## Installation

```bash
pip install requests  # Only dependency
```

## Quick Start

```python
from sandbox_sessions_sdk import SessionsClient

# Initialize the client
client = SessionsClient(
    api_url='https://api.sandbox.example.com/v1',
    bearer_token='your-jwt-token-here'
)

# Create a new session
session = client.create_session(
    ttl=3600,  # 1 hour
    image='python:3.11',
    metadata={'user': 'alice', 'project': 'test'}
)

print(f"Session ID: {session.session_id}")
print(f"Expires at: {session.expires_at}")

# Get session details
session_details = client.get_session(session.session_id)

# Delete the session
client.delete_session(session.session_id)

# Close the client
client.close()
```

## Using Context Manager (Recommended)

```python
from sandbox_sessions_sdk import SessionsClient

with SessionsClient(api_url='...', bearer_token='...') as client:
    session = client.create_session(ttl=3600)
    # Client automatically closes when exiting context
```

## API Reference

### SessionsClient

Main client class for interacting with the Sessions API.

#### Constructor

```python
SessionsClient(
    api_url: str,
    bearer_token: str,
    timeout: int = 30,
    verify_ssl: bool = True
)
```

**Parameters:**
- `api_url`: Base URL of the Sandbox API (e.g., `https://api.sandbox.example.com/v1`)
- `bearer_token`: JWT bearer token for authentication
- `timeout`: Default timeout for HTTP requests in seconds (default: 30)
- `verify_ssl`: Whether to verify SSL certificates (default: True)

#### Methods

##### create_session()

Create a new sandbox session.

```python
session = client.create_session(
    ttl: int = 3600,
    image: Optional[str] = None,
    metadata: Optional[Dict[str, Any]] = None,
    ssh_public_key: Optional[str] = None,
) -> Session
```

**Parameters:**
- `ttl`: Time-to-live in seconds (min: 60, max: 28800, default: 3600)
- `image`: Sandbox environment image (e.g., `'python:3.11'`, `'ubuntu:22.04'`)
- `metadata`: Optional metadata dictionary
- `ssh_public_key`: Optional OpenSSH-formatted public key to authorize for SSH (e.g., `ssh-ed25519 AAAA... user@host`)

**Returns:** `Session` object

**Raises:**
- `ValueError`: If parameters are invalid
- `UnauthorizedError`: If authentication fails
- `RateLimitError`: If rate limit exceeded
- `SandboxOperationError`: For other API errors
- `SandboxConnectionError`: If connection fails

**Example:**
```python
session = client.create_session(
    ttl=7200,  # 2 hours
    image='ubuntu:22.04',
    metadata={
        'user': 'alice',
        'project': 'data-analysis',
        'environment': 'production'
    },
    ssh_public_key='ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIB2VExampleBase64KeyMaterial user@example'
)
```

##### list_sessions()

List all active sessions.

```python
result = client.list_sessions(
    limit: int = 50,
    offset: int = 0
) -> Dict[str, Any]
```

**Parameters:**
- `limit`: Maximum number of sessions to return (min: 1, max: 100, default: 50)
- `offset`: Number of sessions to skip for pagination (default: 0)

**Returns:** Dictionary with:
- `sessions`: List of `Session` objects
- `total`: Total number of sessions
- `limit`: Limit used
- `offset`: Offset used

**Example:**
```python
result = client.list_sessions(limit=10, offset=0)
print(f"Total: {result['total']}")
for session in result['sessions']:
    print(f"  {session.session_id}: {session.status.value}")
```

##### get_session()

Get details about a specific session.

```python
session = client.get_session(session_id: str) -> Session
```

**Parameters:**
- `session_id`: Session UUID

**Returns:** `Session` object

**Raises:**
- `SessionNotFoundError`: If session doesn't exist
- `UnauthorizedError`: If authentication fails

**Example:**
```python
session = client.get_session('550e8400-e29b-41d4-a716-446655440000')
print(f"Status: {session.status.value}")
print(f"Expires: {session.expires_at}")
```

##### delete_session()

Delete a sandbox session.

```python
client.delete_session(session_id: str) -> None
```

**Parameters:**
- `session_id`: Session UUID

**Raises:**
- `SessionNotFoundError`: If session doesn't exist
- `UnauthorizedError`: If authentication fails

**Example:**
```python
client.delete_session('550e8400-e29b-41d4-a716-446655440000')
```

##### run_code()

Execute code inside a sandbox session via the REST API.

```python
result = client.run_code(
    session_id: str,
    code: str,
    language: str = "python",   # one of: 'python', 'javascript', 'bash'
    timeout: int = 60            # seconds, 1..300
) -> Dict[str, Any]
```

**Parameters:**
- `session_id`: Target session UUID
- `code`: Code snippet to execute
- `language`: Programming language (`python` | `javascript` | `bash`)
- `timeout`: Execution timeout in seconds (min: 1, max: 300)

**Returns:** `CommandResult` dict
- `status`: `completed` | `failed` | `timeout`
- `exitCode`: integer or null
- `stdout`: string
- `stderr`: string

**Raises:**
- `ValueError`: If parameters are invalid
- `UnauthorizedError`: If authentication fails
- `SessionNotFoundError`: If the session doesn't exist
- `RateLimitError`: If rate limit exceeded
- `SandboxOperationError`: For other API errors
- `SandboxConnectionError`: If connection fails

**Example:**
```python
result = client.run_code(
    session_id=session.session_id,
    language='python',
    code='import sys; print(sys.version.split()[0])',
    timeout=30,
)
print(result['status'], result['exitCode'])
print(result['stdout'])
print(result['stderr'])
```

### Session Class

Represents a sandbox session.

**Attributes:**
- `session_id` (str): Unique session identifier (UUID)
- `status` (SessionStatus): Session status (`RUNNING` or `PAUSED`)
- `created_at` (datetime): When the session was created
- `expires_at` (datetime): When the session will expire
- `last_activity_at` (Optional[datetime]): Last activity timestamp
- `metadata` (Dict[str, Any]): User-provided metadata

**Methods:**
- `to_dict()`: Convert session to dictionary
- `__repr__()`: String representation

**Example:**
```python
session = client.create_session(ttl=3600)

print(session.session_id)  # '550e8400-e29b-41d4-a716-446655440000'
print(session.status)      # SessionStatus.RUNNING
print(session.created_at)  # datetime object
print(session.expires_at)  # datetime object
print(session.metadata)    # {'user': 'alice'}
```

### SessionStatus Enum

Session status enumeration.

**Values:**
- `SessionStatus.RUNNING`: Session is active
- `SessionStatus.PAUSED`: Session is paused

### Exceptions

All exceptions inherit from `SandboxAPIError`.

#### SandboxAPIError

Base exception for all SDK errors.

**Attributes:**
- `message` (str): Error message
- `error_code` (Optional[str]): API error code
- `details` (Dict[str, Any]): Additional error details
- `status_code` (Optional[int]): HTTP status code

#### SandboxConnectionError

Raised when connection to the API fails.

```python
try:
    client = SessionsClient(api_url='http://invalid', bearer_token='token')
    session = client.create_session()
except SandboxConnectionError as e:
    print(f"Connection failed: {e.message}")
```

#### UnauthorizedError

Raised when authentication fails (HTTP 401).

```python
try:
    client = SessionsClient(api_url='...', bearer_token='invalid-token')
    session = client.create_session()
except UnauthorizedError as e:
    print(f"Auth failed: {e.message}")
```

#### SessionNotFoundError

Raised when a session is not found (HTTP 404).

```python
try:
    session = client.get_session('non-existent-id')
except SessionNotFoundError as e:
    print(f"Session not found: {e.message}")
```

#### RateLimitError

Raised when rate limit is exceeded (HTTP 429).

**Additional Attributes:**
- `limit` (int): Rate limit
- `remaining` (int): Remaining requests
- `reset` (int): When limit resets (Unix timestamp)

```python
try:
    # Make too many requests
    for i in range(1000):
        client.create_session()
except RateLimitError as e:
    print(f"Rate limit: {e.limit}")
    print(f"Remaining: {e.remaining}")
    print(f"Reset at: {e.reset}")
```

#### SandboxOperationError

Raised for other API errors (HTTP 4xx/5xx).

```python
try:
    session = client.create_session(ttl=999999)  # Invalid TTL
except SandboxOperationError as e:
    print(f"Error: {e.message}")
    print(f"Code: {e.error_code}")
    print(f"Details: {e.details}")
```

## Complete Examples

### Example 1: Basic Session Management

```python
from sandbox_sessions_sdk import SessionsClient

with SessionsClient(api_url='...', bearer_token='...') as client:
    # Create session
    session = client.create_session(
        ttl=3600,
        image='python:3.11',
        metadata={'project': 'test'}
    )
    
    # Use session
    print(f"Created: {session.session_id}")
    
    # Get details
    details = client.get_session(session.session_id)
    print(f"Status: {details.status.value}")
    
    # Delete when done
    client.delete_session(session.session_id)
```

### Example 2: Listing and Pagination

```python
with SessionsClient(api_url='...', bearer_token='...') as client:
    # Get first page
    page1 = client.list_sessions(limit=10, offset=0)
    print(f"Total sessions: {page1['total']}")
    
    # Get second page
    if page1['total'] > 10:
        page2 = client.list_sessions(limit=10, offset=10)
        print(f"Page 2 has {len(page2['sessions'])} sessions")
```

### Example 3: Error Handling

```python
from sandbox_sessions_sdk import (
    SessionsClient,
    SessionNotFoundError,
    UnauthorizedError,
    RateLimitError,
    SandboxOperationError
)

client = SessionsClient(api_url='...', bearer_token='...')

try:
    session = client.create_session(ttl=3600)
    # ... do work ...
    client.delete_session(session.session_id)
    
except UnauthorizedError as e:
    print(f"Auth failed: {e.message}")
except SessionNotFoundError as e:
    print(f"Session not found: {e.message}")
except RateLimitError as e:
    print(f"Rate limited. Try again in {e.reset} seconds")
except SandboxOperationError as e:
    print(f"Operation failed: {e.message} ({e.error_code})")
finally:
    client.close()
```

### Example 4: Batch Operations

```python
with SessionsClient(api_url='...', bearer_token='...') as client:
    # Create multiple sessions
    sessions = []
    for env in ['dev', 'staging', 'prod']:
        session = client.create_session(
            ttl=1800,
            metadata={'environment': env}
        )
        sessions.append(session)
    
    # Process all sessions
    for session in sessions:
        print(f"Processing {session.session_id}")
        # ... do work ...
    
    # Cleanup
    for session in sessions:
        client.delete_session(session.session_id)
```

### Example 5: Run Code in a Session

```python
from sandbox_sessions_sdk import SessionsClient

API_URL = 'http://localhost:8080/v1'
TOKEN = 'your-bearer-token-here'

with SessionsClient(api_url=API_URL, bearer_token=TOKEN) as client:
    # Create a session with Python image
    session = client.create_session(ttl=600, image='python:3.11', metadata={'example': 'run_code'})

    # Run Python code
    py = client.run_code(
        session_id=session.session_id,
        language='python',
        code='print("Hello from Python!")',
        timeout=30,
    )
    print('Python:', py['status'], py['exitCode'])
    print(py['stdout'])

    # Run Bash command
    sh = client.run_code(
        session_id=session.session_id,
        language='bash',
        code='echo $SHELL && ls -1 | head -n 3',
        timeout=20,
    )
    print('Bash:', sh['status'])
    print(sh['stdout'])

    # Cleanup
    client.delete_session(session.session_id)
```

## Environment Variables

The SDK respects these environment variables:

- `SANDBOX_API_URL`: Default API URL
- `SANDBOX_API_TOKEN`: Default bearer token

Example:
```bash
export SANDBOX_API_URL=https://api.sandbox.example.com/v1
export SANDBOX_API_TOKEN=eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...
python your_script.py
```

## SSH/SFTP via HTTP CONNECT (Hybrid usage)

The same API server can act as an HTTP CONNECT proxy for tunneled SSH/SFTP to a specific session. Use `SessionSSHClient` to run commands and transfer files over SSH through the API server:

```python
from sandbox_sessions_sdk import SessionsClient, SessionSSHClient

API_URL = "https://api.sandbox.example.com/v1"
TOKEN = "<bearer-token>"
USERNAME = "sandbox"

# 1) Create a session via REST
with SessionsClient(api_url=API_URL, bearer_token=TOKEN) as client:
    session = client.create_session(ttl=1800, image="python:3.11")

# 2) SSH/SFTP over HTTP CONNECT to the same API server
with SessionSSHClient(
    api_url=API_URL,
    bearer_token=TOKEN,
    session_id=session.session_id,
    username=USERNAME,
) as ssh:
    out = ssh.run_command("echo hello && python3 --version")
    print(out["stdout"])  # use upload_file/download_file for SFTP

# 3) Cleanup via REST
with SessionsClient(api_url=API_URL, bearer_token=TOKEN) as client:
    client.delete_session(session.session_id)
```

See a full runnable example in `py/examples/tunnel_ssh_example.py`.

## Testing

Run the example script to test the SDK:

```bash
export SANDBOX_API_URL=http://localhost:8080/v1
export SANDBOX_API_TOKEN=your-token-here
python examples/test_sessions_sdk.py
```

## Best Practices

1. **Use Context Manager**: Always use `with` statement to ensure proper cleanup
2. **Handle Errors**: Catch specific exceptions for better error handling
3. **Set Appropriate TTL**: Choose TTL based on your use case (min 60s, max 8 hours)
4. **Add Metadata**: Include useful metadata for tracking and debugging
5. **Cleanup Sessions**: Always delete sessions when done to free resources
6. **Implement Retries**: Handle rate limits with exponential backoff
7. **Validate Input**: Check parameters before making API calls

## Limitations

- Maximum TTL: 28800 seconds (8 hours)
- Minimum TTL: 60 seconds (1 minute)
- Maximum sessions list limit: 100 per request
- Rate limits apply per user/token (check response headers)

## API Compatibility

This SDK is compatible with Sandbox API v1.0.0 as defined in `sandbox-api-spec.yaml`.

## Support

For issues or questions:
- Check the API documentation
- Review example code in `examples/test_sessions_sdk.py`
- Contact: support@example.com
