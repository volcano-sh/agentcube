import logging
import json
import os

from agentcube import CodeInterpreterClient

def main():
    # Configure logging
    logging.basicConfig(level=logging.INFO, format='%(message)s')
    log = logging.getLogger(__name__)
    
    try:
        log.info("===========================================")
        log.info("AgentCube Code Interpreter Test")
        log.info("===========================================\n")
        
        # Initialize code interpreter using Context Manager
        # The session is now started directly in CodeInterpreterClient()'s __init__ method
        log.info("Step 1: Creating Code Interpreter Session...")
        
        with CodeInterpreterClient() as code_interpreter:
            log.info(f"✅ Session created: {code_interpreter.session_id}\n")

            # 2. Execute test commands
            log.info("Step 2: Executing test commands...")
            commands = [
                "whoami",
                "pwd",
                "echo 'Hello from AgentCube!'",
                "python --version",
                "uname -a"
            ]
            
            for i, cmd in enumerate(commands, 1):
                log.info(f"   [{i}/{len(commands)}] Executing: {cmd}")
                output = code_interpreter.execute_command(cmd)
                log.info(f"      Output: {output.strip()}\n")
            
            # 3. Upload File to remote
            log.info("Step 3: Uploading File...")
            upload_path = "/tmp/upload.txt"
            with open(upload_path, "w", encoding="utf-8") as f:
                f.write("Hello Upload File")

            code_interpreter.upload_file(upload_path, "/workspace/upload.txt")
            log.info("✅ Uploaded file to /workspace/upload.txt\n")

            # 4. Write Python script
            log.info("Step 4: Writing Python script...")
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
            code_interpreter.write_file(script_content, "/workspace/fib.py")
            log.info("✅ Write Content to /workspace/fib.py\n")
            
            # 5. Execute script
            log.info("Step 5: Executing Python script...")
            output = code_interpreter.execute_command("python3 /workspace/fib.py")
            log.info(f"   Output: {output.strip()}\n")
            
            # 6. Download result file
            log.info("Step 6: Downloading output file...")
            local_path = "/tmp/pico_output.json"
            code_interpreter.download_file("/workspace/output.json", local_path)
            code_interpreter.download_file("/workspace/upload.txt", "/tmp/download.txt")
            log.info(f"✅ File downloaded to {local_path}\n")
            
            # 7. Verify result
            log.info("Step 7: Verifying output...")
            with open(local_path, 'r') as f:
                data = json.load(f)
            log.info(f"   Generated {data['count']} numbers, sum: {data['sum']}")

            # 8. Run Code
            log.info("\nStep 8: Running code in sandbox...")
            stdout = code_interpreter.run_code(
                language="py",
                code="print('Hello from Python!'); import sys; print('Python version:', sys.version.split()[0])"
            )
            print(f"✅ Python Output: {stdout}")

        log.info("\n===========================================")
        log.info("🎉 All operations completed successfully")
        log.info("===========================================")
        
    except Exception as e:
        log.error(f"\n❌ Error: {str(e)}")

if __name__ == "__main__":
    # Ensure env vars are set or defaults will be used
    if not os.getenv("WORKLOADMANAGER_URL") and not os.getenv("ROUTER_URL"):
        print("Tip: Set WORKLOADMANAGER_URL and ROUTER_URL environment variables if not running against localhost.")
    main()