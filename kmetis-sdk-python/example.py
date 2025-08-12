"""
Example usage of the Kubernetes Sandbox SDK
"""
import time
from sandbox import Sandbox
from exceptions import SandboxError


def main():
    """Example usage of the SDK"""
    # Initialize the SDK
    sdk = Sandbox(namespace="default")
    
    # Example SSH key (in practice, you would load these from secure storage)
    public_key = "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQDexample your_email@example.com"
    private_key = "/home/qi/picosdk/id_rsa"  # Or the key content as a string
    
    try:
        # Create a sandbox
        # print("Creating sandbox...")
        # sandbox_info = sdk.create_sandbox(public_key=public_key)
        # sandbox_id = sandbox_info["sandbox_id"]
        # print(f"Created sandbox: {sandbox_info}")
        
        # Wait a moment for SSH to be fully ready
        # time.sleep(10)

                
        # Example of updating IP address in cache for testing
        # print("\nUpdating IP address in cache...")
        # success = sdk.update_sandbox_ip("sandbox-1754382392", "127.0.0.1")
        # if success:
        #     print("Updated IP address in cache")
        # else:
        #     print("Failed to update IP address (sandbox not in cache)")

         # Example of manually adding a sandbox to cache for testing
        sandbox_id = "sandbox-1754382392"
        print("\nAdding test sandbox to cache...")
        sdk.add_sandbox_to_cache(
            sandbox_id=sandbox_id,
            ip_address="127.0.0.1",
            port="2222",
            created_at="2023-01-01T00:00:00"
        )
        print("Added test sandbox to cache")
        # Execute a command
        print("\nExecuting command...")
        result = sdk.execute_command(
            sandbox_id=sandbox_id,
            private_key=private_key,
            command="echo 'Hello from sandbox!'"
        )
        print(f"Command result: {result}")
        
        # Get sandbox info
        # print("\nGetting sandbox info...")
        # info = sdk.get_sandbox_info(sandbox_id)
        # print(f"Sandbox info: {info}")
        
        # Demonstrate testing capabilities
        # print("\n--- Testing Capabilities ---")

        # Example of executing command on cached sandbox (for testing)
        # Note: This would normally fail in a real environment without an actual sandbox
        # print("\nTo test execute_command with cached sandbox:")
        # print("1. Add sandbox to cache with add_sandbox_to_cache()")
        # print("2. Use execute_command() with the same sandbox_id")
        # print("3. Provide a valid private key for SSH connection")
        
        # Upload a file (you would need an actual file for this)
        # print("\nUploading file...")
        # success = sdk.upload_file(
        #     sandbox_id=sandbox_id,
        #     private_key=private_key,
        #     local_path="local_file.txt",
        #     remote_path="/tmp/remote_file.txt"
        # )
        # print(f"Upload success: {success}")
        
        # Download a file (you would need an actual file for this)
        # print("\nDownloading file...")
        # success = sdk.download_file(
        #     sandbox_id=sandbox_id,
        #     private_key=private_key,
        #     remote_path="/tmp/remote_file.txt",
        #     local_path="downloaded_file.txt"
        # )
        # print(f"Download success: {success}")
        
        # Shutdown the sandbox
        # print("\nShutting down sandbox...")
        # success = sdk.shutdown_sandbox(sandbox_id)
        # print(f"Shutdown success: {success}")
        
    except SandboxError as e:
        print(f"Sandbox error: {e}")
    except Exception as e:
        print(f"Unexpected error: {e}")


if __name__ == "__main__":
    main()
