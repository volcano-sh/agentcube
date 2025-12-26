import base64
import hashlib
import json
import time
import os
import ast
import shlex
from typing import Dict, Optional, Any, List, Union
from urllib.parse import urljoin, urlparse

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
        private_key: Optional[rsa.RSAPrivateKey] = None,
        router_url: Optional[str] = None,
        namespace: Optional[str] = None,
        cr_name: Optional[str] = None,
        base_url: Optional[str] = None,
        timeout: int = 30
    ):
        """Initialize Data Plane client.

        Args:
            session_id: Session ID (for x-agentcube-session-id header).
            private_key: RSA Private Key for signing JWTs (optional in Static Key Mode).
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
            algorithm="PS256"
        )

    def _request(self, method: str, endpoint: str, body: bytes = None, **kwargs) -> requests.Response:
        """Make an authenticated request to the Data Plane."""
        url = urljoin(self.base_url, endpoint)
        
        # Build full URL with params for signature calculation
        params = kwargs.get("params")
        if params:
            from urllib.parse import urlencode
            query_string = urlencode(params, doseq=True)
            url_with_params = f"{url}?{query_string}"
        else:
            url_with_params = url
        
        headers = {}
        if body:
            headers["Content-Type"] = "application/json"

        if self.private_key:
            # Build canonical request hash for anti-tampering
            canonical_request_hash = self._build_canonical_request_hash(
                method=method.upper(),
                url=url_with_params,  # Use URL with params for signature
                headers=headers,
                body=body or b""
            )
            token = self._create_jwt({"canonical_request_sha256": canonical_request_hash})
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

    def _build_canonical_request_hash(self, method: str, url: str, headers: Dict[str, str], body: bytes) -> str:
        """Build canonical request string and return its SHA256 hash.
        
        Format matches server-side buildCanonicalRequestHash:
        HTTPMethod + \n + URI + \n + QueryString + \n + CanonicalHeaders + \n + SignedHeaders + \n + BodyHash
        """
        parsed = urlparse(url)
        
        # 1. HTTP Method
        http_method = method.upper()
        
        # 2. Canonical URI (path only)
        uri = parsed.path or "/"
        
        # 3. Canonical Query String (sorted)
        query_string = self._build_canonical_query_string(parsed.query)
        
        # 4. Canonical Headers (sorted, lowercase)
        canonical_headers, signed_headers = self._build_canonical_headers(headers, parsed.netloc)
        
        # 5. Body hash
        body_hash = hashlib.sha256(body).hexdigest()
        
        # Build canonical request
        canonical_request = "\n".join([
            http_method,
            uri,
            query_string,
            canonical_headers,
            signed_headers,
            body_hash
        ])
        
        # Return SHA256 of canonical request
        return hashlib.sha256(canonical_request.encode('utf-8')).hexdigest()
    
    def _build_canonical_query_string(self, query: str) -> str:
        """Build sorted canonical query string."""
        if not query:
            return ""
        
        pairs = []
        for part in query.split('&'):
            if '=' in part:
                k, v = part.split('=', 1)
                pairs.append((k, v))
            else:
                pairs.append((part, ""))
        
        pairs.sort(key=lambda x: (x[0], x[1]))
        return "&".join(f"{k}={v}" for k, v in pairs)
    
    def _build_canonical_headers(self, headers: Dict[str, str], host: str) -> tuple:
        """Build canonical headers string and signed headers list."""
        # Only include content-type for request integrity
        header_map = {}
        
        if "Content-Type" in headers:
            header_map["content-type"] = headers["Content-Type"].strip()
        elif "content-type" in headers:
            header_map["content-type"] = headers["content-type"].strip()
        
        # Sort header names
        sorted_keys = sorted(header_map.keys())
        
        # Build canonical headers and signed headers
        if sorted_keys:
            header_lines = [f"{k}:{header_map[k]}" for k in sorted_keys]
            canonical_headers = "\n".join(header_lines) + "\n"
        else:
            canonical_headers = "\n"
        signed_headers = ";".join(sorted_keys)
        
        return canonical_headers, signed_headers

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
            headers = {
                "x-agentcube-session-id": self.session_id
            }
            if self.private_key:
                token = self._create_jwt() # No body hash
                headers["Authorization"] = f"Bearer {token}"
            
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