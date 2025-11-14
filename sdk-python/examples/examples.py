import logging
import json

from agentcube import CodeInterpreterClient

def main():
    # Configure logging
    logging.basicConfig(level=logging.INFO, format='%(message)s')
    log = logging.getLogger(__name__)
    
    try:
        # Initialize code interpreter
        
        log.info("===========================================")
        log.info("SSH Key-based Authentication Test")
        log.info("===========================================\n")
        
        # 1. Create sandbox with SSH key
        log.info("Step 1: Creating sandbox with SSH key...")
        code_interpreter = CodeInterpreterClient()
        log.info(f"✅ sandbox created: {code_interpreter.id}\n")

        # 2. get sandbox info
        log.info("Step 2: Retrieving sandbox info...")
        sandbox_info = code_interpreter.get_info()
        log.info(f"   sandbox Info: {json.dumps(sandbox_info, indent=2)}\n")

        # 3. Execute test commands
        log.info("Step 3: Executing test commands...")
        commands = [
            "whoami",
            "pwd",
            "echo 'Hello from PicoClient!'",
            "python --version",
            "uname -a"
        ]
        
        for i, cmd in enumerate(commands, 1):
            log.info(f"   [{i}/{len(commands)}] Executing: {cmd}")
            output = code_interpreter.execute_command(cmd)
            log.info(f"      Output: {output.strip()}\n")
        
        # 4. Upload File to remote
        log.info("Step 4: Uploading File...")
        with open("/tmp/upload.txt", "w", encoding="utf-8") as f:
            f.write("Hello Upload File")

        code_interpreter.upload_file("/tmp/upload.txt", "/workspace/upload.txt")
        log.info("✅ Uploaded file to /workspace/upload.txt\n")

        # 5. Write Python script
        log.info("Step 5: Writing Python script...")
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
        
        # 6. Execute script
        log.info("Step 6: Executing Python script...")
        output = code_interpreter.execute_command("python3 /workspace/fib.py")
        log.info(f"   Output: {output.strip()}\n")
        
        # 7. Download result file
        log.info("Step 7: Downloading output file...")
        local_path = "/tmp/pico_output.json"
        code_interpreter.download_file("/workspace/output.json", local_path)
        code_interpreter.download_file("/workspace/upload.txt", "/tmp/download.txt")
        log.info(f"✅ File downloaded to {local_path}\n")
        
        # 8. Verify result
        log.info("Step 8: Verifying output...")
        with open(local_path, 'r') as f:
            data = json.load(f)
        log.info(f"   Generated {data['count']} numbers, sum: {data['sum']}")

        # 9. Run Code
        log.info("\nStep 9: Running code in sandbox...")
        stdout = code_interpreter.run_code(
            language="py",
            code="print('Hello from Python!'); import sys; print('Python version:', sys.version.split()[0])"
        )
        print(f"✅ Python Output: {stdout}")

        # 10. Stop sandbox
        log.info("\nStep 10: Stop sandbox...")
        if code_interpreter.stop():
            log.info(f"✅ Sandbox {code_interpreter.id} deleted\n")
        else:
            log.error(f"❌ Failed to delete sandbox {code_interpreter.id}\n")
        
        log.info("\n===========================================")
        log.info("🎉 All operations completed successfully")
        log.info("===========================================")
        
    except Exception as e:
        log.error(f"\n❌ Error: {str(e)}")

if __name__ == "__main__":
    main()