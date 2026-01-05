# Copyright The Volcano Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

import base64
import json
import time
import os
import ast
import shlex
from typing import Optional, Any, List, Union
from urllib.parse import urljoin

import requests

from agentcube.utils.log import get_logger
from agentcube.utils.http import create_session
from agentcube.exceptions import CommandExecutionError

class DataPlaneClient:
    """Client for AgentCube Data Plane (Router -> PicoD).
    Handles command execution and file operations via the Router.
    
    Note: JWT authentication is now handled by the Router. The SDK no longer
    needs to generate keys or sign requests.
    """

    def __init__(
        self,
        session_id: str,
        router_url: Optional[str] = None,
        namespace: Optional[str] = None,
        cr_name: Optional[str] = None,
        base_url: Optional[str] = None,
        timeout: int = 120,
        connect_timeout: float = 5.0,
        pool_connections: int = 10,
        pool_maxsize: int = 10,
    ):
        """Initialize Data Plane client.

        Args:
            session_id: Session ID (for x-agentcube-session-id header).
            router_url: Base URL of the Router service (optional if base_url is provided).
            namespace: Kubernetes namespace (optional if base_url is provided).
            cr_name: Code Interpreter resource name (optional if base_url is provided).
            base_url: Direct base URL for invocations (overrides router logic).
            timeout: Default request timeout in seconds (default: 120).
            connect_timeout: Connection timeout in seconds (default: 5).
            pool_connections: Number of connection pools to cache (default: 10).
            pool_maxsize: Maximum connections per pool (default: 10).
        """
        self.session_id = session_id
        self.timeout = timeout
        self.connect_timeout = connect_timeout
        self.pool_connections = pool_connections
        self.pool_maxsize = pool_maxsize
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
        
        # Create session with connection pooling using shared utility
        self.session = create_session(
            pool_connections=pool_connections,
            pool_maxsize=pool_maxsize,
        )
        
        # Add the routing header - Router will add JWT auth
        self.session.headers.update({
            "x-agentcube-session-id": self.session_id
        })

    def _request(self, method: str, endpoint: str, body: bytes = None, **kwargs) -> requests.Response:
        """Make a request to the Data Plane via Router.
        
        Note: Router handles JWT authentication, so we don't add Authorization header here.
        """
        url = urljoin(self.base_url, endpoint)
        
        headers = {}
        if body:
            headers["Content-Type"] = "application/json"
        
        # Set timeout as (connect_timeout, read_timeout) tuple if not provided
        if "timeout" not in kwargs:
            kwargs["timeout"] = (self.connect_timeout, self.timeout)
        elif isinstance(kwargs["timeout"], (int, float)):
            # If a single timeout is provided, use it as read_timeout with default connect_timeout
            kwargs["timeout"] = (self.connect_timeout, kwargs["timeout"])

        # Merge headers from kwargs to prevent TypeError (restoring previous behavior)
        if "headers" in kwargs:
            headers.update(kwargs.pop("headers"))

        self.logger.debug(f"{method} {url}")
        
        # Use session for connection pooling
        return self.session.request(
            method=method,
            url=url,
            data=body,
            headers=headers,
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
            
            url = urljoin(self.base_url, "api/files")
            headers = {
                "x-agentcube-session-id": self.session_id
            }
            
            self.logger.debug(f"Uploading file {local_path} to {remote_path}")
            resp = self.session.post(url, files=files, data=data, headers=headers, timeout=self.timeout)
            resp.raise_for_status()

    def download_file(self, remote_path: str, local_path: str) -> None:
        """Download a file."""
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