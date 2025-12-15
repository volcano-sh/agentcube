from agentcube import CodeInterpreterClient

def main():
    """
    This example demonstrates the basic usage of the AgentCube Python SDK.
    It requires a running AgentCube environment.
    
    Ensure the following environment variables are set before running:
    - WORKLOAD_MANAGER_URL: URL of the WorkloadManager service
    - ROUTER_URL: URL of the Router service
    - API_TOKEN: (Optional) Authentication token if required
    """
    
    # specific configuration can be passed directly or via environment variables
    # workload_manager_url = os.getenv("WORKLOAD_MANAGER_URL", "http://localhost:8080")
    # router_url = os.getenv("ROUTER_URL", "http://localhost:8080")

    print("Initializing AgentCube Client...")
    
    try:
        # Using context manager ensures the session is cleaned up (deleted) after use
        with CodeInterpreterClient(verbose=True) as client:
            print(f"Session created successfully! Session ID: {client.session_id}")

            # 1. Execute a simple Shell Command
            print("\n--- 1. Shell Command: whoami ---")
            output = client.execute_command("whoami")
            print(f"Result: {output.strip()}")

            print("\n--- 2. Shell Command: Check OS release ---")
            output = client.execute_command("cat /etc/os-release")
            print(f"Result:\n{output.strip()}")

            # 2. Execute Python Code
            print("\n--- 3. Python Code: Calculate Pi ---")
            code = """
                import math
                print(f"Pi is approximately {math.pi:.6f}")
            """
            output = client.run_code("python", code)
            print(f"Result: {output.strip()}")

            # 3. File Operations
            print("\n--- 4. File Operations ---")
            
            # Write a file to the remote sandbox
            remote_filename = "hello_agentcube.txt"
            content = "Hello from AgentCube SDK Example!"
            print(f"Writing to '{remote_filename}'...")
            client.write_file(content, remote_filename)
            
            # Verify file creation by listing files
            print("Listing files in current directory...")
            files = client.list_files(".")
            for f in files:
                print(f" - {f['name']} ({f['size']} bytes)")
                
            # Read the file back (using cat for simplicity)
            print(f"Reading '{remote_filename}' content...")
            output = client.execute_command(f"cat {remote_filename}")
            print(f"File content: {output.strip()}")

    except Exception as e:
        print(f"\nAn error occurred: {e}")
        # Note: If an exception occurs within the 'with' block, 
        # the __exit__ method is still called, ensuring cleanup.

if __name__ == "__main__":
    main()
