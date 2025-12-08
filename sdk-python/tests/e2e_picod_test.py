import os
import time
import subprocess
import requests
import jwt
import base64
import logging
from cryptography.hazmat.primitives import serialization
from cryptography.hazmat.primitives.asymmetric import rsa
from cryptography.hazmat.backends import default_backend

from agentcube.clients.data_plane import DataPlaneClient

# Configure Logging
logging.basicConfig(level=logging.INFO)
logger = logging.getLogger("sdk_e2e_test")

# --- Constants ---
IMAGE_NAME = "picod-test:latest"
CONTAINER_NAME = "picod_e2e_test"
HOST_PORT = 8080
BOOTSTRAP_KEY_FILE = os.path.abspath("bootstrap_public.pem")

# --- Helper Classes ---

class DirectDataPlaneClient(DataPlaneClient):
    """
    Subclass of DataPlaneClient that connects directly to PicoD,
    bypassing the Router URL construction logic.
    """
    def __init__(self, picod_url, session_id, private_key, timeout=30):
        # We skip super().__init__ because it enforces Router URL logic.
        # Instead we replicate the necessary parts.
        
        self.session_id = session_id
        self.private_key = private_key
        self.timeout = timeout
        self.logger = logging.getLogger(f"{__name__}.DirectDataPlaneClient")
        
        # Point directly to PicoD root (e.g., http://localhost:8080/)
        self.base_url = picod_url if picod_url.endswith("/") else picod_url + "/"
        
        self.session = requests.Session()
        # PicoD doesn't strictly require x-agentcube-session-id if bypassing Router,
        # but it doesn't hurt.
        self.session.headers.update({
            "x-agentcube-session-id": self.session_id
        })

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

def start_picod_container():
    """Start the PicoD docker container with mounted key."""
    logger.info(f"Starting Docker container {CONTAINER_NAME}...")
    
    # Remove existing if any
    subprocess.run(["docker", "rm", "-f", CONTAINER_NAME], capture_output=True)
    
    cmd = [
        "docker", "run", "-d",
        "--name", CONTAINER_NAME,
        "-p", f"{HOST_PORT}:8080",
        "-v", f"{BOOTSTRAP_KEY_FILE}:/etc/picod/public-key.pem",
        IMAGE_NAME,
        "-bootstrap-key", "/etc/picod/public-key.pem"
    ]
    
    logger.info(f"Running: {' '.join(cmd)}")
    result = subprocess.run(cmd, capture_output=True, text=True)
    if result.returncode != 0:
        raise RuntimeError(f"Failed to start container: {result.stderr}")
        
    # Wait for health check
    url = f"http://localhost:{HOST_PORT}/health"
    retries = 10
    for i in range(retries):
        try:
            resp = requests.get(url, timeout=1)
            if resp.status_code == 200:
                logger.info("PicoD is up and running!")
                return
        except Exception:
            pass
        logger.info("Waiting for PicoD...")
        time.sleep(1)
        
    raise RuntimeError("PicoD failed to start or is unhealthy")

def stop_picod_container():
    """Stop the Docker container."""
    logger.info(f"Stopping container {CONTAINER_NAME}...")
    subprocess.run(["docker", "rm", "-f", CONTAINER_NAME], capture_output=True)

def perform_init_handshake(bootstrap_priv_key, session_pub_pem):
    """
    Simulate the WorkloadManager performing the Init handshake.
    Sign a token with Bootstrap Private Key containing Session Public Key.
    """
    logger.info("Performing Init Handshake...")
    
    # Create JWT
    now = int(time.time())
    # Picod expects Base64 encoded (Raw, no padding) public key
    session_pub_b64 = base64.b64encode(session_pub_pem).decode('utf-8').rstrip("=")
    
    claims = {
        "iss": "workload-manager-sim",
        "iat": now,
        "exp": now + 60,
        "session_public_key": session_pub_b64
    }
    
    token = jwt.encode(
        payload=claims,
        key=bootstrap_priv_key,
        algorithm="RS256"
    )
    
    url = f"http://localhost:{HOST_PORT}/init"
    headers = {
        "Authorization": f"Bearer {token}"
    }
    
    resp = requests.post(url, headers=headers)
    if resp.status_code != 200:
        raise RuntimeError(f"Init failed: {resp.status_code} {resp.text}")
        
    logger.info("Init Handshake Successful!")

# --- Main Test ---

def main():
    try:
        # 1. Generate Bootstrap Keys (Simulating Infrastructure Setup)
        logger.info("Generating Bootstrap Keys...")
        bootstrap_priv, bootstrap_priv_pem, bootstrap_pub_pem = generate_key_pair()
        
        # Save Public Key for Docker mount
        with open(BOOTSTRAP_KEY_FILE, "wb") as f:
            f.write(bootstrap_pub_pem)
            
        # 2. Start PicoD
        start_picod_container()
        
        # 3. Generate Session Keys (Simulating Client/SDK)
        logger.info("Generating Session Keys...")
        session_priv, session_priv_pem, session_pub_pem = generate_key_pair()
        session_id = "test-session-123"
        
        # 4. Perform Handshake (Simulating WM)
        perform_init_handshake(bootstrap_priv, session_pub_pem)
        
        # 5. Initialize SDK Client (Direct Mode)
        logger.info("Initializing DirectDataPlaneClient...")
        client = DirectDataPlaneClient(
            picod_url=f"http://localhost:{HOST_PORT}",
            session_id=session_id,
            private_key=session_priv
        )
        
        # 6. Run Tests
        logger.info(">>> TEST: Execute Command")
        output = client.execute_command("echo 'Hello SDK'")
        print(f"Output: {output.strip()}")
        assert output.strip() == "Hello SDK"

        logger.info(">>> TEST: Execute Command with List Arguments")
        output_list_cmd = client.execute_command(["echo", "Hello from list args"])
        print(f"Output (list args): {output_list_cmd.strip()}")
        assert output_list_cmd.strip() == "Hello from list args"
        
        logger.info(">>> TEST: Run Python Code")
        code = "print(10 + 20)"
        output = client.run_code("python", code)
        print(f"Output: {output.strip()}")
        assert output.strip() == "30"
        
        logger.info(">>> TEST: File Upload & Download")
        test_content = "This is a test file."
        client.write_file(test_content, "test.txt")
        
        # Verify with cat
        output = client.execute_command("cat test.txt")
        assert output.strip() == test_content
        
        # Verify with download
        client.download_file("test.txt", "downloaded_test.txt")
        with open("downloaded_test.txt", "r") as f:
            content = f.read()
        assert content == test_content

        logger.info(">>> TEST: File Upload (Multipart)")
        local_multipart = "local_multipart.txt"
        multipart_content = "This is multipart content."
        with open(local_multipart, "w") as f:
            f.write(multipart_content)
        
        try:
            # Test upload_file (multipart)
            client.upload_file(local_multipart, "remote_multipart.txt")
            
            # Verify upload with cat
            output = client.execute_command("cat remote_multipart.txt")
            assert output.strip() == multipart_content
        finally:
             if os.path.exists(local_multipart):
                 os.remove(local_multipart)

        logger.info(">>> TEST: List Files")
        files = client.list_files(".")
        # We expect at least test.txt (from previous test) and remote_multipart.txt
        filenames = [f['name'] for f in files]
        logger.info(f"Files found: {filenames}")
        assert "test.txt" in filenames
        assert "remote_multipart.txt" in filenames
        
        # Verify file info structure
        test_file = next(f for f in files if f['name'] == 'test.txt')
        assert test_file['size'] > 0
        assert not test_file['is_dir']

        logger.info(">>> TEST: Command Failure")
        try:
            client.execute_command("ls /nonexistent_directory_for_test")
            assert False, "Command should have failed"
        except Exception as e:
            logger.info(f"Caught expected error: {e}")
            assert "exit" in str(e)
            
        logger.info(">>> TEST: Timeout")
        # Should pass with sufficient timeout
        client.execute_command("sleep 0.1", timeout=1.0)
        
        try:
             # Should fail with short timeout
             # Note: We rely on the SDK client to pass "0.1s" to PicoD
             client.execute_command("sleep 2", timeout=0.1)
             assert False, "Command should have timed out"
        except Exception as e:
             logger.info(f"Caught expected timeout error: {e}")
             # PicoD returns exit code 124 for timeout
             assert "124" in str(e)

        logger.info(">>> ALL TESTS PASSED SUCCESSFULLY! <<<")
        
    except Exception as e:
        logger.error(f"Test Failed: {e}")
        # Print container logs on failure
        logs = subprocess.run(["docker", "logs", CONTAINER_NAME], capture_output=True, text=True)
        print("--- Container Logs ---")
        print(logs.stderr)
        print(logs.stdout)
        raise
        
    finally:
        # Cleanup
        stop_picod_container()
        if os.path.exists(BOOTSTRAP_KEY_FILE):
            os.remove(BOOTSTRAP_KEY_FILE)
        if os.path.exists("downloaded_test.txt"):
            os.remove("downloaded_test.txt")

if __name__ == "__main__":
    main()
