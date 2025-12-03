"""
PicoD REST API Client

This is a lightweight REST API client for interacting with the PicoD daemon.
PicoD provides basic sandbox capabilities through simple HTTP endpoints.

This client provides the same interface as SSHClient for easy migration.
"""

import os
import shlex
import base64
import time
import json
import logging
from typing import Dict, List, Optional, Any
from urllib.parse import urljoin

logger = logging.getLogger(__name__)

try:
    import requests
except ImportError:
    raise ImportError(
        "requests library is required for PicoDClient. "
        "Install it with: pip install requests"
    )

try:
    import jwt
except ImportError:
    raise ImportError(
        "PyJWT library is required for PicoDClient. "
        "Install it with: pip install PyJWT"
    )

try:
    from cryptography.hazmat.primitives import serialization, hashes
    from cryptography.hazmat.primitives.asymmetric import rsa
    from cryptography.hazmat.backends import default_backend
except ImportError:
    raise ImportError(
        "cryptography library is required for RSA authentication. "
        "Install it with: pip install cryptography"
    )


# RSA Key Pair container
class RSAKeyPair:
    """Container for RSA public and private keys"""
    def __init__(self, private_key: rsa.RSAPrivateKey, public_key: rsa.RSAPublicKey):
        self.private_key = private_key
        self.public_key = public_key


class PicoDClient:
    """Client for interacting with PicoD daemon via REST API

    This client provides the same interface as SandboxSSHClient,
    making it an alternative to SSH-based communication.
    """

    def __init__(
        self,
        api_url: str,
        namespace: str,
        name: str,
        session_id: Optional[str] = None,
        timeout: int = 30,
    ):
        """Initialize PicoD client connection parameters

        Args:
            api_url: AgentCube API URL (serves as both Manager and Gateway)
            namespace: Kubernetes namespace of the Code Interpreter
            name: Name of the Code Interpreter
            session_id: Pre-existing session ID (optional)
            timeout: Default request timeout in seconds
        """
        self.gateway_url = api_url
        self.namespace = namespace
        self.name = name
        self.session_id = session_id
        self.timeout = timeout
        self.session = requests.Session()
        self.key_pair: Optional[RSAKeyPair] = None

    def generate_rsa_key_pair(self, key_file: Optional[str] = None) -> RSAKeyPair:
        """Generate a new RSA-2048 key pair

        Args:
            key_file: Path to save private key (default: picod_client_keys.pem)

        Returns:
            RSAKeyPair containing the generated keys
        """
        key_file = key_file or "picod_client_keys.pem" # Default key file for SDK generated keys.

        # Generate private key
        private_key = rsa.generate_private_key(
            public_exponent=65537,
            key_size=2048,
            backend=default_backend()
        )
        public_key = private_key.public_key()

        # Serialize private key to PEM format
        private_pem = private_key.private_bytes(
            encoding=serialization.Encoding.PEM,
            format=serialization.PrivateFormat.PKCS8,
            encryption_algorithm=serialization.NoEncryption()
        )

        # Save to file
        with open(key_file, 'wb') as f:
            f.write(private_pem)
        os.chmod(key_file, 0o600)

        self.key_pair = RSAKeyPair(private_key, public_key)
        # self.key_file = key_file # Removed as per new design

        logger.info(f"RSA key pair generated and saved to {key_file}")
        return self.key_pair

    def load_rsa_key_pair(self, key_file: Optional[str] = None) -> RSAKeyPair:
        """Load RSA key pair from file

        Args:
            key_file: Path to private key file

        Returns:
            RSAKeyPair containing the loaded keys

        Raises:
            FileNotFoundError: If key file doesn't exist
        """
        key_file = key_file or "picod_client_keys.pem" # Default key file for SDK generated keys.

        if not os.path.exists(key_file):
            raise FileNotFoundError(f"Private key file not found: {key_file}")

        with open(key_file, 'rb') as f:
            private_key = serialization.load_pem_private_key(
                f.read(),
                password=None,
                backend=default_backend()
            )

        public_key = private_key.public_key()
        self.key_pair = RSAKeyPair(private_key, public_key)
        # self.key_file = key_file # Removed as per new design

        return self.key_pair

    def _create_signed_jwt(self, claims_payload: Dict[str, Any], exp_delta_seconds: int = 300) -> str:
        """Create a signed JWT token using the session private key"""
        if not self.key_pair:
            raise ValueError("No key pair loaded. Generate or load keys first.")

        # Default claims
        now = int(time.time())
        claims = {
            "iss": "sdk-client",
            "iat": now,
            "exp": now + exp_delta_seconds,  # Token valid for exp_delta_seconds
        }
        claims.update(claims_payload)

        # Use PyJWT to encode the token
        # We need to pass the private key object (cryptography's RSAPrivateKey) directly.
        # PyJWT handles the conversion and signing.
        token = jwt.encode(
            payload=claims,
            key=self.key_pair.private_key,
            algorithm="RS256"
        )

        return token
    
    def start_session(self):
        """Start a new session by generating a key pair"""
        self.generate_rsa_key_pair()
        
    def _make_authenticated_request(
        self,
        method: str,
        path: str,
        body_bytes: Optional[bytes] = None,
        files=None,
        data=None,
        stream: bool = False,
        timeout: Optional[float] = None,
    ) -> requests.Response:
        """Make an authenticated request to the PicoD API"""

        # Construct URL relative to the gateway/manager
        url = urljoin(self.gateway_url, path)
        
        headers = {}
        claims = {}
        
        if body_bytes:
            # Calculate body SHA256
            sha256_hash = hashes.Hash(hashes.SHA256())
            sha256_hash.update(body_bytes)
            digest = sha256_hash.finalize()
            claims["body_sha256"] = digest.hex()
            headers["Content-Type"] = "application/json"
            
        # Generate token
        token = self._create_signed_jwt(claims)
        headers["Authorization"] = f"Bearer {token}"
        
        return self.session.request(
            method=method,
            url=url,
            data=body_bytes if body_bytes else data,
            files=files,
            headers=headers,
            stream=stream,
            timeout=timeout
        )

    def execute_command(
        self,
        command: str,
        timeout: Optional[float] = None,
    ) -> str:
        """Execute command in sandbox and return stdout

        Args:
            command: Command to execute
            timeout: Command execution timeout in seconds

        Returns:
            Command stdout output

        Raises:
            Exception: If command execution fails (non-zero exit code)
        """
        payload = {
            "command": command,
            "timeout": timeout or self.timeout,
        }
        body_bytes = json.dumps(payload, sort_keys=True, separators=(',', ':')).encode('utf-8')

        response = self._make_authenticated_request(
            "POST",
            "api/execute", # Path relative to invocations URL
            body_bytes=body_bytes,
            timeout=timeout or self.timeout,
        )

        response.raise_for_status()
        result = response.json()

        if result["exit_code"] != 0:
            raise Exception(
                f"Command execution failed (exit code {result['exit_code']}): {result['stderr']}"
            )

        return result["stdout"]
    
    def execute_commands(self, commands: List[str]) -> Dict[str, str]:
        """Execute multiple commands in sandbox
        
        Compatible with SandboxSSHClient.execute_commands()
        
        Args:
            commands: List of commands to execute
            
        Returns:
            Dictionary mapping commands to outputs
        """
        results = {}
        for cmd in commands:
            results[cmd] = self.execute_command(cmd)
        return results
    
    def run_code(
        self,
        language: str,
        code: str,
        timeout: Optional[float] = None
    ) -> str:
        """Run code snippet in specified language
        
        Compatible with SandboxSSHClient.run_code()
        
        Args:
            language: Programming language (e.g., "python", "bash")
            code: Code snippet to execute
            timeout: Execution timeout in seconds
            
        Returns:
            Code execution output
            
        Raises:
            ValueError: If language is not supported
        """
        lang = language.lower()
        lang_aliases = {
            "python": ["python", "py", "python3"],
            "bash": ["bash", "sh", "shell"]
        }

        target_lang = None
        for std_lang, aliases in lang_aliases.items():
            if lang in aliases:
                target_lang = std_lang
                break
        
        if not target_lang:
            raise ValueError(
                f"Unsupported language: {language}. Supported: {list(lang_aliases.keys())}"
            )

        quoted_code = shlex.quote(code)
        if target_lang == "python":
            command = f"python3 -c {quoted_code}"
        elif target_lang == "bash":
            command = f"bash -c {quoted_code}"

        return self.execute_command(command, timeout)
    
    def write_file(
        self,
        content: str,
        remote_path: str,
    ) -> None:
        """Write content to file in sandbox (JSON/base64)

        Args:
            content: Content to write to remote file
            remote_path: Write path on remote server
        """
        # Encode to base64
        if isinstance(content, str):
            content_bytes = content.encode('utf-8')
        else:
            content_bytes = content

        content_b64 = base64.b64encode(content_bytes).decode('utf-8')

        payload = {
            "path": remote_path,
            "content": content_b64,
            "mode": "0644"
        }
        body_bytes = json.dumps(payload, sort_keys=True, separators=(',', ':')).encode('utf-8')

        response = self._make_authenticated_request(
            "POST",
            "api/files",
            body_bytes=body_bytes,
            timeout=self.timeout,
        )

        response.raise_for_status()

    def upload_file(
        self,
        local_path: str,
        remote_path: str,
    ) -> None:
        """Upload local file to sandbox (multipart/form-data)

        Args:
            local_path: Local file path to upload
            remote_path: Upload path on remote server

        Raises:
            FileNotFoundError: If local file does not exist
        """
        if not os.path.exists(local_path):
            raise FileNotFoundError(f"Local file not found: {local_path}")

        # For multipart, we can't compute hash easily, so no body_sha256 claim
        with open(local_path, 'rb') as f:
            files = {'file': f}
            data = {'path': remote_path, 'mode': '0644'}

            response = self._make_authenticated_request(
                "POST",
                "api/files",
                files=files,
                data=data,
                timeout=self.timeout,
            )

            response.raise_for_status()

    def download_file(
        self,
        remote_path: str,
        local_path: str
    ) -> None:
        """Download file from sandbox

        Args:
            remote_path: Download path on remote server
            local_path: Local path to save downloaded file
        """
        # Ensure local directory exists
        local_dir = os.path.dirname(local_path)
        if local_dir:
            os.makedirs(local_dir, exist_ok=True)

        # Handle path: remove leading /
        clean_remote_path = remote_path.lstrip('/')

        response = self._make_authenticated_request(
            "GET",
            f"api/files/{clean_remote_path}", # Path relative to invocations URL
            body_bytes=None, # GET requests typically have no body
            stream=True,
            timeout=self.timeout,
        )

        response.raise_for_status()

        with open(local_path, 'wb') as f:
            for chunk in response.iter_content(chunk_size=8192):
                if chunk:
                    f.write(chunk)
    
    def list_files(self, path: str) -> List[Dict[str, Any]]:
        """List files in directory

        Args:
            path: Directory path to list

        Returns:
            List of file entries containing name, size, modified, etc.
        """
        payload = {"path": path}
        body_bytes = json.dumps(payload, sort_keys=True, separators=(',', ':')).encode('utf-8')

        response = self._make_authenticated_request(
            "POST",
            "api/files/list",
            body_bytes=body_bytes,
            timeout=self.timeout,
        )

        response.raise_for_status()
        return response.json()["files"]
    
    def cleanup(self) -> None:
        """Clean up resources (close HTTP session)

        Compatible with SandboxSSHClient.cleanup()
        """
        if self.session:
            self.session.close()

    def is_authenticated(self) -> bool:
        """Check if client is using authentication

        Returns:
            True if RSA key pair is loaded
        """
        return self.key_pair is not None

    def __enter__(self):
        """Context manager entry"""
        return self

    def __exit__(self, exc_type, exc_val, exc_tb):
        """Context manager exit"""
        self.cleanup()

    @staticmethod
    def generate_ssh_key_pair():
        """Generate SSH key pair

        This method is kept for API compatibility with SandboxSSHClient,
        but raises an error as PicoD uses JWT authentication, not SSH.

        """
        raise NotImplementedError(
            "PicoD uses JWT authentication, not SSH. "
        )

