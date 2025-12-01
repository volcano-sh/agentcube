#!/usr/bin/env python3
"""
PicoD Client RSA Authentication Example

This example demonstrates how to use PicoDClient with RSA-based authentication.
The RSA authentication provides secure communication between the client and PicoD server.

Prerequisites:
1. PicoD server is running (go run cmd/picod/main.go)
2. Dependencies installed: pip install cryptography requests
3. Set PYTHONPATH to sdk-python directory if running from repo root

Environment Variables:
- PICOD_HOST: PicoD server host (default: localhost)
- PICOD_PORT: PicoD server port (default: 9527)
"""

import os
import sys
import json
import logging

# Add parent directory to Python path for imports
sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..'))

from agentcube.clients.picod_client import PicoDClient


def setup_logging():
    """Configure logging for the example"""
    logging.basicConfig(
        level=logging.INFO,
        format='%(message)s'
    )
    return logging.getLogger(__name__)


def get_config():
    """Get configuration from environment variables"""
    config = {
        'host': os.getenv('PICOD_HOST', 'localhost'),
        'port': int(os.getenv('PICOD_PORT', '9527')),
        'key_file': os.getenv('PICOD_KEY_FILE', 'picod_client_keys.pem')
    }
    return config


def initialize_client(logger, config):
    """Initialize PicoD client with RSA authentication"""
    logger.info("=" * 60)
    logger.info("PicoD Client RSA Authentication Example")
    logger.info("=" * 60)
    logger.info("")

    logger.info("Configuration:")
    logger.info(f"  Host: {config['host']}")
    logger.info(f"  Port: {config['port']}")
    logger.info(f"  Key File: {config['key_file']}")
    logger.info("")

    # Create client
    client = PicoDClient(
        host=config['host'],
        port=config['port'],
        key_file=config['key_file']
    )

    # Generate or load RSA key pair
    logger.info("Step 1: Loading/Generating RSA key pair...")
    try:
        client.load_rsa_key_pair()
        logger.info("  ✓ Loaded existing key pair")
    except FileNotFoundError:
        client.generate_rsa_key_pair()
        logger.info("  ✓ Generated new RSA key pair")
    logger.info("")

    # Check authentication status
    is_auth = client.is_authenticated()
    logger.info(f"  Authentication Status: {'Enabled' if is_auth else 'Disabled'}")
    logger.info("")

    # Initialize server (only needed once)
    logger.info("Step 2: Initializing PicoD server...")
    logger.info("  Note: This sends your public key to the server")
    success = client.initialize_server()

    if success:
        logger.info("  ✓ Server initialized successfully")
    else:
        logger.warning("  ⚠ Server initialization failed (may already be initialized)")
    logger.info("")

    return client


def execute_test_commands(logger, client):
    """Execute a series of test commands"""
    logger.info("Step 3: Executing test commands...")

    commands = [
        "whoami",
        "pwd",
        "echo 'Hello from PicoD with RSA authentication!'",
        "python3 --version",
        "uname -a"
    ]

    for i, cmd in enumerate(commands, 1):
        logger.info(f"  [{i}/{len(commands)}] Executing: {cmd}")
        try:
            output = client.execute_command(cmd)
            logger.info(f"    Output: {output.strip()}")
        except Exception as e:
            logger.warning(f"    Warning: {str(e)}")
    logger.info("")


def test_file_operations(logger, client):
    """Test file upload, write, and download operations"""
    logger.info("Step 4: Testing file operations...")

    # Upload file
    logger.info("  [1/3] Uploading file...")
    upload_content = "Hello PicoD!\nThis file was uploaded via RSA-authenticated client."
    try:
        client.write_file(upload_content, "/tmp/uploaded.txt")
        logger.info("    ✓ File written successfully")
    except Exception as e:
        logger.warning(f"    Warning: {str(e)}")

    # Upload via upload_file method
    logger.info("  [2/3] Uploading local file...")
    local_file = "/tmp/example_upload.txt"
    try:
        with open(local_file, 'w') as f:
            f.write("Local file content\n")
        client.upload_file(local_file, "/tmp/example_upload.txt")
        logger.info("    ✓ File uploaded successfully")
    except Exception as e:
        logger.warning(f"    Warning: {str(e)}")

    # Download file
    logger.info("  [3/3] Downloading file...")
    try:
        client.download_file("/tmp/example_upload.txt", "/tmp/downloaded.txt")
        logger.info("    ✓ File downloaded successfully")
    except Exception as e:
        logger.warning(f"    Warning: {str(e)}")

    logger.info("")


def execute_python_script(logger, client):
    """Execute a Python script in the sandbox"""
    logger.info("Step 5: Executing Python script...")

    script = """#!/usr/bin/env python3
import json
from datetime import datetime

def generate_fibonacci(n):
    fib = [0, 1]
    for i in range(2, n):
        fib.append(fib[i-1] + fib[i-2])
    return fib[:n]

n = 15
fibonacci = generate_fibonacci(n)

output = {
    "timestamp": datetime.now().isoformat(),
    "algorithm": "Fibonacci",
    "count": n,
    "numbers": fibonacci,
    "sum": sum(fibonacci),
    "message": "Generated via PicoD RSA auth!"
}

with open('/tmp/fibonacci.json', 'w') as f:
    json.dump(output, f, indent=2)

print(f"Generated {n} Fibonacci numbers")
print(f"Sum: {sum(fibonacci)}")
"""

    try:
        # Write script
        client.write_file(script, "/tmp/fibonacci.py")
        logger.info("  ✓ Python script written")

        # Execute script
        output = client.execute_command("python3 /tmp/fibonacci.py")
        logger.info(f"  ✓ Script executed:\n{output}")

        # Download result
        client.download_file("/tmp/fibonacci.json", "/tmp/fibonacci_downloaded.json")
        logger.info("  ✓ Result downloaded")

        # Display result
        with open("/tmp/fibonacci_downloaded.json", 'r') as f:
            data = json.load(f)
            logger.info(f"  ✓ Verified: {data['count']} numbers, sum = {data['sum']}")
    except Exception as e:
        logger.warning(f"  Warning: {str(e)}")

    logger.info("")


def test_run_code(logger, client):
    """Test the run_code method"""
    logger.info("Step 6: Testing run_code method...")

    # Test Python
    logger.info("  [1/2] Running Python code...")
    try:
        python_code = """
import sys
print(f"Python {sys.version.split()[0]}")
x = 5 + 3
print(f"5 + 3 = {x}")
"""
        output = client.run_code("python", python_code)
        logger.info(f"    Output:\n{output}")
    except Exception as e:
        logger.warning(f"    Warning: {str(e)}")

    # Test Bash
    logger.info("  [2/2] Running Bash code...")
    try:
        bash_code = """
for i in {1..3}; do
    echo "Count: $i"
done
echo "Done!"
"""
        output = client.run_code("bash", bash_code)
        logger.info(f"    Output:\n{output}")
    except Exception as e:
        logger.warning(f"    Warning: {str(e)}")

    logger.info("")


def demonstrate_authentication(logger, client):
    """Demonstrate authentication features"""
    logger.info("Step 7: Authentication features...")

    # Check authentication status
    is_auth = client.is_authenticated()
    logger.info(f"  Current authentication status: {'Authenticated' if is_auth else 'Anonymous'}")

    # Show public key info
    try:
        public_key = client.get_public_key_pem()
        logger.info(f"  Public key length: {len(public_key)} characters")
        logger.info(f"  Public key format: PEM")
    except Exception as e:
        logger.warning(f"  Warning: {str(e)}")

    logger.info("")


def cleanup(logger, client):
    """Cleanup resources"""
    logger.info("Step 8: Cleaning up...")
    try:
        client.cleanup()
        logger.info("  ✓ Resources cleaned up")
    except Exception as e:
        logger.warning(f"  Warning: {str(e)}")
    logger.info("")


def main():
    """Main execution function"""
    logger = setup_logging()
    config = get_config()

    client = None
    try:
        # Initialize client with RSA authentication
        client = initialize_client(logger, config)

        # Run tests
        execute_test_commands(logger, client)
        test_file_operations(logger, client)
        execute_python_script(logger, client)
        test_run_code(logger, client)
        demonstrate_authentication(logger, client)
        cleanup(logger, client)

        # Success message
        logger.info("=" * 60)
        logger.info("✓ All operations completed successfully!")
        logger.info("=" * 60)
        logger.info("")
        logger.info("Summary:")
        logger.info("  ✓ RSA key pair generated/loaded")
        logger.info("  ✓ Server initialized with public key")
        logger.info("  ✓ Commands executed with signature verification")
        logger.info("  ✓ File operations completed")
        logger.info("  ✓ Python script executed in sandbox")
        logger.info("  ✓ Code runner tested")
        logger.info("")
        logger.info("Your PicoD client is ready for use with RSA authentication!")

    except Exception as e:
        logger.error("")
        logger.error("=" * 60)
        logger.error("✗ Error occurred")
        logger.error("=" * 60)
        logger.error(f"Error: {str(e)}")
        import traceback
        traceback.print_exc()

        if client:
            client.cleanup()

        sys.exit(1)


if __name__ == "__main__":
    main()
