# AgentCube SDK Examples

This directory contains examples of how to use the AgentCube Python SDK.

## Prerequisites

1.  **Install the SDK**:
    
    You can install the SDK from the parent directory:
    ```bash
    cd ..
    pip install .
    ```

2.  **AgentCube Environment**:
    You need access to a running AgentCube instance (WorkloadManager and Router).
    
    Set the following environment variables to point to your AgentCube services:
    ```bash
    export WORKLOAD_MANAGER_URL="http://<your-workload-manager-host>:<port>"
    export ROUTER_URL="http://<your-router-host>:<port>"
    
    # Optional: If your instance requires authentication
    # export API_TOKEN="your-token"
    ```

## Running the Examples

### Basic Usage

`basic_usage.py` demonstrates the core features:
*   Connecting to the Control Plane (WorkloadManager)
*   Creating a secure session
*   Executing shell commands
*   Running Python code
*   Managing files
*   Automatic session cleanup

To run it:
```bash
python basic_usage.py
```
