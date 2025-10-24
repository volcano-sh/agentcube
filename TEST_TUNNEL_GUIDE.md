# Tunnel 测试指南

## 快速开始

### 1. 构建测试工具

```bash
# 构建 test-tunnel 工具
make build-test-tunnel

# 或构建所有工具
make build-all
```

### 2. 启动 pico-apiserver

```bash
# 本地开发模式
make run

# 或在 Kubernetes 中
make k8s-deploy
```

### 3. 创建 Session

```bash
# 创建一个新的 sandbox session
curl -X POST http://localhost:8080/v1/sessions \
  -H "Content-Type: application/json" \
  -d '{
    "ttl": 3600,
    "image": "sandbox:latest"
  }' | jq '.'

# 记录返回的 session_id
```

输出示例：
```json
{
  "session_id": "d6bdc5a3-c963-4c0f-be75-bb8083739883",
  "status": "running",
  "created_at": "2025-10-24T03:45:00Z",
  "expires_at": "2025-10-24T04:45:00Z",
  ...
}
```

### 4. 等待 Sandbox 就绪

```bash
# 检查 sandbox pod 状态
kubectl get pods -l managed-by=pico-apiserver

# 等待状态变为 Running 且 Ready 1/1
```

### 5. 运行 Tunnel 测试

#### 方法 1: 使用 Makefile（推荐）

```bash
# 快速测试（直接运行）
make test-tunnel SESSION_ID=d6bdc5a3-c963-4c0f-be75-bb8083739883

# 或先构建再运行
make test-tunnel-build SESSION_ID=d6bdc5a3-c963-4c0f-be75-bb8083739883
```

#### 方法 2: 直接运行

```bash
# 使用默认参数
./bin/test-tunnel -session d6bdc5a3-c963-4c0f-be75-bb8083739883

# 使用完整参数
./bin/test-tunnel \
  -api http://localhost:8080 \
  -session d6bdc5a3-c963-4c0f-be75-bb8083739883 \
  -user sandbox \
  -password sandbox \
  -cmd "echo 'Hello World'"
```

#### 方法 3: 使用 go run

```bash
go run ./cmd/test-tunnel/main.go -session <session-id>
```

## 测试输出示例

成功的测试输出：

```
2025/10/24 03:45:00 Testing tunnel connection to session: d6bdc5a3-c963-4c0f-be75-bb8083739883
2025/10/24 03:45:00 Connecting to localhost:8080
2025/10/24 03:45:00 Sending CONNECT request to /v1/sessions/d6bdc5a3-c963-4c0f-be75-bb8083739883/tunnel
2025/10/24 03:45:00 Received response: 200 Connection Established
2025/10/24 03:45:00 ✅ HTTP CONNECT tunnel established successfully
2025/10/24 03:45:00 Establishing SSH connection as user: sandbox
2025/10/24 03:45:01 ✅ SSH connection established successfully
2025/10/24 03:45:01 ✅ Command executed successfully

--- Command Output ---
Command: echo 'Hello from sandbox'
Exit Code: 0
Stdout:
Hello from sandbox

--- End Output ---

2025/10/24 03:45:01 Running additional tests...
2025/10/24 03:45:01 ✅ Current directory: /workspace
2025/10/24 03:45:01 ✅ Current user: sandbox
2025/10/24 03:45:01 ✅ Python version: Python 3.11.9

🎉 All tests completed successfully!
```

## 命令行参数

| 参数        | 默认值                      | 说明                       |
| ----------- | --------------------------- | -------------------------- |
| `-api`      | `http://localhost:8080`     | pico-apiserver 地址        |
| `-session`  | *必需*                      | Session ID                 |
| `-token`    | `""`                        | 认证 token（如果启用认证） |
| `-user`     | `sandbox`                   | SSH 用户名                 |
| `-password` | `sandbox`                   | SSH 密码                   |
| `-cmd`      | `echo 'Hello from sandbox'` | 要执行的命令               |

## 测试场景

### 基本连接测试

```bash
# 测试基本的 SSH 连接
./bin/test-tunnel -session <id>
```

### Python 代码执行

```bash
# 执行 Python 代码
./bin/test-tunnel -session <id> -cmd "python -c 'import sys; print(sys.version)'"

# 运行 Python 脚本
./bin/test-tunnel -session <id> -cmd "python -c 'print(sum(range(100)))'"
```

### 文件操作测试

```bash
# 创建文件
./bin/test-tunnel -session <id> -cmd "echo 'test data' > /tmp/test.txt && cat /tmp/test.txt"

# 列出文件
./bin/test-tunnel -session <id> -cmd "ls -la /workspace"
```

### 长时间命令

```bash
# Sleep 测试（测试连接稳定性）
./bin/test-tunnel -session <id> -cmd "sleep 5 && echo 'Sleep completed'"

# 大量输出
./bin/test-tunnel -session <id> -cmd "for i in {1..100}; do echo Line \$i; done"
```

### 网络测试

```bash
# Ping 测试
./bin/test-tunnel -session <id> -cmd "ping -c 3 8.8.8.8"

# DNS 解析
./bin/test-tunnel -session <id> -cmd "nslookup google.com"

# HTTP 请求
./bin/test-tunnel -session <id> -cmd "curl -s https://api.github.com/users/github"
```

## 故障排查

### 问题 1: Connection Refused

```
Error: failed to connect to server: connection refused
```

**原因**: pico-apiserver 未运行

**解决方案**:
```bash
# 检查服务状态
curl http://localhost:8080/health

# 本地启动
make run

# 或检查 k8s 部署
kubectl get pods -l app=pico-apiserver
kubectl logs -l app=pico-apiserver
```

### 问题 2: Session Not Found

```
Error: CONNECT failed with status 404: Session not found
```

**原因**: Session ID 不存在或已过期

**解决方案**:
```bash
# 列出所有 sessions
curl http://localhost:8080/v1/sessions | jq '.'

# 创建新 session
curl -X POST http://localhost:8080/v1/sessions \
  -H "Content-Type: application/json" \
  -d '{"ttl": 3600}'
```

### 问题 3: Sandbox Not Ready

```
Error: CONNECT failed with status 503: Sandbox not ready
```

**原因**: Sandbox pod 还未就绪

**解决方案**:
```bash
# 检查 pod 状态
kubectl get pods -l managed-by=pico-apiserver

# 等待 pod 就绪
kubectl wait --for=condition=Ready pod -l session-id=<session-id> --timeout=60s

# 检查 pod 日志
kubectl logs <sandbox-pod-name>
```

### 问题 4: SSH Handshake Failed

```
Error: failed to establish SSH connection: ssh: handshake failed
```

**可能原因**:
1. SSH 服务未启动
2. 凭据错误
3. Pod 网络问题

**解决方案**:
```bash
# 1. 检查 SSH 服务
kubectl exec <sandbox-pod-name> -- ps aux | grep sshd

# 2. 验证凭据
./bin/test-tunnel -session <id> -user sandbox -password sandbox

# 3. 测试 pod 网络
kubectl exec <sandbox-pod-name> -- ip addr
kubectl exec <sandbox-pod-name> -- netstat -tlnp
```

### 问题 5: Authentication Failed

```
Error: ssh: unable to authenticate
```

**原因**: 用户名或密码错误

**解决方案**:
```bash
# 使用正确的凭据
./bin/test-tunnel -session <id> -user sandbox -password sandbox

# 或检查 sandbox 镜像配置
docker run --rm -it sandbox:latest /bin/bash
# 在容器内测试 SSH
```

### 问题 6: Command Execution Failed

```
Error: failed to execute command
```

**解决方案**:
```bash
# 使用简单命令测试
./bin/test-tunnel -session <id> -cmd "echo test"

# 检查 shell 语法
./bin/test-tunnel -session <id> -cmd "whoami"

# 调试命令输出
./bin/test-tunnel -session <id> -cmd "bash -x -c 'your-command'"
```

## 完整测试流程脚本

创建 `test_complete.sh`:

```bash
#!/bin/bash
set -e

echo "========================================="
echo "完整 Tunnel 测试流程"
echo "========================================="

# 步骤 1: 检查 pico-apiserver
echo -e "\n[1/5] 检查 pico-apiserver..."
if curl -s http://localhost:8080/health > /dev/null; then
    echo "✅ pico-apiserver 运行中"
else
    echo "❌ pico-apiserver 未运行"
    echo "请先运行: make run"
    exit 1
fi

# 步骤 2: 创建 session
echo -e "\n[2/5] 创建 sandbox session..."
RESPONSE=$(curl -s -X POST http://localhost:8080/v1/sessions \
  -H "Content-Type: application/json" \
  -d '{"ttl": 3600, "image": "sandbox:latest"}')

SESSION_ID=$(echo $RESPONSE | jq -r '.session_id')
echo "✅ Session 创建成功: $SESSION_ID"

# 步骤 3: 等待 sandbox 就绪
echo -e "\n[3/5] 等待 sandbox pod 就绪..."
SANDBOX_NAME=$(echo $RESPONSE | jq -r '.sandbox_name')
kubectl wait --for=condition=Ready pod -l sandbox=$SANDBOX_NAME --timeout=60s
echo "✅ Sandbox 就绪"

# 步骤 4: 运行 tunnel 测试
echo -e "\n[4/5] 运行 tunnel 测试..."
./bin/test-tunnel -session $SESSION_ID
echo "✅ Tunnel 测试成功"

# 步骤 5: 清理
echo -e "\n[5/5] 清理资源..."
curl -s -X DELETE http://localhost:8080/v1/sessions/$SESSION_ID
echo "✅ Session 已删除"

echo -e "\n========================================="
echo "🎉 所有测试完成！"
echo "========================================="
```

使用方法：
```bash
chmod +x test_complete.sh
./test_complete.sh
```

## 高级用法

### 并发测试

```bash
# 同时测试多个 session
for i in {1..5}; do
  SESSION_ID=$(curl -s -X POST http://localhost:8080/v1/sessions \
    -H "Content-Type: application/json" \
    -d '{"ttl": 3600}' | jq -r '.session_id')
  
  ./bin/test-tunnel -session $SESSION_ID &
done
wait
```

### 压力测试

```bash
# 循环测试
for i in {1..100}; do
  echo "Test iteration $i"
  ./bin/test-tunnel -session <id> -cmd "echo Test $i"
  sleep 1
done
```

### 与 SDK 配合测试

```python
# Python 示例
from sandbox_sdk import SandboxClient

client = SandboxClient("http://localhost:8080", token="")
session = client.create_session(ttl=3600)

print(f"Session ID: {session.session_id}")
print(f"Run: ./bin/test-tunnel -session {session.session_id}")
```

## 参考文档

- [test-tunnel README](cmd/test-tunnel/README.md) - 详细工具说明
- [Tunnel 实现分析](TUNNEL_ANALYSIS.md) - 技术细节
- [pico-apiserver README](README.md) - 项目概述
- [Sandbox 镜像指南](images/sandbox/README.md) - 镜像配置

## 下一步

1. ✅ 测试基本 tunnel 连接
2. ✅ 验证 SSH 功能
3. ⏭️ 测试文件上传/下载
4. ⏭️ 测试长时间会话
5. ⏭️ 性能和并发测试

