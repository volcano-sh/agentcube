"""
Example usage of the Kubernetes Sandbox SDK
"""
import atexit
import subprocess
import time

from sandbox import SandboxSDK
from services.exceptions import SandboxError


def main():

    """Example usage of the SDK"""
    # Initialize the SDK
    sdk = SandboxSDK()
    
    # Example SSH key (in practice, you would load these from secure storage)
    public_key_path = "/root/.ssh/id_rsa.pub"
    private_key = "/root/.ssh/id_rsa"  # Or the key content as a string
    
    try:
        # Demonstrate testing capabilities
        print("\n---------------------------- Testing Capabilities ----------------------------")

        # Upload a file (you would need an actual file for this)
        print("\n---------------------------- Create Sandbox ----------------------------")
        # Create a sandbox
        print("Creating sandbox...")
        sandbox = sdk.create_sandbox(sandbox_id="example")
        print(f"Created sandbox: {sandbox}")

        print("\n--------------- Wait for SSH to be ready and hack configurations --------------")
        # Wait a moment for SSH to be fully ready
        time.sleep(10)

        # The container using local configuration cannot be connected using ssh
        port_forward_str = f"kubectl port-forward {sandbox.id} 2222:{sandbox.port} --address 0.0.0.0"
        port_forward_cmd = port_forward_str.split(" ")
        process = subprocess.Popen(port_forward_cmd, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        print(f"Command '{' '.join(port_forward_cmd)}' started in the background with PID: {process.pid}")

        # Register a cleanup function to kill the process when the main program exits
        def cleanup_background_process():
            if process and process.poll() is None:  # Check if the process is still running
                print(f"Terminating background process with PID: {process.pid}")
                try:
                    # Send SIGTERM first for graceful shutdown, then SIGKILL if needed
                    process.terminate()
                    process.wait(timeout=5)  # Wait for process to terminate
                    if process.poll() is None:
                        print(f"Background process did not terminate gracefully, killing PID: {process.pid}")
                        process.kill()
                except subprocess.TimeoutExpired:
                    print(f"Background process did not terminate gracefully, killing PID: {process.pid}")
                    process.kill()
                except Exception as e:
                    print(f"Error terminating background process: {e}")

        if process:
            atexit.register(cleanup_background_process)

        clean_known_hosts = 'ssh-keygen -f /root/.ssh/known_hosts -R [127.0.0.1]:2222'
        clean_known_hosts_cmd = clean_known_hosts.split(" ")
        result = subprocess.run(clean_known_hosts_cmd, capture_output=True, text=True, check=True)
        print(f"Command executed successfully: {result.stdout}")
        if result.stderr:
            print(f"Errors (if any): {result.stderr}")

        time.sleep(5)
        sandbox.ip_address = "127.0.0.1"
        sandbox.port = 2222
        print(f"Updated sandbox: {sandbox}")

        if process and process.poll() is None:  # Check if the process is still running
            print(f"Process is running: {process.pid}")
        
        # Upload a file (you would need an actual file for this)
        print("\n---------------------------- Uploading file ----------------------------")
        local_file_path="test_python.py"
        remote_file_path="/tmp/test_python.py"
        success = sdk.upload_file(
            sandbox=sandbox,
            private_key=private_key,
            local_file_path="test_python.py",
            remote_file_path="/tmp/test_python.py"
        )
        print(f"Upload {local_file_path} to {sandbox.id}:{remote_file_path} success: {success}")

        # Execute python code files
        print("\n---------------------------- Executing command ----------------------------")
        result = sdk.execute_command(
            sandbox,
            private_key=private_key,
            command=f"python3 {remote_file_path}"
        )
        print(f"Command result: {result}")
        
        # Download a file (you would need an actual file for this)
        print("\n---------------------------- Downloading file ----------------------------")
        success = sdk.download_file(
            sandbox=sandbox,
            private_key=private_key,
            remote_file_path="/tmp/test_python.py",
            local_file_path="downloaded_file.txt"
        )
        print(f"Download success: {success}")

        print(f"\n Downloaded file content:")
        with open('downloaded_file.txt', 'r') as f:
            print(f.read())
        
        # Delete the sandbox
        print("\n---------------------------- Shutting down sandbox ----------------------------")
        success = sdk.delete_sandbox(sandbox)
        print(f"Shutdown success: {success}")
        
    except SandboxError as e:
        print(f"Sandbox error: {e}")
    except Exception as e:
        print(f"Unexpected error: {e}")


if __name__ == "__main__":
    main()
