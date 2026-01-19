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

"""
E2E Test for Router -> PicoD JWT Authentication Architecture.

This test validates the new single-stage JWT verification flow where:
1. Router generates JWT signing keys
2. Router signs requests before forwarding to PicoD
3. PicoD verifies JWT using public key from environment variable

Test Flow:
1. Generate RSA key pair (simulating Router's key generation)
2. Start PicoD container with PICOD_AUTH_PUBLIC_KEY env var
3. Start Router container (or simulate Router by signing JWTs locally)
4. Test command execution, file operations through the authenticated flow
"""

import base64
import logging
import subprocess
import time
from datetime import datetime, timedelta, timezone

import jwt
import requests
from cryptography.hazmat.backends import default_backend
from cryptography.hazmat.primitives import serialization
from cryptography.hazmat.primitives.asymmetric import rsa

# Configure Logging
logging.basicConfig(level=logging.INFO, format='%(asctime)s - %(name)s - %(levelname)s - %(message)s')
logger = logging.getLogger("e2e_picod_test")

# --- Constants ---
PICOD_IMAGE = "picod-test:latest"
PICOD_CONTAINER_NAME = "picod_router_e2e_test"
PICOD_PORT = 8080

# --- Helper Functions ---

def generate_key_pair():
    """Generate RSA 2048 key pair."""
    private_key = rsa.generate_private_key(
        public_exponent=65537,
        key_size=2048,
        backend=default_backend()
    )

    pub = private_key.public_key()
    pub_pem = pub.public_bytes(
        encoding=serialization.Encoding.PEM,
        format=serialization.PublicFormat.SubjectPublicKeyInfo
    )

    priv_pem = private_key.private_bytes(
        encoding=serialization.Encoding.PEM,
        format=serialization.PrivateFormat.PKCS8,
        encryption_algorithm=serialization.NoEncryption()
    )

    return private_key, priv_pem, pub_pem


def start_picod_container(public_key_pem: bytes):
    """
    Start PicoD container with public key injected via environment variable.
    This simulates the new architecture where WorkloadManager injects PICOD_AUTH_PUBLIC_KEY.
    """
    logger.info(f"Starting PicoD container {PICOD_CONTAINER_NAME}...")

    # Remove existing if any
    subprocess.run(["docker", "rm", "-f", PICOD_CONTAINER_NAME], capture_output=True)

    # Decode public key PEM for environment variable
    pub_key_str = public_key_pem.decode('utf-8')

    cmd = [
        "docker", "run", "-d",
        "--name", PICOD_CONTAINER_NAME,
        "-p", f"{PICOD_PORT}:8080",
        "-e", f"PICOD_AUTH_PUBLIC_KEY={pub_key_str}",
        PICOD_IMAGE
    ]

    logger.info(
        f"Running: docker run -d --name {PICOD_CONTAINER_NAME} "
        f"-p {PICOD_PORT}:8080 -e PICOD_AUTH_PUBLIC_KEY=<key> {PICOD_IMAGE}"
    )
    result = subprocess.run(cmd, capture_output=True, text=True)
    if result.returncode != 0:
        raise RuntimeError(f"Failed to start PicoD container: {result.stderr}")

    # Wait for health check
    wait_for_health(f"http://localhost:{PICOD_PORT}/health", "PicoD")


def wait_for_health(url: str, service_name: str, retries: int = 15):
    """Wait for service health check to pass."""
    for i in range(retries):
        try:
            resp = requests.get(url, timeout=2)
            if resp.status_code == 200:
                data = resp.json()
                logger.info(f"{service_name} is up! Status: {data.get('status', 'unknown')}")
                return
        except (requests.ConnectionError, requests.Timeout) as e:
            logger.debug(f"Health check attempt {i+1} for {service_name} failed: {e}")
        logger.info(f"Waiting for {service_name}... ({i+1}/{retries})")
        time.sleep(1)

    raise RuntimeError(f"{service_name} failed to start or is unhealthy")


def stop_containers():
    """Stop all test containers."""
    logger.info("Stopping test containers...")
    subprocess.run(["docker", "rm", "-f", PICOD_CONTAINER_NAME], capture_output=True)


class RouterSimulator:
    """
    Simulates Router's JWT signing behavior.
    In production, Router would handle this, but for E2E testing,
    we simulate Router by signing JWTs with the same algorithm.
    """

    def __init__(self, private_key, picod_url: str):
        self.private_key = private_key
        self.picod_url = picod_url.rstrip("/")
        self.session = requests.Session()

    def _sign_jwt(self, claims: dict = None) -> str:
        """Generate a JWT token like Router would."""
        now = datetime.now(timezone.utc)
        payload = {
            "iss": "agentcube-router",
            "iat": now,
            "exp": now + timedelta(minutes=5),
        }
        if claims:
            payload.update(claims)

        return jwt.encode(payload, self.private_key, algorithm="RS256")

    def request(self, method: str, endpoint: str, **kwargs) -> requests.Response:
        """Make an authenticated request (simulating Router's forwarding)."""
        url = f"{self.picod_url}/{endpoint.lstrip('/')}"

        # Sign JWT like Router does
        token = self._sign_jwt({
            "path": endpoint,
        })

        headers = kwargs.pop("headers", {})
        headers["Authorization"] = f"Bearer {token}"

        return self.session.request(method, url, headers=headers, **kwargs)

    def execute_command(self, command: list, timeout: str = "30s") -> dict:
        """Execute a command via PicoD."""
        payload = {
            "command": command if isinstance(command, list) else [command],
            "timeout": timeout
        }
        resp = self.request("POST", "/api/execute", json=payload)
        resp.raise_for_status()
        return resp.json()

    def upload_file(self, content: str, path: str):
        """Upload a file to PicoD."""
        payload = {
            "path": path,
            "content": base64.b64encode(content.encode()).decode(),
            "mode": "0644"
        }
        resp = self.request("POST", "/api/files", json=payload)
        resp.raise_for_status()
        return resp.json()

    def download_file(self, path: str) -> bytes:
        """Download a file from PicoD."""
        resp = self.request("GET", f"/api/files/{path}")
        resp.raise_for_status()
        return resp.content

    def list_files(self, path: str = ".") -> list:
        """List files in a directory."""
        resp = self.request("GET", "/api/files", params={"path": path})
        resp.raise_for_status()
        return resp.json().get("files", [])


# --- Tests ---

def test_health_check():
    """Test that PicoD health check returns ok status."""
    logger.info(">>> TEST: Health Check")
    resp = requests.get(f"http://localhost:{PICOD_PORT}/health")
    assert resp.status_code == 200
    data = resp.json()
    assert data["status"] == "ok"
    logger.info(f"Health check passed: {data}")


def test_unauthorized_access():
    """Test that unauthenticated requests are rejected."""
    logger.info(">>> TEST: Unauthorized Access")
    resp = requests.post(
        f"http://localhost:{PICOD_PORT}/api/execute",
        json={"command": ["echo", "hello"]}
    )
    assert resp.status_code == 401, f"Expected 401, got {resp.status_code}"
    logger.info("Unauthorized access correctly rejected")


def test_invalid_token():
    """Test that requests with invalid JWT are rejected."""
    logger.info(">>> TEST: Invalid Token")

    # Generate a different key pair
    wrong_key, _, _ = generate_key_pair()
    wrong_token = jwt.encode(
        {"iss": "fake", "iat": datetime.now(timezone.utc), "exp": datetime.now(timezone.utc) + timedelta(hours=1)},
        wrong_key,
        algorithm="RS256"
    )

    resp = requests.post(
        f"http://localhost:{PICOD_PORT}/api/execute",
        json={"command": ["echo", "hello"]},
        headers={"Authorization": f"Bearer {wrong_token}"}
    )
    assert resp.status_code == 401, f"Expected 401, got {resp.status_code}"
    logger.info("Invalid token correctly rejected")


def test_command_execution(client: RouterSimulator):
    """Test basic command execution."""
    logger.info(">>> TEST: Command Execution")
    result = client.execute_command(["echo", "Hello Router-PicoD!"])
    assert result["exit_code"] == 0
    assert result["stdout"].strip() == "Hello Router-PicoD!"
    logger.info(f"Command output: {result['stdout'].strip()}")


def test_python_execution(client: RouterSimulator):
    """Test Python code execution."""
    logger.info(">>> TEST: Python Execution")

    # Upload Python script
    code = "import sys; print(f'Python {sys.version_info.major}.{sys.version_info.minor}')"
    client.upload_file(code, "test_python.py")

    # Execute it
    result = client.execute_command(["python3", "test_python.py"])
    assert result["exit_code"] == 0
    assert "Python 3" in result["stdout"]
    logger.info(f"Python output: {result['stdout'].strip()}")


def test_file_operations(client: RouterSimulator):
    """Test file upload, download, and listing."""
    logger.info(">>> TEST: File Operations")

    # Upload
    content = "Hello from Router-PicoD E2E test!"
    client.upload_file(content, "e2e_test.txt")
    logger.info("File uploaded")

    # Verify with cat
    result = client.execute_command(["cat", "e2e_test.txt"])
    assert result["stdout"].strip() == content
    logger.info("File content verified via cat")

    # Download
    downloaded = client.download_file("e2e_test.txt")
    assert downloaded.decode() == content
    logger.info("File downloaded and verified")

    # List
    files = client.list_files(".")
    filenames = [f["name"] for f in files]
    assert "e2e_test.txt" in filenames
    logger.info(f"File listing: {filenames}")


def test_timeout(client: RouterSimulator):
    """Test command timeout handling."""
    logger.info(">>> TEST: Timeout Handling")

    # Should pass with sufficient timeout
    result = client.execute_command(["sleep", "0.1"], timeout="5s")
    assert result["exit_code"] == 0

    # Should timeout
    result = client.execute_command(["sleep", "5"], timeout="0.5s")
    assert result["exit_code"] == 124  # Timeout exit code
    assert "timed out" in result["stderr"].lower()
    logger.info("Timeout correctly handled")


# --- Main ---

def main():
    try:
        # 1. Generate Router's key pair
        logger.info("=== SETUP: Generating Router's JWT key pair ===")
        router_private_key, _, router_public_key_pem = generate_key_pair()

        # 2. Start PicoD with public key
        logger.info("=== SETUP: Starting PicoD container ===")
        start_picod_container(router_public_key_pem)

        # 3. Create Router simulator
        logger.info("=== SETUP: Creating Router simulator ===")
        client = RouterSimulator(
            private_key=router_private_key,
            picod_url=f"http://localhost:{PICOD_PORT}"
        )

        # 4. Run tests
        logger.info("=== RUNNING TESTS ===")
        test_health_check()
        test_unauthorized_access()
        test_invalid_token()
        test_command_execution(client)
        test_python_execution(client)
        test_file_operations(client)
        test_timeout(client)

        logger.info("=== ALL TESTS PASSED! ===")

    except Exception as e:
        logger.error(f"Test failed: {e}")
        # Print container logs on failure
        logger.info("--- PicoD Container Logs ---")
        logs = subprocess.run(["docker", "logs", PICOD_CONTAINER_NAME], capture_output=True, text=True)
        print(logs.stdout)
        print(logs.stderr)
        raise

    finally:
        stop_containers()


if __name__ == "__main__":
    main()
