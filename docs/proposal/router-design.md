# Router Submodule Design Document

## 1. Overview

Router apiserver is responsible for receiving user HTTP requests and forwarding them to the corresponding Sandbox. Router focuses on high-performance request routing, while session and sandbox management is handled by SessionManager.

## 2. Architecture Design

### 2.1 Overall Architecture Flow

```mermaid
graph TB
    Client[Client] --> Router[Router API Server]
    Router --> SessionMgr[SessionManager Interface]
    Router --> Sandbox1[Sandbox 1]
    Router --> Sandbox2[Sandbox 2]
    Router --> SandboxN[Sandbox N]
    
    subgraph "Router Core Components"
        Router
        SessionMgr
    end
    
    subgraph "Sandbox Cluster"
        Sandbox1
        Sandbox2
        SandboxN
    end
```

### 2.2 Request Routing Flow

```mermaid
sequenceDiagram
    participant C as Client
    participant R as Router
    participant SM as SessionManager (Mock)
    participant SB as Sandbox
    
    C->>R: HTTP Request with x-agentcube-session-id
    
    alt session-id exists
        R->>SM: getSandboxInfoBySessionId(session-id)
        SM->>R: return (endpoint, session-id, nil)
    else session-id is empty
        R->>SM: getSandboxInfoBySessionId("")
        SM->>R: return (endpoint, new-session-id, nil)
    end
    
    alt get sandbox success
        R->>SB: forward request to sandbox endpoint
        SB->>R: return response
        R->>C: forward response + session-id header
    else get sandbox failed
        R->>C: return error response
    end
```

## 3. Detailed Design

### 3.1 SessionManager Interface Definition

Router obtains Sandbox information through the SessionManager interface, which can be implemented with Mock:

```go
type SessionManager interface {
    // Get sandbox information based on session-id
    // When sessionId is empty, create a new session
    GetSandboxInfoBySessionId(sessionId string) (endpoint string, newSessionId string, err error)
}

// Mock implementation example
type MockSessionManager struct {
    sandboxEndpoints []string
    currentIndex     int
}

func (m *MockSessionManager) GetSandboxInfoBySessionId(sessionId string) (string, string, error) {
    if sessionId == "" {
        sessionId = generateNewSessionId()
    }
    
    // Simple round-robin sandbox selection
    endpoint := m.sandboxEndpoints[m.currentIndex%len(m.sandboxEndpoints)]
    m.currentIndex++
    
    return endpoint, sessionId, nil
}
```

### 3.2 Supported Request Types

Uses Gin framework to provide HTTP Server services, handling two types of requests:

1. **Agent Invoke Requests**
   ```
   <frontend-url>:<frontend-port>/v1/namespaces/{agentNamespace}/agent-runtimes/{agentName}/invocations/<agent specific path>
   ```

2. **Code Interpreter Invoke Requests**
   ```
   <frontend-url>:<frontend-port>/v1/namespaces/{namespace}/code-interpreters/{name}/invocations/<code interpreter specific path>
   ```

### 3.3 Request Processing Flow

```mermaid
flowchart TD
    Start([Receive HTTP Request]) --> ValidateReq{Validate Request Format}
    ValidateReq -->|Invalid| ReturnBadRequest[Return 400 Bad Request]
    ValidateReq -->|Valid| ExtractSessionId[Extract x-agentcube-session-id]
    
    ExtractSessionId --> GetSandbox[Call SessionMgr.GetSandboxInfoBySessionId]
    GetSandbox --> CheckResult{Check Result}
    
    CheckResult -->|Success| ForwardRequest[Forward Request to Sandbox]
    CheckResult -->|Interface Error| ReturnInternalError[Return 500 Internal Server Error]
    
    ForwardRequest --> CheckSandboxResponse{Check Sandbox Response}
    CheckSandboxResponse -->|Success| ReturnSuccess[Return Success Response + Session ID]
    CheckSandboxResponse -->|Timeout| ReturnTimeout[Return 504 Gateway Timeout]
    CheckSandboxResponse -->|Connection Failed| ReturnBadGateway[Return 502 Bad Gateway]
    CheckSandboxResponse -->|Other Error| ReturnSandboxError[Return Sandbox Error]
    
    ReturnBadRequest --> End([End])
    ReturnInternalError --> End
    ReturnSuccess --> End
    ReturnTimeout --> End
    ReturnBadGateway --> End
    ReturnSandboxError --> End
```

### 3.4 Core Requirements

1. **High-Performance Routing**: Fast routing to corresponding sandbox based on session-id
2. **Session Integration**: Seamless collaboration with SessionManager, supporting dynamic sandbox creation
3. **Long Connection Support**: Support for long-running requests such as code execution and file operations
4. **Simple Design**: Focus on core routing functionality, avoid over-engineering
5. **Graceful Shutdown**: Reference E2B's graceful shutdown process to ensure no request loss

### 3.5 Design Goals

- **High Performance**: Millisecond-level routing latency, support for high concurrency
- **High Availability**: Stateless design, support for horizontal scaling
- **Observability**: Complete monitoring, logging, and tracing system

## 4. HTTP Response Handling

### 4.1 Success Responses

| Status Code | Scenario | Response Headers | Response Body |
|-------------|----------|------------------|---------------|
| 200 OK | Request processed successfully | `x-agentcube-session-id: <session-id>` | Original response from Sandbox |
| 201 Created | Resource created successfully | `x-agentcube-session-id: <session-id>` | Created resource information |
| 202 Accepted | Async request accepted | `x-agentcube-session-id: <session-id>` | Task status information |

### 4.2 Client Error Responses

| Status Code | Scenario | Response Body Example |
|-------------|----------|----------------------|
| 400 Bad Request | Invalid request format, invalid parameters | `{"error": "invalid request format", "code": "INVALID_REQUEST"}` |
| 401 Unauthorized | Authentication failed | `{"error": "authentication required", "code": "AUTH_REQUIRED"}` |
| 403 Forbidden | Insufficient permissions | `{"error": "insufficient permissions", "code": "PERMISSION_DENIED"}` |
| 404 Not Found | Session or resource not found | `{"error": "session not found", "code": "SESSION_NOT_FOUND"}` |
| 409 Conflict | Resource conflict | `{"error": "resource conflict", "code": "RESOURCE_CONFLICT"}` |
| 429 Too Many Requests | Rate limit exceeded | `{"error": "rate limit exceeded", "code": "RATE_LIMIT_EXCEEDED"}` |

### 4.3 Server Error Responses

| Status Code | Scenario | Response Body Example |
|-------------|----------|----------------------|
| 500 Internal Server Error | Router internal error | `{"error": "internal server error", "code": "INTERNAL_ERROR"}` |
| 502 Bad Gateway | Sandbox connection failed | `{"error": "sandbox unreachable", "code": "SANDBOX_UNREACHABLE"}` |
| 503 Service Unavailable | Sandbox unavailable or overloaded | `{"error": "sandbox unavailable", "code": "SANDBOX_UNAVAILABLE"}` |
| 504 Gateway Timeout | Sandbox response timeout | `{"error": "sandbox timeout", "code": "SANDBOX_TIMEOUT"}` |

### 4.4 Error Handling Flow

```mermaid
flowchart TD
    Error[Error Occurred] --> CheckErrorType{Check Error Type}
    
    CheckErrorType -->|Request Validation Error| ClientError[Client Error 4xx]
    CheckErrorType -->|SessionMgr Error| CheckSessionError{Session Error Type}
    CheckErrorType -->|Sandbox Error| CheckSandboxError{Sandbox Error Type}
    CheckErrorType -->|Router Internal Error| ServerError[Server Error 5xx]
    
    CheckSessionError -->|Session Not Found| Return404[404 Not Found]
    CheckSessionError -->|Session Creation Failed| Return500[500 Internal Error]
    CheckSessionError -->|Permission Error| Return403[403 Forbidden]
    
    CheckSandboxError -->|Connection Failed| Return502[502 Bad Gateway]
    CheckSandboxError -->|Response Timeout| Return504[504 Gateway Timeout]
    CheckSandboxError -->|Service Unavailable| Return503[503 Service Unavailable]
    CheckSandboxError -->|Sandbox Internal Error| ForwardError[Forward Sandbox Error]
    
    ClientError --> LogError[Log Error]
    ServerError --> LogError
    Return404 --> LogError
    Return500 --> LogError
    Return403 --> LogError
    Return502 --> LogError
    Return504 --> LogError
    Return503 --> LogError
    ForwardError --> LogError
    
    LogError --> UpdateMetrics[Update Metrics]
    UpdateMetrics --> ReturnResponse[Return Error Response]
```

## 5. Performance and Monitoring

### 5.1 Performance Metrics

- **Routing Latency**: Target < 5ms (P99)
- **Throughput**: Target > 10,000 RPS
- **Concurrent Connections**: Support > 50,000 concurrent connections
- **Memory Usage**: < 1GB (steady state)

### 5.2 Monitoring Metrics

```mermaid
graph LR
    subgraph "Request Metrics"
        A[Total Requests]
        B[Request Latency]
        C[Error Rate]
        D[Concurrency]
    end
    
    subgraph "Routing Metrics"
        E[Routing Success Rate]
        F[Session Creation Rate]
        G[Sandbox Hit Rate]
    end
    
    subgraph "System Metrics"
        H[CPU Usage]
        I[Memory Usage]
        J[Network I/O]
        K[Connection Pool Status]
    end
```

### 5.3 Logging

- **Access Logs**: Record all HTTP requests
- **Error Logs**: Record all errors and exceptions
- **Performance Logs**: Record key performance metrics
- **Audit Logs**: Record important operations and state changes

## 6. Deployment and Operations

### 6.1 Deployment Architecture

```mermaid
graph TB
    LB[Load Balancer] --> R1[Router Instance 1]
    LB --> R2[Router Instance 2]
    LB --> R3[Router Instance N]
    
    R1 --> SM[SessionManager Interface]
    R2 --> SM
    R3 --> SM
    
    R1 --> SB1[Sandbox Cluster]
    R2 --> SB1
    R3 --> SB1
    
    subgraph "Router Layer"
        R1
        R2
        R3
    end
    
    subgraph "Interface Layer"
        SM
    end
    
    subgraph "Sandbox Layer"
        SB1
    end
```

### 6.2 Configuration Management

- **Environment Configuration**: Support for multi-environment configuration (dev/staging/prod)
- **Dynamic Configuration**: Support for runtime configuration updates
- **Configuration Validation**: Validate configuration integrity at startup

### 6.3 Health Checks

- **Liveness Check**: `/health/live`
- **Readiness Check**: `/health/ready`
- **Dependency Check**: Verify connectivity to SessionManager and Sandbox
