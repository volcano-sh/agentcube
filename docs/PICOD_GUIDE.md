# PicoD 使用指南

**PicoD** (Pico Daemon) 是一个轻量级的沙箱服务守护进程，用于替代传统的 SSH 连接方式。它通过简单的 REST API 提供命令执行和文件传输能力。

---

## 📋 目录

- [架构概述](#架构概述)
- [快速开始](#快速开始)
- [客户端 SDK](#客户端-sdk)
- [测试示例](#测试示例)
- [API 参考](#api-参考)
- [故障排查](#故障排查)
- [与 SSH 的对比](#与-ssh-的对比)

---

## 🏗️ 架构概述

```
┌─────────────────┐      HTTP/REST       ┌─────────────────┐
│  Python Client  │ ─────────────────────>│  PicoD Server   │
│  (picod_client) │ <───────────────────  │   (Go/Gin)      │
└─────────────────┘     JSON + Token      └─────────────────┘
                                                    │
                                                    ▼
                                            ┌──────────────┐
                                            │   Sandbox    │
                                            │  File System │
                                            └──────────────┘
```

**特点**：
- 🚀 **轻量级**: 替代 SSH，使用简单的 REST API
- 🔒 **安全**: Bearer Token 认证
- 📦 **兼容性**: 与 `SandboxSSHClient` 接口完全兼容
- 🌐 **易用**: 使用标准 HTTP 协议，无需 SSH 密钥

**核心组件**：
1. **PicoD Server** (Go): 运行在沙箱内的 HTTP 服务器
2. **PicoDClient** (Python): SDK 客户端，与 `SandboxSSHClient` API 兼容
3. **REST API**: 三个核心端点
   - `POST /api/execute` - 命令执行
   - `POST /api/files` - 文件上传
   - `GET /api/files/{path}` - 文件下载

---

## 🚀 快速开始

### 1. 构建 PicoD 服务器

```bash
cd /Users/wangxu/agent/agentcube

# 构建服务器
make build-picod

# 或手动构建
go build -o bin/picod ./cmd/picod
```

### 2. 启动 PicoD 服务器

```bash
# 方式 1: 带认证（推荐）
./bin/picod --access-token=your-secret-token --port=9527

# 方式 2: 从文件读取 token
echo "your-secret-token" > /tmp/token.txt
./bin/picod --access-token-file=/tmp/token.txt --port=9527

# 方式 3: 从环境变量
export PICOD_ACCESS_TOKEN=your-secret-token
./bin/picod --port=9527

# 方式 4: 无认证（仅测试）
./bin/picod --port=9527
```

**命令行参数**:
- `--port`: 监听端口（默认 9527）
- `--access-token`: 访问令牌
- `--access-token-file`: 从文件读取令牌
- 环境变量: `PICOD_ACCESS_TOKEN`

### 3. 安装 Python 依赖

```bash
cd sdk-python
pip install requests
```

### 4. 运行测试

**高层测试（Python SDK）**:
```bash
export PICOD_HOST=localhost
export PICOD_PORT=9527
export PICOD_ACCESS_TOKEN=your-secret-token

python3 sdk-python/examples/picod_example.py
```

**低层测试（Go 直接调用 REST API）**:
```bash
# 构建测试程序
make build-picod-client

# 运行测试
export PICOD_URL=http://localhost:9527
export PICOD_ACCESS_TOKEN=your-secret-token
./bin/picod-client
```

---

## 📖 客户端 SDK

### Python SDK: `PicoDClient`

`PicoDClient` 提供与 `SandboxSSHClient` 完全兼容的接口。

#### 初始化

```python
from agentcube.clients.picod_client import PicoDClient

client = PicoDClient(
    host="localhost",
    port=9527,
    access_token="your-secret-token",
    timeout=30  # 默认超时（秒）
)
```

#### 命令执行

```python
# 执行单条命令
output = client.execute_command("ls -la /workspace")
print(output)

# 批量执行
commands = ["whoami", "pwd", "uname -a"]
results = client.execute_commands(commands)
for cmd, output in results.items():
    print(f"{cmd}: {output}")
```

#### 代码执行

```python
# Python 代码
python_code = """
import os
print(f"PID: {os.getpid()}")
"""
output = client.run_code("python", python_code)

# Bash 脚本
bash_script = "for i in 1 2 3; do echo $i; done"
output = client.run_code("bash", bash_script)
```

**支持的语言**: `python`, `py`, `python3`, `bash`, `sh`, `shell`

#### 文件操作

```python
# 写入文件（JSON+Base64）
client.write_file(
    content="Hello World",
    remote_path="/workspace/hello.txt"
)

# 上传文件（multipart）
client.upload_file(
    local_path="./data.csv",
    remote_path="/workspace/data.csv"
)

# 下载文件
client.download_file(
    remote_path="/workspace/output.json",
    local_path="./output.json"
)
```

#### 资源清理

```python
# 手动清理
client.cleanup()

# 或使用上下文管理器（推荐）
with PicoDClient(host="localhost", port=9527, access_token="token") as client:
    output = client.execute_command("echo 'Hello'")
# 自动调用 cleanup()
```

---

## 🧪 测试示例

### 测试 1: Go 低层测试（直接 REST API）

**对应**: `client.go` (SSH 版本)  
**位置**: `example/picod_client.go`

这个测试直接调用 PicoD 的 REST API，不依赖任何 SDK：

```bash
# 构建
make build-picod-client

# 运行
export PICOD_URL=http://localhost:9527
export PICOD_ACCESS_TOKEN=test-token
./bin/picod-client
```

**测试内容**:
1. ✅ 健康检查
2. ✅ 执行基本命令
3. ✅ 上传文件（multipart）
4. ✅ 写入文件（JSON+Base64）
5. ✅ 执行 Python 脚本
6. ✅ 下载文件
7. ✅ 验证文件内容

### 测试 2: Python 高层测试（SDK）

**对应**: `examples.py` (SSH 版本)  
**位置**: `sdk-python/examples/picod_example.py`

这个测试通过 `PicoDClient` SDK 进行操作：

```bash
export PICOD_HOST=localhost
export PICOD_PORT=9527
export PICOD_ACCESS_TOKEN=test-token

python3 sdk-python/examples/picod_example.py
```

**测试内容**:
1. ✅ 初始化客户端
2. ✅ 执行测试命令
3. ✅ 上传文件
4. ✅ 验证上传
5. ✅ 写入 Python 脚本
6. ✅ 执行脚本
7. ✅ 下载结果
8. ✅ 验证 JSON 输出
9. ✅ 运行 Python/Bash 代码
10. ✅ 资源清理

### 预期输出

两个测试都应该输出类似：

```
===========================================
PicoD ... Test
===========================================

Initializing PicoD client...
✅ PicoD client initialized

Step 1: Executing test commands...
   [1/5] Executing: whoami
      Output: root
...

🎉 All tests passed successfully!
===========================================
```

---

## 📚 API 参考

### 健康检查

**端点**: `GET /health`  
**认证**: 不需要

```bash
curl http://localhost:9527/health
```

**响应**:
```json
{
  "status": "ok",
  "service": "PicoD",
  "version": "1.0.0",
  "uptime": "2h30m15s"
}
```

---

### 命令执行

**端点**: `POST /api/execute`  
**认证**: Bearer Token

**请求**:
```json
{
  "command": "ls -la /workspace",
  "timeout": 30,
  "working_dir": "/workspace",
  "env": {
    "VAR1": "value1"
  }
}
```

**响应**:
```json
{
  "stdout": "total 8\ndrwxr-xr-x ...",
  "stderr": "",
  "exit_code": 0,
  "duration": 0.15
}
```

**示例**:
```bash
curl -X POST http://localhost:9527/api/execute \
  -H "Authorization: Bearer your-token" \
  -H "Content-Type: application/json" \
  -d '{"command": "echo Hello", "timeout": 10}'
```

---

### 文件上传

**端点**: `POST /api/files`  
**认证**: Bearer Token

#### 方式 1: Multipart Form-Data（推荐）

```bash
curl -X POST http://localhost:9527/api/files \
  -H "Authorization: Bearer your-token" \
  -F "path=/workspace/data.csv" \
  -F "file=@./local_data.csv" \
  -F "mode=0644"
```

#### 方式 2: JSON + Base64

```bash
curl -X POST http://localhost:9527/api/files \
  -H "Authorization: Bearer your-token" \
  -H "Content-Type: application/json" \
  -d '{
    "path": "/workspace/test.txt",
    "content": "SGVsbG8gV29ybGQ=",
    "mode": "0644"
  }'
```

**响应**:
```json
{
  "path": "/workspace/data.csv",
  "size": 2048,
  "mode": "-rw-r--r--",
  "modified": "2025-11-19T10:30:00Z"
}
```

---

### 文件下载

**端点**: `GET /api/files/{path}`  
**认证**: Bearer Token

```bash
# 下载文件
curl -H "Authorization: Bearer your-token" \
  http://localhost:9527/api/files/workspace/result.txt \
  -o result.txt

# 查看文本文件
curl -H "Authorization: Bearer your-token" \
  http://localhost:9527/api/files/tmp/log.txt
```

**响应头**:
```
HTTP/1.1 200 OK
Content-Type: text/plain
Content-Length: 1024
Content-Disposition: attachment; filename="result.txt"

[文件内容]
```

---

## 🔧 故障排查

### 1. 连接错误

**错误**: `Connection refused`

**解决**:
```bash
# 检查服务器是否运行
curl http://localhost:9527/health

# 检查端口
lsof -i :9527

# 检查进程
ps aux | grep picod
```

---

### 2. 认证失败

**错误**: `401 Unauthorized: Invalid token`

**解决**:
1. 检查服务器启动时的 `--access-token` 参数
2. 确保客户端使用相同的 token
3. 检查环境变量

```bash
# 服务器端
./bin/picod --access-token=my-secret-token

# 客户端
export PICOD_ACCESS_TOKEN=my-secret-token
```

---

### 3. 文件下载失败

**错误**: `404 File not found`

**解决**:
```python
# 先检查文件是否存在
output = client.execute_command("ls -la /workspace/file.txt")
print(output)

# 检查文件权限
output = client.execute_command("stat /workspace/file.txt")
print(output)
```

---

### 4. 命令超时

**错误**: `Command timed out`

**解决**:
```python
# 增加超时时间
client = PicoDClient(host="localhost", port=9527, timeout=60)

# 或单独设置
output = client.execute_command("long-task", timeout=120)
```

---

### 5. 导入错误

**错误**: `ModuleNotFoundError: No module named 'requests'`

**解决**:
```bash
pip install requests
```

---

## 🆚 与 SSH 的对比

| 特性 | SSHClient | PicoDClient |
|------|-----------|-------------|
| **协议** | SSH/SFTP | HTTP/REST |
| **端口** | 22 | 9527 (可配置) |
| **认证** | RSA 密钥对 | Bearer Token |
| **依赖** | paramiko | requests |
| **性能** | 中等 | 较快 |
| **防火墙** | 需要开放 22 | HTTP 友好 |
| **调试** | 困难 | 简单（curl/浏览器） |
| **API 兼容** | ✅ | ✅ 完全兼容 |

### 迁移示例

从 SSH 迁移到 PicoD **非常简单**，只需修改初始化代码：

```python
# 旧代码（SSH）
from agentcube.clients.ssh_client import SandboxSSHClient
client = SandboxSSHClient(private_key=key, tunnel_sock=sock)

# 新代码（PicoD）
from agentcube.clients.picod_client import PicoDClient
client = PicoDClient(host="localhost", port=9527, access_token="token")

# 🎉 后续所有 API 调用完全相同！
output = client.execute_command("ls -la")
client.write_file(content, "/tmp/file.txt")
client.download_file("/tmp/file.txt", "./file.txt")
```

---

## 🏗️ 在沙箱中部署 PicoD

### Docker 镜像集成

在 Dockerfile 中添加 PicoD：

```dockerfile
FROM python:3.11-slim

# 安装 PicoD
COPY bin/picod /usr/local/bin/picod
RUN chmod +x /usr/local/bin/picod

# 启动脚本
COPY start.sh /start.sh
RUN chmod +x /start.sh

ENTRYPOINT ["/start.sh"]
```

**start.sh**:
```bash
#!/bin/bash

# 启动 PicoD（后台）
/usr/local/bin/picod --port=9527 &

# 其他初始化...
exec "$@"
```

### Kubernetes 部署

在 Pod 中运行 PicoD 作为 sidecar：

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: sandbox-pod
spec:
  containers:
  - name: sandbox
    image: sandbox:latest
  - name: picod
    image: picod:latest
    ports:
    - containerPort: 9527
    env:
    - name: PICOD_ACCESS_TOKEN
      valueFrom:
        secretKeyRef:
          name: picod-secret
          key: token
    command: ["/usr/local/bin/picod"]
    args: ["--port=9527"]
```

---

## 📚 相关文档

- [PicoD 设计文档](../PicoD-Design.md)
- [服务器端代码](../pkg/picod/)
- [Go 服务器入口](../cmd/picod/)
- [Python SDK](../sdk-python/agentcube/clients/picod_client.py)
- [测试示例 (Go)](../example/picod_client.go)
- [测试示例 (Python)](../sdk-python/examples/picod_example.py)

---

## 🤝 贡献

欢迎提交 Issue 和 Pull Request！

## 📄 许可证

本项目基于 Apache 2.0 许可证开源。

