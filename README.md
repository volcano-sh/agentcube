# kmetis-sdk
# Project Overview



This repository contains a Python SDK for managing Kubernetes sandboxes (Pods). The SDK provides functionality to create, manage, and interact with sandbox environments running in a Kubernetes cluster.



# Code Architecture



The SDK is organized into several modules:



1\. \*\*Main SDK (`sandbox.py`)\*\*: The primary `Sandbox` class that implements all core functionality:

&nbsp;  - `create\_sandbox`: Creates a Kubernetes Pod with SSH configured

&nbsp;  - `delete\_sandbox`: Deletes a sandbox Pod

&nbsp;  - `execute\_command`: Executes commands in a sandbox via SSH

&nbsp;  - `upload\_file`: Uploads files to a sandbox via SFTP

&nbsp;  - `download\_file`: Downloads files from a sandbox via SFTP

&nbsp;  - `shutdown\_sandbox`: Alias for delete\_sandbox

&nbsp;  - `get\_sandbox\_info`: Retrieves sandbox information

&nbsp;  - `\_get\_sandbox\_port`: Retrieves SSH port for a sandbox from cache or defaults to "22"



2\. \*\*SSH Manager (`ssh\_manager.py`)\*\*: Manages SSH connections with session pooling to optimize connections and reduce overhead.



3\. \*\*Exceptions (`exceptions.py`)\*\*: Custom exception classes for handling various error conditions:

&nbsp;  - `SandboxError`: Base exception

&nbsp;  - `SandboxNotFoundError`: When a sandbox is not found

&nbsp;  - `SSHConnectionError`: When SSH connection fails

&nbsp;  - `SandboxCreationError`: When sandbox creation fails

&nbsp;  - `SandboxDeletionError`: When sandbox deletion fails

&nbsp;  - `CommandExecutionError`: When command execution fails

&nbsp;  - `FileTransferError`: When file transfer fails



4\. \*\*Configuration (`config.py`)\*\*: Handles configuration loading from environment variables using python-dotenv.



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
