import unittest
import os
import time
import json
import subprocess
import requests
from unittest.mock import patch
from cryptography.hazmat.primitives import serialization, hashes
from cryptography.hazmat.primitives.asymmetric import rsa, padding
import base64

# Add SDK path
import sys
sys.path.append(os.path.join(os.getcwd(), "sdk-python"))

from agentcube import CodeInterpreterClient, Sandbox
# Configure logging
import logging
logging.basicConfig(level=logging.INFO, format='%(asctime)s - %(levelname)s - %(message)s')
logger = logging.getLogger(__name__)

PICOD_PORT = 9528
PICOD_URL = f"http://localhost:{PICOD_PORT}"
BOOTSTRAP_KEY_FILE = "test_bootstrap_key.pem"

class TestAgentCubeSDK(unittest.TestCase):
    
    @classmethod
    def setUpClass(cls):
        """Setup test environment: generate keys, start picod mock server"""
        logger.info("Setting up test environment...")
        
        # Cleanup previous runs
        cls.cleanup_files()

        # 1. Generate Bootstrap Keys for Picod
        key = rsa.generate_private_key(public_exponent=65537, key_size=2048)
        pub_key = key.public_key()
        
        with open(BOOTSTRAP_KEY_FILE, "wb") as f:
            f.write(pub_key.public_bytes(
                encoding=serialization.Encoding.PEM,
                format=serialization.PublicFormat.SubjectPublicKeyInfo
            ))
        
        cls.bootstrap_private_key = key
        
        # 2. Start Picod Server (Real binary)
        logger.info(f"Starting Picod on port {PICOD_PORT}...")
        if not os.path.exists("./picod"):
             raise RuntimeError("Picod binary not found. Please run 'go build -o picod ./cmd/picod/' first.")

        cls.picod_process = subprocess.Popen(
            ["./picod", "--bootstrap-key", BOOTSTRAP_KEY_FILE, "--port", str(PICOD_PORT)],
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE
        )
        time.sleep(2) # Wait for server startup
        
        if cls.picod_process.poll() is not None:
            out, err = cls.picod_process.communicate()
            logger.error(f"Picod failed to start. Stdout: {out}, Stderr: {err}")
            raise RuntimeError("Picod failed to start")

    @classmethod
    def tearDownClass(cls):
        """Cleanup: stop picod, remove files"""
        logger.info("Tearing down test environment...")
        if cls.picod_process:
            cls.picod_process.terminate()
            cls.picod_process.wait()
        
        cls.cleanup_files()

    @classmethod
    def cleanup_files(cls):
        files_to_remove = [
            BOOTSTRAP_KEY_FILE, 
            "picod_public_key.pem", 
            "picod_client_keys.pem",
            "test_upload.txt",
            "remote_upload.txt",
            "test_download.txt",
            "remote_file.txt"
        ]
        for f in files_to_remove:
            if os.path.exists(f):
                os.remove(f)

    def mock_create_sandbox(self, *args, **kwargs):
        """Mock Sandbox creation - return a dummy ID"""
        return "test-sandbox-id"

    def mock_establish_tunnel(self, sandbox_id):
        """Mock tunnel establishment - No-op for Picod direct connection"""
        return None

    def mock_get_sandbox(self, sandbox_id):
        """Mock get_sandbox - simulate running status"""
        if sandbox_id == "test-sandbox-id":
            return {"status": "running", "id": sandbox_id}
        return None

    def mock_delete_sandbox(self, sandbox_id):
        """Mock delete_sandbox"""
        return True

    def _create_bootstrap_token(self, session_public_key_pem):
        """Helper to create JWT token signed by bootstrap key"""
        header = json.dumps({"alg": "RS256", "typ": "JWT"}).encode()
        claims = json.dumps({
            "session_public_key": session_public_key_pem,
            "iat": int(time.time()),
            "exp": int(time.time()) + 60
        }).encode()

        b64_header = base64.urlsafe_b64encode(header).rstrip(b"=")
        b64_claims = base64.urlsafe_b64encode(claims).rstrip(b"=")
        
        msg = b64_header + b"." + b64_claims
        signature = self.bootstrap_private_key.sign(msg, padding.PKCS1v15(), hashes.SHA256())
        b64_sig = base64.urlsafe_b64encode(signature).rstrip(b"=")
        
        return (msg + b"." + b64_sig).decode()

    @patch("agentcube.clients.client.SandboxClient.create_sandbox")
    @patch("agentcube.clients.client.SandboxClient.get_sandbox")
    @patch("agentcube.clients.client.SandboxClient.delete_sandbox")
    def test_full_lifecycle(self, mock_delete, mock_get, mock_create):
        """Test full SDK lifecycle: Init -> Run Code -> Files -> Stop"""
        
        # Setup Mocks for Control Plane (since we don't have real WorkloadManager)
        mock_create.side_effect = self.mock_create_sandbox
        mock_get.side_effect = self.mock_get_sandbox
        mock_delete.side_effect = self.mock_delete_sandbox
        
        logger.info("Testing CodeInterpreterClient Lifecycle...")

        # We need to manually initialize the Picod server because WorkloadManager isn't doing it
        # We hook into the client initialization to do this "Man-in-the-Middle" setup
        
        with CodeInterpreterClient(api_url=PICOD_URL) as client:
            # -- HACK: Manually Initialize Picod Server simulating WorkloadManager --
            # Get the public key the client generated
            client_pub_key = client._executor.key_pair.public_key.public_bytes(
                encoding=serialization.Encoding.PEM,
                format=serialization.PublicFormat.SubjectPublicKeyInfo
            ).decode()
            
            # Create token signed by bootstrap key
            init_token = self._create_bootstrap_token(client_pub_key)
            
            # Call Init API
            resp = requests.post(
                f"{PICOD_URL}/api/init", 
                headers={"Authorization": f"Bearer {init_token}"}
            )
            self.assertEqual(resp.status_code, 200, f"Picod Init Failed: {resp.text}")
            logger.info("Simulated WorkloadManager Init call successful.")
            # -- END HACK --

            # 1. Test execute_command
            logger.info("Test: execute_command")
            output = client.execute_command("echo 'Hello SDK'")
            self.assertEqual(output.strip(), "Hello SDK")

            # 2. Test run_code (Python)
            logger.info("Test: run_code (Python)")
            output = client.run_code("python", "print(1 + 1)")
            self.assertEqual(output.strip(), "2")
            
            # 3. Test run_code (Bash)
            logger.info("Test: run_code (Bash)")
            output = client.run_code("bash", "echo 'bash test'")
            self.assertEqual(output.strip(), "bash test")

            # 4. Test write_file
            logger.info("Test: write_file")
            client.write_file("test content", "remote_file.txt")
            # Verify with cat
            content = client.execute_command("cat remote_file.txt")
            self.assertEqual(content.strip(), "test content")

            # 5. Test upload_file (Multipart)
            logger.info("Test: upload_file (Multipart)")
            with open("test_upload.txt", "w") as f:
                f.write("upload content")
            client.upload_file("test_upload.txt", "remote_upload.txt")
            content = client.execute_command("cat remote_upload.txt")
            self.assertEqual(content.strip(), "upload content")

            # 6. Test download_file
            logger.info("Test: download_file")
            client.download_file("remote_upload.txt", "test_download.txt")
            with open("test_download.txt", "r") as f:
                content = f.read()
            self.assertEqual(content, "upload content")
            
            logger.info("Lifecycle test passed!")

        # 7. Verify Stop/Cleanup
        mock_delete.assert_called_once()
        logger.info("Cleanup verified.")

    @patch("agentcube.clients.client.SandboxClient.get_sandbox")
    def test_status_checks(self, mock_get):
        """Test is_running status check"""
        
        sandbox = Sandbox(api_url=PICOD_URL, skip_creation=True)
        sandbox.id = "test-sandbox-id" # Manually set ID
        
        # Case 1: Running
        mock_get.return_value = {"status": "running"}
        self.assertTrue(sandbox.is_running())
        
        # Case 2: Stopped
        mock_get.return_value = {"status": "stopped"}
        self.assertFalse(sandbox.is_running())

if __name__ == "__main__":
    unittest.main()
