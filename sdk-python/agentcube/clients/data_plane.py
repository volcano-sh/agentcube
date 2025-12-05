import base64
import json
import time
import shlex
import os
from typing import Dict, List, Optional, Any
from urllib.parse import urljoin

import requests
import jwt
from cryptography.hazmat.primitives import hashes
from cryptography.hazmat.primitives.asymmetric import rsa

from agentcube.utils.log import get_logger

class DataPlaneClient:
    """Client for AgentCube Data Plane (Router -> PicoD).
    Handles command execution and file operations via the Router.
    """

    def __init__(
        self,
        router_url: str,
        namespace: str,
        session_id: str,
        private_key: rsa.RSAPrivateKey,
        timeout: int = 30
    ):
        """Initialize Data Plane client.

        Args:
            router_url: Base URL of the Router service.
            namespace: Kubernetes namespace.
            session_id: ID of the Code Interpreter session.
            private_key: RSA Private Key for signing JWTs.
            timeout: Default request timeout.
        """
        self.session_id = session_id
        self.private_key = private_key
        self.timeout = timeout
        self.logger = get_logger(f"{__name__}.DataPlaneClient")
        
        # Construct the base invocation URL
        # Router path: /v1/code-namespaces/{namespace}/code-interpreters/{name}/invocations
        # Note: 'name' here corresponds to the session_id (or the resource name used in creation).
        # In AgentCube, the resource name usually EQUALS the session_id if dynamically created, 
        # OR the template name if static.
        # Based on previous logic, create_sandbox returns sessionId.
        # The URL provided by user: .../code-interpreters/{name}/invocations
        # If create_session returned an ID, we use that.
        
        base_path = f"/v1/code-namespaces/{namespace}/code-interpreters/{session_id}/invocations/"
        self.base_url = urljoin(router_url, base_path)
        if not self.base_url.endswith("/"):
             self.base_url += "/"

        self.session = requests.Session()
        # Add the routing header
        self.session.headers.update({
            "x-agentcube-session-id": session_id
        })

    def _create_jwt(self, payload_extra: Dict[str, Any] = None) -> str:
        """Create a signed JWT for authentication."""
        now = int(time.time())
        claims = {
            "iss": "sdk-client",
            "iat": now,
            "exp": now + 300, # 5 mins expiry
        }
        if payload_extra:
            claims.update(payload_extra)
            
        return jwt.encode(
            payload=claims,
            key=self.private_key,
            algorithm="RS256"
        )

    def _request(self, method: str, endpoint: str, body: bytes = None, **kwargs) -> requests.Response:
        """Make an authenticated request to the Data Plane."""
        url = urljoin(self.base_url, endpoint)
        
        headers = {}
        claims = {}
        
        if body:
            # Calculate body hash for JWT
            digest = hashes.Hash(hashes.SHA256())
            digest.update(body)
            claims["body_sha256"] = digest.finalize().hex()
            headers["Content-Type"] = "application/json"
        
        token = self._create_jwt(claims)
        headers["Authorization"] = f"Bearer {token}"
        
        # Merge headers
        req_headers = self.session.headers.copy()
        req_headers.update(headers)
        if "headers" in kwargs:
            req_headers.update(kwargs.pop("headers"))

        self.logger.debug(f"{method} {url}")
        
        return requests.request(
            method=method,
            url=url,
            data=body,
            headers=req_headers,
            **kwargs
        )

    def execute_command(self, command: str, timeout: Optional[float] = None) -> str:
        """Execute a shell command."""
        payload = {
            "command": command,
            "timeout": timeout or self.timeout
        }
        body = json.dumps(payload).encode('utf-8')
        
        resp = self._request("POST", "api/execute", body=body, timeout=timeout or self.timeout)
        resp.raise_for_status()
        
        result = resp.json()
        if result["exit_code"] != 0:
             raise Exception(f"Command failed (exit {result['exit_code']}): {result['stderr']}")
        
        return result["stdout"]

    def run_code(self, language: str, code: str, timeout: Optional[float] = None) -> str:
        """Run a code snippet (python or bash)."""
        lang = language.lower()
        if lang in ["python", "py", "python3"]:
            cmd = f"python3 -c {shlex.quote(code)}"
        elif lang in ["bash", "sh"]:
            cmd = f"bash -c {shlex.quote(code)}"
        else:
            raise ValueError(f"Unsupported language: {language}")
            
        return self.execute_command(cmd, timeout)

    def write_file(self, content: str, remote_path: str) -> None:
        """Write text content to a file."""
        content_bytes = content.encode('utf-8')
        content_b64 = base64.b64encode(content_bytes).decode('utf-8')
        
        payload = {
            "path": remote_path,
            "content": content_b64,
            "mode": "0644"
        }
        body = json.dumps(payload).encode('utf-8')
        
        resp = self._request("POST", "api/files", body=body)
        resp.raise_for_status()

    def upload_file(self, local_path: str, remote_path: str) -> None:
        """Upload a local file using multipart/form-data."""
        if not os.path.exists(local_path):
            raise FileNotFoundError(f"Local file not found: {local_path}")
            
        with open(local_path, 'rb') as f:
            files = {'file': f}
            data = {'path': remote_path, 'mode': '0644'}
            
            # Note: For multipart, we typically don't hash the body for JWT in this simple client
            # unless strictly required by server. Picod server logic usually checks body_sha256 
            # if body is raw, but for multipart it might skip or handle differently.
            # Looking at previous PicodClient, it skipped body_sha256 for multipart.
            
            # We use _request but we need to bypass the body hashing logic and let requests handle multipart
            # So we construct headers manually here or modify _request.
            # Easier to just do specific logic here.
            
            url = urljoin(self.base_url, "api/files")
            token = self._create_jwt() # No body hash
            headers = {
                "Authorization": f"Bearer {token}",
                "x-agentcube-session-id": self.session_id
            }
            
            self.logger.debug(f"Uploading file {local_path} to {remote_path}")
            resp = self.session.post(url, files=files, data=data, headers=headers, timeout=self.timeout)
            resp.raise_for_status()
        """Download a file."""
        # GET request, no body, no body_hash claim
        clean_path = remote_path.lstrip("/")
        resp = self._request("GET", f"api/files/{clean_path}", stream=True)
        resp.raise_for_status()
        
        os.makedirs(os.path.dirname(local_path), exist_ok=True)
        with open(local_path, 'wb') as f:
            for chunk in resp.iter_content(chunk_size=8192):
                f.write(chunk)

    def close(self):
        self.session.close()
