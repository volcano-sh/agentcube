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
from datetime import datetime
from typing import Dict, List, Optional

logger = logging.getLogger(__name__)

try:
    import requests
except ImportError:
    raise ImportError(
        "requests library is required for PicoDClient. "
        "Install it with: pip install requests"
    )

try:
    from cryptography.hazmat.primitives import serialization, hashes
    from cryptography.hazmat.primitives.asymmetric import rsa, padding
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

    Example:
        >>> client = PicoDClient(host="localhost", port=9527)
        >>> client.generate_rsa_key_pair()  # First time setup
        >>> client.initialize_server()      # Initialize server with public key
        >>> result = client.execute_command("echo 'Hello World'")
        >>> print(result)
        Hello World

        >>> client.write_file(content="test data", remote_path="/tmp/test.txt")
        >>> client.download_file(remote_path="/tmp/test.txt", local_path="./test.txt")
    """

    def __init__(
        self,
        host: str,
        port: int = 9527,
        timeout: int = 30,
        key_file: Optional[str] = None,
    ):
        """Initialize PicoD client connection parameters

        Args:
            host: PicoD server hostname or IP address
            port: PicoD server port (default: 9527)
            timeout: Default request timeout in seconds
            key_file: Path to RSA private key file (optional, for authentication)
        """
        self.base_url = f"http://{host}:{port}"
        self.timeout = timeout
        self.session = requests.Session()
        self.key_file = key_file or "picod_client_keys.pem"
        self.key_pair: Optional[RSAKeyPair] = None
        self.initialized = False

    def generate_rsa_key_pair(self, key_file: Optional[str] = None) -> RSAKeyPair:
        """Generate a new RSA-2048 key pair

        Args:
            key_file: Path to save private key (default: picod_client_keys.pem)

        Returns:
            RSAKeyPair containing the generated keys
        """
        key_file = key_file or self.key_file

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
        self.key_file = key_file

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
        key_file = key_file or self.key_file

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
        self.key_file = key_file

        return self.key_pair

    def get_public_key_pem(self) -> str:
        """Get public key in PEM format for server initialization

        Returns:
            Public key as PEM-encoded string
        """
        if not self.key_pair:
            raise ValueError("No key pair loaded. Generate or load keys first.")

        public_pem = self.key_pair.public_key.public_bytes(
            encoding=serialization.Encoding.PEM,
            format=serialization.PublicFormat.SubjectPublicKeyInfo
        )

        return public_pem.decode('utf-8')

    def initialize_server(self) -> bool:
        """Initialize the PicoD server with this client's public key

        Returns:
            True if successful

        Raises:
            ValueError: If no key pair is available
        """
        if not self.key_pair:
            # Try to load existing keys
            try:
                self.load_rsa_key_pair()
            except FileNotFoundError:
                # Generate new keys if none exist
                logger.info("No key pair found. Generating new keys...")
                self.generate_rsa_key_pair()

        public_key_pem = self.get_public_key_pem()

        payload = {
            "public_key": public_key_pem
        }

        try:
            response = self.session.post(
                f"{self.base_url}/api/init",
                json=payload,
                timeout=self.timeout,
            )
            response.raise_for_status()

            result = response.json()
            if result.get("success"):
                self.initialized = True
                logger.info(f"Server initialized: {result.get('message')}")
                return True
            else:
                raise Exception(result.get("message", "Unknown error"))
        except Exception as e:
            logger.error(f"Server initialization failed: {e}")
            return False

    def _sign_request(self, timestamp: str, body: str) -> str:
        """Sign a request with RSA private key

        Args:
            timestamp: RFC3339 formatted timestamp
            body: Request body as string

        Returns:
            Base64-encoded signature
        """
        if not self.key_pair:
            raise ValueError("No key pair loaded. Generate or load keys first.")

        # Create message: timestamp + body
        message = timestamp + body

        # Sign the message directly (no manual hashing)
        signature = self.key_pair.private_key.sign(
            message.encode('utf-8'),
            padding.PKCS1v15(),
            hashes.SHA256()
        )

        return base64.b64encode(signature).decode('utf-8')

    def _make_authenticated_request(self, method: str, url: str, **kwargs) -> requests.Response:
        """Make an authenticated HTTP request with RSA signature

        Args:
            method: HTTP method
            url: Full URL
            **kwargs: Additional arguments for requests

        Returns:
            HTTP response
        """
        if not self.key_pair:
            raise ValueError("No key pair loaded. Generate or load keys first.")

        # Get or create request body
        body_str = ""
        if 'json' in kwargs:
            body_str = json.dumps(kwargs['json'], sort_keys=True, separators=(',', ':'))
        elif 'data' in kwargs and isinstance(kwargs['data'], str):
            body_str = kwargs['data']
        elif 'files' in kwargs:
            # For multipart requests, use empty body for signature
            body_str = ""

        # Generate timestamp with sub-second precision to prevent replay attacks
        timestamp = datetime.utcnow().isoformat() + 'Z'
        signature = self._sign_request(timestamp, body_str)

        # Add headers
        headers = kwargs.get('headers', {})
        headers['X-Timestamp'] = timestamp
        headers['X-Signature'] = signature
        kwargs['headers'] = headers

        return self.session.request(method, url, **kwargs)
    
    def execute_command(
        self,
        command: str,
        timeout: Optional[float] = None,
    ) -> str:
        """Execute command in sandbox and return stdout

        Compatible with SandboxSSHClient.execute_command()

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

        if self.key_pair:
            # Use authenticated request
            response = self._make_authenticated_request(
                "POST",
                f"{self.base_url}/api/execute",
                json=payload,
                timeout=timeout or self.timeout,
            )
        else:
            # Use unauthenticated request
            response = self.session.post(
                f"{self.base_url}/api/execute",
                json=payload,
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

        Compatible with SandboxSSHClient.write_file()

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

        if self.key_pair:
            # Use authenticated request
            response = self._make_authenticated_request(
                "POST",
                f"{self.base_url}/api/files",
                json=payload,
                timeout=self.timeout,
            )
        else:
            # Use unauthenticated request
            response = self.session.post(
                f"{self.base_url}/api/files",
                json=payload,
                timeout=self.timeout,
            )

        response.raise_for_status()

    def upload_file(
        self,
        local_path: str,
        remote_path: str,
    ) -> None:
        """Upload local file to sandbox (multipart/form-data)

        Compatible with SandboxSSHClient.upload_file()

        Args:
            local_path: Local file path to upload
            remote_path: Upload path on remote server

        Raises:
            FileNotFoundError: If local file does not exist
        """
        if not os.path.exists(local_path):
            raise FileNotFoundError(f"Local file not found: {local_path}")

        with open(local_path, 'rb') as f:
            files = {'file': f}
            data = {'path': remote_path, 'mode': '0644'}

            if self.key_pair:
                # Use authenticated request
                response = self._make_authenticated_request(
                    "POST",
                    f"{self.base_url}/api/files",
                    files=files,
                    data=data,
                    timeout=self.timeout,
                )
            else:
                # Use unauthenticated request
                response = self.session.post(
                    f"{self.base_url}/api/files",
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

        Compatible with SandboxSSHClient.download_file()

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

        if self.key_pair:
            # Use authenticated request
            response = self._make_authenticated_request(
                "GET",
                f"{self.base_url}/api/files/{clean_remote_path}",
                stream=True,
                timeout=self.timeout,
            )
        else:
            # Use unauthenticated request
            response = self.session.get(
                f"{self.base_url}/api/files/{clean_remote_path}",
                stream=True,
                timeout=self.timeout,
            )

        response.raise_for_status()

        with open(local_path, 'wb') as f:
            for chunk in response.iter_content(chunk_size=8192):
                if chunk:
                    f.write(chunk)
    
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
        """Generate SSH key pair (not applicable for PicoD)

        This method is kept for API compatibility with SandboxSSHClient,
        but raises an error as PicoD uses RSA authentication, not SSH.

        Use generate_rsa_key_pair() instead for PicoD.
        """
        raise NotImplementedError(
            "PicoD uses RSA authentication, not SSH. "
            "Use generate_rsa_key_pair() instead."
        )

