"""
PicoD REST API Client

这是一个轻量级的 REST API 客户端，用于与 PicoD 守护进程交互。
PicoD 通过简单的 HTTP 端点提供基本的沙箱能力。

这个客户端提供与 SSHClient 相同的接口，便于迁移。
"""

import os
import shlex
import base64
from typing import Dict, List, Optional

try:
    import requests
except ImportError:
    raise ImportError(
        "requests library is required for PicoDClient. "
        "Install it with: pip install requests"
    )


class PicoDClient:
    """用于与 PicoD 守护进程通过 REST API 交互的客户端
    
    这个客户端提供与 SandboxSSHClient 相同的接口，
    使其成为基于 SSH 的通信的替代方案。
    
    示例:
        >>> client = PicoDClient(host="localhost", port=9527, access_token="secret")
        >>> result = client.execute_command("echo 'Hello World'")
        >>> print(result)
        Hello World
        
        >>> client.write_file(content="test data", remote_path="/tmp/test.txt")
        >>> client.download_file(remote_path="/tmp/test.txt", local_path="./test.txt")
    """
    
    def __init__(
        self,
        host: str,
        port: int = 9527,
        access_token: Optional[str] = None,
        timeout: int = 30,
    ):
        """初始化 PicoD 客户端连接参数
        
        Args:
            host: PicoD 服务器主机名或 IP 地址
            port: PicoD 服务器端口 (默认: 9527)
            access_token: 认证访问令牌 (可选)
            timeout: 默认请求超时时间（秒）
        """
        self.base_url = f"http://{host}:{port}"
        self.access_token = access_token
        self.timeout = timeout
        self.session = requests.Session()
        
        if access_token:
            self.session.headers.update({
                "Authorization": f"Bearer {access_token}",
            })
    
    def execute_command(
        self,
        command: str,
        timeout: Optional[float] = None,
    ) -> str:
        """在沙箱中执行命令并返回 stdout
        
        与 SandboxSSHClient.execute_command() 兼容
        
        Args:
            command: 要执行的命令
            timeout: 命令执行超时时间（秒）
            
        Returns:
            命令的 stdout 输出
            
        Raises:
            Exception: 如果命令执行失败（非零退出码）
        """
        payload = {
            "command": command,
            "timeout": timeout or self.timeout,
        }
        
        response = self.session.post(
            f"{self.base_url}/api/execute",
            json=payload,
            timeout=timeout or self.timeout,
        )
        response.raise_for_status()
        
        result = response.json()
        
        if result["exit_code"] != 0:
            raise Exception(
                f"Command execution failed (exit code {result['exit_code']}): {result['stderr']}"
            )
        
        return result["stdout"]
    
    def execute_commands(self, commands: List[str]) -> Dict[str, str]:
        """在沙箱中执行多条命令
        
        与 SandboxSSHClient.execute_commands() 兼容
        
        Args:
            commands: 要执行的命令列表
            
        Returns:
            命令到输出的字典映射
        """
        results = {}
        for cmd in commands:
            results[cmd] = self.execute_command(cmd)
        return results
    
    def run_code(
        self,
        language: str,
        code: str,
        timeout: Optional[float] = None
    ) -> str:
        """运行指定语言的代码片段
        
        与 SandboxSSHClient.run_code() 兼容
        
        Args:
            language: 编程语言 (如 "python", "bash")
            code: 要执行的代码片段
            timeout: 执行超时时间（秒）
            
        Returns:
            代码执行输出
            
        Raises:
            ValueError: 如果不支持该语言
        """
        lang = language.lower()
        lang_aliases = {
            "python": ["python", "py", "python3"],
            "bash": ["bash", "sh", "shell"]
        }

        target_lang = None
        for std_lang, aliases in lang_aliases.items():
            if lang in aliases:
                target_lang = std_lang
                break
        
        if not target_lang:
            raise ValueError(
                f"Unsupported language: {language}. Supported: {list(lang_aliases.keys())}"
            )

        quoted_code = shlex.quote(code)
        if target_lang == "python":
            command = f"python3 -c {quoted_code}"
        elif target_lang == "bash":
            command = f"bash -c {quoted_code}"

        return self.execute_command(command, timeout)
    
    def write_file(
        self,
        content: str,
        remote_path: str,
    ) -> None:
        """将内容写入沙箱中的文件 (JSON/base64)
        
        与 SandboxSSHClient.write_file() 兼容
        
        Args:
            content: 要写入远程文件的内容
            remote_path: 远程服务器上的写入路径
        """
        # 编码为 base64
        if isinstance(content, str):
            content_bytes = content.encode('utf-8')
        else:
            content_bytes = content
        
        content_b64 = base64.b64encode(content_bytes).decode('utf-8')
        
        payload = {
            "path": remote_path,
            "content": content_b64,
            "mode": "0644"
        }
        
        response = self.session.post(
            f"{self.base_url}/api/files",
            json=payload,
            timeout=self.timeout,
        )
        response.raise_for_status()
    
    def upload_file(
        self,
        local_path: str,
        remote_path: str,
    ) -> None:
        """将本地文件上传到沙箱 (multipart/form-data)
        
        与 SandboxSSHClient.upload_file() 兼容
        
        Args:
            local_path: 要上传的本地文件路径
            remote_path: 远程服务器上的上传路径
            
        Raises:
            FileNotFoundError: 如果本地文件不存在
        """
        if not os.path.exists(local_path):
            raise FileNotFoundError(f"Local file not found: {local_path}")
        
        with open(local_path, 'rb') as f:
            files = {'file': f}
            data = {'path': remote_path, 'mode': '0644'}
            
            # 只使用 Authorization header
            headers = {}
            if self.access_token:
                headers["Authorization"] = f"Bearer {self.access_token}"
            
            response = requests.post(
                f"{self.base_url}/api/files",
                headers=headers,
                files=files,
                data=data,
                timeout=self.timeout,
            )
            response.raise_for_status()
    
    def download_file(
        self,
        remote_path: str,
        local_path: str
    ) -> None:
        """从沙箱下载文件
        
        与 SandboxSSHClient.download_file() 兼容
        
        Args:
            remote_path: 远程服务器上的下载路径
            local_path: 保存下载文件的本地路径
        """
        # 确保本地目录存在
        local_dir = os.path.dirname(local_path)
        if local_dir:
            os.makedirs(local_dir, exist_ok=True)
        
        # 处理路径：移除前导 /
        clean_remote_path = remote_path.lstrip('/')
        
        response = self.session.get(
            f"{self.base_url}/api/files/{clean_remote_path}",
            stream=True,
            timeout=self.timeout,
        )
        response.raise_for_status()
        
        with open(local_path, 'wb') as f:
            for chunk in response.iter_content(chunk_size=8192):
                if chunk:
                    f.write(chunk)
    
    def cleanup(self) -> None:
        """清理资源（关闭 HTTP 会话）
        
        与 SandboxSSHClient.cleanup() 兼容
        """
        if self.session:
            self.session.close()
    
    def __enter__(self):
        """上下文管理器入口"""
        return self
    
    def __exit__(self, exc_type, exc_val, exc_tb):
        """上下文管理器退出"""
        self.cleanup()
    
    @staticmethod
    def generate_ssh_key_pair():
        """生成 SSH 密钥对（不适用于 PicoD）
        
        此方法保留以与 SandboxSSHClient 的 API 兼容，
        但会抛出 NotImplementedError，因为 PicoD 使用基于令牌的认证。
        """
        raise NotImplementedError(
            "PicoD uses token-based authentication, not SSH keys. "
            "Please provide an access token when initializing the client."
        )

