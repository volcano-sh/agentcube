import base64
import json
import time
import os
import ast
import shlex
from typing import Dict, Optional, Any, List, Union
from urllib.parse import urljoin

import requests
import jwt
from cryptography.hazmat.primitives.asymmetric import rsa

from agentcube.utils.log import get_logger
from agentcube.exceptions import CommandExecutionError

class DataPlaneClient:
    """Client for AgentCube Data Plane (Router -> PicoD).
    Handles command execution and file operations via the Router.
    """

    def __init__(
        self,
        session_id: str,
        private_key: rsa.RSAPrivateKey,
        router_url: Optional[str] = None,
        namespace: Optional[str] = None,
        cr_name: Optional[str] = None,
        base_url: Optional[str] = None,
        timeout: int = 30
    ):
        """Initialize Data Plane client.

        Args:
            session_id: Session ID (for x-agentcube-session-id header).
            private_key: RSA Private Key for signing JWTs.
            router_url: Base URL of the Router service (optional if base_url is provided).
            namespace: Kubernetes namespace (optional if base_url is provided).
            cr_name: Code Interpreter resource name (optional if base_url is provided).
            base_url: Direct base URL for invocations (overrides router logic).
            timeout: Default request timeout.
        """
        self.session_id = session_id
        self.private_key = private_key
        self.timeout = timeout
        self.logger = get_logger(f"{__name__}.DataPlaneClient")
        
        if base_url:
            self.base_url = base_url
            self.cr_name = cr_name # Might be None, but that's fine if base_url is used
        elif router_url and namespace and cr_name:
            self.cr_name = cr_name
            # Construct the base invocation URL
            # Router path: /v1/namespaces/{namespace}/code-interpreters/{name}/invocations
            base_path = f"/v1/namespaces/{namespace}/code-interpreters/{cr_name}/invocations/"
            self.base_url = urljoin(router_url, base_path)
        else:
            raise ValueError("Either 'base_url' or all of 'router_url', 'namespace', 'cr_name' must be provided.")
        
        self.session = requests.Session()
        # Add the routing header
        self.session.headers.update({
            "x-agentcube-session-id": self.session_id
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
        if body:
            headers["Content-Type"] = "application/json"

        token = self._create_jwt()
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

    def execute_command(self, command: Union[str, List[str]], timeout: Optional[float] = None) -> str:
        """Execute a shell command.
        
        Args:
            command: The command to execute, either as a single string or a list of arguments.
            timeout: Optional timeout for the command execution.
        """
        # Convert timeout to string with 's' suffix as expected by PicoD
        timeout_value = timeout or self.timeout
        timeout_str = f"{timeout_value}s" if isinstance(timeout_value, (int, float)) else str(timeout_value)

        cmd_list = shlex.split(command, posix=True) if isinstance(command, str) else command

        payload = {
            "command": cmd_list,
            "timeout": timeout_str
        }
        body = json.dumps(payload).encode('utf-8')
        
        # Add a buffer to the read timeout to allow PicoD to return the timeout response
        # otherwise requests might raise ReadTimeout before we get the JSON response with exit_code 124
        read_timeout = timeout_value + 2.0 if isinstance(timeout_value, (int, float)) else timeout_value
        
        resp = self._request("POST", "api/execute", body=body, timeout=read_timeout)
        resp.raise_for_status()
        
        result = resp.json()
        if result["exit_code"] != 0:
             raise CommandExecutionError(
                 exit_code=result["exit_code"],
                 stderr=result["stderr"],
                 command=command
             )
        
        return result["stdout"]

    def run_code(self, language: str, code: str, timeout: Optional[float] = None) -> str:
        """Run a code snippet (python or bash)."""
        lang = language.lower()
        if lang in ["python", "py", "python3"]:
            # Sanitize code: Fix double-escaped newlines if the code is invalid syntax
            try:
                ast.parse(code)
            except SyntaxError:
                # Try fixing escaped newlines (replace literal "\n" with newline char)
                # This handles cases where LLMs/parsers double-escape newlines.
                fixed_code = code.replace("\\n", "\n")
                try:
                    ast.parse(fixed_code)
                    # If fixed code is valid, use it
                    self.logger.warning("Detected and fixed double-escaped newlines in Python code.")
                    code = fixed_code
                except SyntaxError:
                    # If still invalid, stick to original to preserve user intent/error
                    pass
            except Exception as e:
                # Fallback for any other ast parsing error (shouldn't break execution flow)
                self.logger.debug(f"AST parsing fallback error: {e}", exc_info=True)

            # Use file-based execution to avoid shell quoting issues and length limits
            filename = f"script_{int(time.time() * 1000)}.py"
            self.write_file(code, filename)
            cmd = ["python3", filename]
        elif lang in ["bash", "sh"]:
            # Also use file execution for bash to be consistent and safe
            filename = f"script_{int(time.time() * 1000)}.sh"
            self.write_file(code, filename)
            cmd = ["bash", filename]
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
            # unless strictly required by server. PicoD server logic usually checks body_sha256 
            # if body is raw, but for multipart it might skip or handle differently.
            # Looking at previous PicoDClient, it skipped body_sha256 for multipart.
            
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

    def download_file(self, remote_path: str, local_path: str) -> None:
        """Download a file."""
        # GET request, no body, no body_hash claim
        clean_path = remote_path.lstrip("/")
        resp = self._request("GET", f"api/files/{clean_path}", stream=True)
        resp.raise_for_status()
        
        if os.path.dirname(local_path):
            os.makedirs(os.path.dirname(local_path), exist_ok=True)
        with open(local_path, 'wb') as f:
            for chunk in resp.iter_content(chunk_size=8192):
                f.write(chunk)

    def list_files(self, path: str = ".") -> Any:
        """List files in a directory."""
        resp = self._request("GET", "api/files", params={"path": path})
        resp.raise_for_status()
        return resp.json().get("files", [])

    def close(self):
        self.session.close()