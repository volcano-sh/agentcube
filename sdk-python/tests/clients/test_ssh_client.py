import logging
import json
from pathlib import Path

from agentcube.clients.client import SandboxClient
from agentcube.clients.ssh_client import SandboxSSHClient

def main():
    # Configure logging
    logging.basicConfig(level=logging.INFO, format='%(message)s')
    log = logging.getLogger(__name__)
    
    try:
        # Initialize ssh client
        log.info("===========================================")
        log.info("SSH Key-based Authentication Test")
        log.info("===========================================\n")
        
        # 1. Create session with SSH key
        log.info("Step 1: Creating session with SSH key...")
        client = SandboxClient()
        public_key, private_key = SandboxSSHClient.generate_ssh_key_pair()
        sandbox_id = client.create_sandbox(ssh_public_key=public_key)
        log.info(f"✅ Session created: {sandbox_id}\n")
        
        # 2. Establish tunnel
        log.info("Step 2: Establishing HTTP tunnel...")
        sock = client.establish_tunnel(sandbox_id)
        log.info("✅ Tunnel established\n")

        # 3. Establish SSH connection
        log.info("Step 3: Connecting via SSH...")
        ssh_client = SandboxSSHClient(private_key=private_key, tunnel_sock=sock)
        log.info("✅ SSH connection established\n")

        # 4. Execute test commands
        log.info("Step 4: Executing test commands...")
        commands = [
            "whoami",
            "pwd",
            "echo 'Hello from PicoClient!'",
            "python --version",
            "uname -a"
        ]
        
        for i, cmd in enumerate(commands, 1):
            log.info(f"   [{i}/{len(commands)}] Executing: {cmd}")
            output = ssh_client.execute_command(cmd)
            log.info(f"      Output: {output.strip()}\n")
        
        # 5. Upload File to remote
        log.info("Step 5: Uploading Empty File...")
        with open("/tmp/upload.txt", "w", encoding="utf-8") as f:
            f.write("Hello Upload File")

        ssh_client.upload_file("/tmp/upload.txt", "/workspace/upload.txt")
        log.info("✅ Uploaded file to /workspace/upload.txt\n")


        # 6. Write Python script to remote
        log.info("Step 6: Write Python script to remote...")
        script_content = """#!/usr/bin/env python3
import json
from datetime import datetime

def generate_fibonacci(n):
    fib = [0, 1]
    for i in range(2, n):
        fib.append(fib[i-1] + fib[i-2])
    return fib[:n]

n = 20
fib = generate_fibonacci(n)
with open('/workspace/output.json', 'w') as f:
    json.dump({
        "timestamp": datetime.now().isoformat(),
        "count": n,
        "numbers": fib,
        "sum": sum(fib)
    }, f, indent=2)
print(f"Generated {n} Fibonacci numbers")
"""
        ssh_client.write_file(script_content, "/workspace/fib.py")
        log.info("✅ content write to /workspace/fib.py\n")

        # 7. Execute script
        log.info("Step 7: Executing Python script...")
        output = ssh_client.execute_command("python3 /workspace/fib.py")
        log.info(f"   Output: {output.strip()}\n")
        
        # 8. Download result file
        log.info("Step 8: Downloading output file...")
        local_path = "/tmp/pico_output.json"
        ssh_client.download_file("/workspace/output.json", local_path)
        ssh_client.download_file("/workspace/upload.txt", "/tmp/download.txt")
        log.info(f"✅ File downloaded to {local_path}\n")
        

        # 9. Verify result
        log.info("Step 9: Verifying output...")
        with open(local_path, 'r') as f:
            data = json.load(f)
        log.info(f"   Generated {data['count']} numbers, sum: {data['sum']}")
        
        log.info("\n===========================================")
        log.info("🎉 All operations completed successfully")
        log.info("===========================================")
         
    except Exception as e:
        log.error(f"\n❌ Error: {str(e)}")
    finally:
        ssh_client.cleanup()
        client.delete_sandbox(sandbox_id)

if __name__ == "__main__":
    main()