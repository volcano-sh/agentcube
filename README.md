# kmetis-sdk

# Project Overview

This repository contains a Python SDK for managing Kubernetes sandboxes (Pods). The SDK provides functionality to create, manage, and interact with sandbox environments running in a Kubernetes cluster.

# Code Architecture

## Project Structure

```
sandbox_sdk/  
├── models/                   # Data models
│   ├── pod_templates.py      # Custom exception classes for handling various error conditions:
│   ├── sandbox_info.py       # Sandbox instance, pod states, and execution result
│   └── models.py  
├── providers/                # Implementation providers  
│   ├── kubernetes/           # K8s implementations  
│   │   ├── client.py         # Low-level K8s client  
│   │   ├── lifecycle.py      # Pod lifecycle manager and related resource manager
│   └── ssh/                  # SSH implementations  
│       ├── client.py         # Low-level SSH client  
│       └── process.py        # Command/transfer manager  
├── services/                 # Domain services  
│   ├── resource_tracker.py   # tracker of resources during sandbox-related operations  
│   ├── log.py                # Enhanced logging service  
│   └── exceptions.py         # exceptions for various error conditions  
├── constants.py              # Constants used for sandbox management
├── sandbox.py                # sandbox core functionality
└── example.py                # example sandbox usage scenarios
```

The SDK is organized into several modules:

1\. \*\*Main SDK (`sandbox.py`)\*\*: The primary `Sandbox` class that implements all core functionality:

&nbsp;  - `create\_sandbox`: Creates a Kubernetes Pod with SSH configured

&nbsp;  - `delete\_sandbox`: Deletes a sandbox Pod

&nbsp;  - `execute\_command`: Executes commands in a sandbox via SSH

&nbsp;  - `upload\_file`: Uploads files to a sandbox via SFTP

&nbsp;  - `download\_file`: Downloads files from a sandbox via SFTP

2\. \*\*SSH Manager (`providers/ssh/process.py`)\*\*: Manages SSH connections with session pooling to optimize connections and reduce overhead.

3\. \*\*Exceptions (`exceptions.py`)\*\*: Custom exception classes for handling various error conditions:

4\. \*\*Logging(`services/log.py`)\*\*:  Provides hierarchical logging mechanisms for debugging specific components, setting different log levels for different modules, analyzing log flows through the system, and maintaining clean separation of concerns in logs.

# Dependencies

\- `kubernetes`: Kubernetes Python client (v27.2.0)

\- `paramiko`: SSH and SFTP library (v3.4.0)

\- `python-dotenv`: Environment variable management (v1.0.0)

# Development Commands

## Installation

```bash
pip install -e .

\# or

pip install kubernetes paramiko python-dotenv
```

## Running the Example

```bash
python example.py
```

## Running Tests

```bash
pytest
```

# Key Implementation Details

1\. \*\*Kubernetes Integration\*\*: The SDK uses the official Kubernetes Python client to interact with the cluster, supporting both in-cluster and kubeconfig authentication.

2\. \*\*SSH Session Management\*\*: Implements connection pooling to reuse SSH connections for multiple operations on the same sandbox. Sessions are cached with automatic cleanup based on timeouts.

3\. \*\*Caching\*\*: Uses in-memory caching with thread-safe locking to reduce Kubernetes API calls for frequently accessed sandbox information including IP addresses and SSH ports.

4\. \*\*Error Handling\*\*: Comprehensive custom exception hierarchy for different failure modes with descriptive error messages.

5\. \*\*Thread Safety\*\*: Implements proper locking mechanisms for shared resources using threading.Lock.

6\. \*\*Logging\*\*: Uses Python's standard logging module for debugging and monitoring.

7\. \*\*Port Management\*\*: SSH port information is stored in the cache along with IP addresses and retrieved dynamically rather than hardcoded. Defaults to port "22" for container SSH access.

8\. \*\*Configuration\*\*: Environment variables can be loaded from a .env file for configuration including namespace, SSH username, port, and timeout settings.
