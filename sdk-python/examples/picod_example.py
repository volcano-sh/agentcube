"""
PicoD REST API Client 高层测试示例

这个示例通过 PicoDClient SDK 测试 PicoD 服务，
模拟 examples.py 的测试流程，使用 PicoDClient 替代 SSHClient。

使用前提：
1. PicoD 服务器正在运行
2. 已安装依赖：pip install requests
3. 设置环境变量或在代码中配置访问令牌
"""

import logging
import json
import os
import sys

# 添加父目录到 Python 路径
sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..'))

from agentcube.clients.picod_client import PicoDClient


def main():
    # 配置日志
    logging.basicConfig(level=logging.INFO, format='%(message)s')
    log = logging.getLogger(__name__)
    
    # 配置连接参数
    HOST = os.getenv("PICOD_HOST", "localhost")
    PORT = int(os.getenv("PICOD_PORT", "9527"))
    ACCESS_TOKEN = os.getenv("PICOD_ACCESS_TOKEN", "")
    
    try:
        log.info("===========================================")
        log.info("PicoD Client SDK Test (High-Level)")
        log.info("===========================================\n")
        
        # 初始化 PicoD 客户端
        log.info(f"Initializing PicoD client...")
        log.info(f"   Host: {HOST}")
        log.info(f"   Port: {PORT}")
        if ACCESS_TOKEN:
            log.info(f"   Token: {ACCESS_TOKEN[:10]}...\n")
        else:
            log.info("   Token: (none - running without auth)\n")
        
        client = PicoDClient(
            host=HOST,
            port=PORT,
            access_token=ACCESS_TOKEN
        )
        log.info("✅ PicoD client initialized\n")
        
        # Step 1: 执行测试命令
        log.info("Step 1: Executing test commands...")
        commands = [
            "whoami",
            "pwd",
            "echo 'Hello from PicoClient!'",
            "python3 --version",
            "uname -a"
        ]
        
        for i, cmd in enumerate(commands, 1):
            log.info(f"   [{i}/{len(commands)}] Executing: {cmd}")
            output = client.execute_command(cmd)
            log.info(f"      Output: {output.strip()}\n")
        
        # Step 2: 上传文件到远程
        log.info("Step 2: Uploading File...")
        upload_content = "Hello Upload File\nThis file was uploaded via PicoD REST API"
        with open("/tmp/upload.txt", "w", encoding="utf-8") as f:
            f.write(upload_content)
        
        client.upload_file("/tmp/upload.txt", "/workspace/upload.txt")
        log.info("✅ Uploaded file to /workspace/upload.txt\n")
        
        # Step 3: 验证上传的文件
        log.info("Step 3: Verifying uploaded file...")
        output = client.execute_command("cat /workspace/upload.txt")
        log.info(f"   File content: {output.strip()}\n")
        
        # Step 4: 写入 Python 脚本
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
        client.write_file(script_content, "/workspace/fib.py")
        log.info("✅ Write Content to /workspace/fib.py\n")
        
        # Step 5: 执行脚本
        log.info("Step 5: Executing Python script...")
        output = client.execute_command("python3 /workspace/fib.py")
        log.info(f"   Output: {output.strip()}\n")
        
        # Step 6: 下载结果文件
        log.info("Step 6: Downloading output file...")
        local_path = "/tmp/pico_output.json"
        client.download_file("/workspace/output.json", local_path)
        client.download_file("/workspace/upload.txt", "/tmp/download.txt")
        log.info(f"✅ File downloaded to {local_path}\n")
        
        # Step 7: 验证结果
        log.info("Step 7: Verifying output...")
        with open(local_path, 'r') as f:
            data = json.load(f)
        log.info(f"   Generated {data['count']} numbers, sum: {data['sum']}")
        log.info(f"   Timestamp: {data['timestamp']}\n")
        
        # Step 8: 运行代码
        log.info("Step 8: Running code in sandbox...")
        stdout = client.run_code(
            language="py",
            code="print('Hello from Python!'); import sys; print('Python version:', sys.version.split()[0])"
        )
        log.info(f"   ✅ Python Output: {stdout.strip()}\n")
        
        # Step 9: 运行 Bash 脚本
        log.info("Step 9: Running bash script...")
        bash_script = """
for i in 1 2 3 4 5; do
    echo "Count: $i"
done
echo "Done!"
"""
        stdout = client.run_code(language="bash", code=bash_script)
        log.info(f"   ✅ Bash Output:\n{stdout}\n")
        
        # Step 10: 清理
        log.info("Step 10: Cleanup...")
        client.cleanup()
        log.info("✅ Client resources cleaned up\n")
        
        log.info("\n===========================================")
        log.info("🎉 All operations completed successfully")
        log.info("===========================================")
        
    except Exception as e:
        log.error(f"\n❌ Error: {str(e)}")
        import traceback
        traceback.print_exc()
        sys.exit(1)


if __name__ == "__main__":
    main()

