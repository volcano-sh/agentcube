# Core Components

AgentCube consists of several specialized components working together to provide a seamless agent hosting experience.

## 1. Workload Manager

The orchestrator of the AgentCube ecosystem. It manages the lifecycle of all sandboxes.

- **Sandbox APIServer**: Provides the internal API for the Router to request new sandboxes.
- **WarmPool Controller**: Maintains a configurable number of pre-instantiated pods to ensure "warm starts" for new sessions.
- **Session Registry**: Persists session metadata, endpoints, and expiration timestamps in the global state store.

## 2. AgentCube Router

The gateway to the agent world. Every request from your SDK or frontend goes through the Router.

- **Session Identification**: Uses the `x-agentcube-session-id` header to route traffic.
- **Lazy Provisioning**: If a request arrives for a non-existent session, the Router automatically triggers the Workload Manager to create it on the fly.
- **High Availability**: Stateless design allows multiple Router replicas to serve traffic, synchronized via Redis.

## 3. PicoD / AgentD

The "agent in the box." This is a lightweight daemon running inside every sandbox.

- **Code Execution**: Receives commands from the Data Plane and executes them in a secure subprocess.
- **Secure File Ops**: Handles file uploads and downloads within the sandbox filesystem.
- **JWT Validation**: Uses a session-specific Public Key to verify that every incoming command is signed by the authorized client.

## 4. Session Store (Redis)

The "brain" that keeps the cluster in sync.

- **State Sync**: Allows different Router replicas to know exactly where a session's pod is located.
- **Heartbeats**: Tracks the "last active" time of every session to inform garbage collection.

---

## Component Interaction Flow

1. **Client** sends a request to the **Router**.
2. **Router** checks **Redis** for an existing session.
3. If not found, **Router** asks **Workload Manager** for a new sandbox.
4. **Workload Manager** grabs a pod from the **WarmPool** and registers it in **Redis**.
5. **Router** forwards the request to **PicoD** inside the pod.
6. **PicoD** executes the code and returns the result.
