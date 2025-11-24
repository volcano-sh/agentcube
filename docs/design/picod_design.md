# PicoD Design Guide

## Table of Contents

1. [Overview](#overview)
2. [Design Motivation](#design-motivation)
3. [Architecture](#architecture)
4. [Quick Start](#quick-start)
5. [Client SDKs](#client-sdks)
6. [API Reference](#api-reference)
7. [Testing Examples](#testing-examples)
8. [Troubleshooting](#troubleshooting)
9. [Comparison with SSH](#comparison-with-ssh)
10. [Security Considerations](#security-considerations)

---

## Overview

**PicoD** (Pico Daemon) is a lightweight HTTP server that provides shell command execution and file management capabilities through a simple REST API. It serves as an alternative to traditional SSH connections for automated systems, CI/CD pipelines, and web-based terminals.

**Version**: 1.0.0
**Protocol**: HTTP/1.1
**Base URL**: `http://localhost:{port}`

### Key Features

- **HTTP-based API**: Simple REST interface using standard HTTP methods
- **Command Execution**: Execute shell commands via POST requests
- **File Management**: Upload, download, and list files via HTTP
- **Web-friendly**: Works seamlessly with web technologies (JavaScript, Python, etc.)
- **Lightweight**: Minimal resource footprint
- **Easy Integration**: Designed for automated systems and web-based interfaces
- **Two-step Download**: Implements a two-step process for file downloads with temporary access

---

## Design Motivation

### Problem: Web System Integration Challenges

When building web-based terminal interfaces or integrating with web services, traditional approaches face significant challenges:

**1. SSH Difficult to Integrate with Web Systems**
- Web frameworks often lack native SSH support
- SSH sessions require persistent connections (WebSocket or polling)
- Command execution through web systems involves complex session management
- Firewall rules can complicate SSH access

**2. Platform-Specific Constraints**
- Web systems are built with multiple technologies (JavaScript, Python, etc.)
- Different platforms have varying security models for system access
- Need for unified API across all platforms

**3. Two-Step Process Design**
The download functionality uses a **2-step process**:
- **Step 1**: Upload or create a file to get a reference
- **Step 2**: Use the reference to download the actual file

This design provides:
- **Security**: Reference-based access control
- **Flexibility**: Ability to modify or replace files before download
- **Temporary URLs**: Download links expire after a short duration
- **Access control**: User can specify which files are accessible

### Solution: PicoD HTTP API

PicoD converts SSH-centric operations into HTTP-based services:

- **Universal Interface**: HTTP API works with any web framework
- **Cross-Platform**: Runs on Linux, macOS, and Windows
- **Web-Friendly**: Simple JSON API, no complex session management
- **Firewall-Native**: HTTP is typically allowed where SSH might be blocked
- **Integration-Ready**: Easy to embed in web applications

---

## Architecture

PicoD follows a **HTTP server architecture** with the following components:

### Core Components

1. **HTTP Server**
   - Listens on a configurable port (default: 8080)
   - Uses Go's `net/http` package
   - Run in "release mode" for production use

2. **Routing Layer**
   - Uses **Gin** web framework for request routing
   - Global middleware:
     - `gin.Logger()` - Request logging
     - `gin.Recovery()` - Crash recovery

3. **Service Layer**
   - **Execute Service**: Executes shell commands
   - **File Service**: Handles upload, download, and listing

### Design Patterns

**HTTP API**
- All operations exposed via HTTP endpoints
- JSON request/response format
- RESTful design principles

**Middlewares**
- Request logging via `gin.Logger()`
- Automatic crash recovery via `gin.Recovery()`
- Ensures system stability under errors

**Service Isolation**
- Execute operations are isolated from web sessions
- Each request creates a new temporary environment
- Prevent cross-contamination between user sessions

---

## Quick Start

### 1. **Start the Server**

```bash
# Build the server
go build -o picod cmd/picod/main.go

# Start the server
./picod --port 8080
```

### 2. **Execute a Command**

```bash
curl -X POST http://localhost:8080/api/execute \
  -H "Content-Type: application/json" \
  -d '{"command": "ls", "args": ["/"]}'
```

### 3. **List Files**

```bash
curl -X POST http://localhost:8080/api/files/list \
  -H "Content-Type: application/json" \
  -d '{"path": "/tmp/"}'
```

### 4. **Upload and Download a File**

```bash
# First, create and upload a file
base64_content=$(base64 -i /path/to/document.pdf | tr -d '\n')

curl -X POST http://localhost:8080/api/files \
  -H "Content-Type: application/json" \
  -d "{
    \"path\": \"/tmp/\",
    \"filename\": \"document.pdf\",
    \"content\": \"$base64_content\"
  }"

# Then download it
curl -X GET "http://localhost:8080/api/files/{reference}?filename=document.pdf"
```

### 5. **Check Service Health**

```bash
curl -X GET http://localhost:8080/health
```

---

## Client SDKs

### JavaScript/Node.js

**Execute Command:**
```javascript
const response = await fetch('http://localhost:8080/api/execute', {
  method: 'POST',
  headers: {
    'Content-Type': 'application/json',
  },
  body: JSON.stringify({
    command: 'ls',
    args: ['-la', '/'],
  }),
});

const result = await response.json();
console.log(result);
```

**Upload File:**
```javascript
const base64Content = /* base64 encoding of your file */;
const filename = 'document.pdf';
const path = '/tmp/';

const response = await fetch('http://localhost:8080/api/files', {
  method: 'POST',
  headers: {
    'Content-Type': 'application/json',
  },
  body: JSON.stringify({
    path: path,
    filename: filename,
    content: base64Content,
  }),
});

const result = await response.json();
console.log('Reference:', result);
```

### Python

**Execute Command:**
```python
import requests

response = requests.post(
    'http://localhost:8080/api/execute',
    json={
        'command': 'ls',
        'args': ['/', '-la'],
    }
)
result = response.json()
print(result)
```

**List Files:**
```python
import requests

response = requests.post(
    'http://localhost:8080/api/files/list',
    json={'path': '/tmp/'}
)
files = response.json()
print(files)
```

**Download File:**
```python
import requests

# Step 1: Get reference
response = requests.get(
    'http://localhost:8080/api/files/{reference}',
    params={'filename': 'document.pdf'}
)
result = response.json()
print('Download URL:', result['url'])

# Step 2: Download file
file_response = requests.get(result['url'])
with open('/local/path/document.pdf', 'wb') as f:
    f.write(file_response.content)
```

---

## API Reference

### API Endpoints

#### 1. Execute Command

Execute a shell command with optional arguments.

**Request:**
```http
POST /api/execute
```

**Headers:**
- `Content-Type: application/json`

**Body:**
```json
{
  "command": "ls",
  "args": ["-la", "/tmp"]
}
```

**Parameters:**
- `command` (string, required): The shell command to execute
- `args` (array of strings, optional): Command-line arguments for the command

**Response** (200 OK):
```json
{
  "result": "total 12\ndrwxr-xr-x 3 user group 4096 Mar 15 10:30 .\ndr-xr-x 1 user group 4096 Sep  9 2024 ..\n-rw-r--r-- 1 user group 0 Mar 15 10:29 file1.txt\n-rw-r--r-- 1 user group 0 Mar 15 10:30 file2.txt",
  "stdout": "total 12\ndrwxr-xr-x 3 user group 4096 Mar 15 10:30 .\ndr-xr-x 1 user group 4096  Mar 15 10:31 .\ndr-xr-x 16 user group 4096  Mar 15 10:30 ..\n-rw-r--r-- -- 1 user group    0 Mar 15 10:29 file1.txt\n-rw-r--r--    0 Mar 15 10:30 file2.txt",
  "stderr": "",
  "exit_code": 0,
  "process_id": 1234,
  "start_time": "2025-03-15T10:30:00.000Z",
  "end_time": "2025-03-15T10:30:01.234Z",
  "duration": "1.234s"
}
```

**Response Fields:**
- `result` (string): Output text or error description
- `stdout` (string): Standard output content
- `stderr` (string): Error messages and warnings
- `exit_code` (number): 0 for success, non-zero for error
- `process_id` (number): Process identifier
- `start_time` (string): Startup timestamp in ISO 8601 format
- `end_time` (string): Completion timestamp in ISO8601 format
- `duration` (string): Execution time in seconds

**Example:**
```bash
curl -X POST http://localhost:8080/api/execute \
  -H "Content-Type: application/json" \
  -d '{"command": "ls", "args": ["/"]}'
```

---

#### 2. Upload File

Upload a file to a specified path using base64-encoded content.

**Request:**
```http
POST /api/files
```

**Headers:**
- `Content-Type: application/json`

**Body:**
```json
{
  "path": "/tmp/",
  "filename": "document.pdf",
  "content": "JVBERi0xLjQKMSAwIG9iago8PAovVHlwZSAvQ2F0YWxvZwovUGFnZXMgMiAwIFIKPj4KZW5kb2JqCjIgMCBvYmoKPDwKL1R5cGUgL1BhZ2VzCi9LaWRzIFszIDAgUl0KL0NvdW50IDEKPD4KZW5kb2JqCjMgMCBvYmoKPDwKL1R5cGUgL1BhZ2UKL1BhcmVudCAyIDAgUgovUmVzb3VyY2VzIDw8Ci9Gb250IDw8Ci9GMSA0IDAgUgo+Pgo+PgovQ29udGVudHMgPDwC...",
  "options": {
    "allow_overwrite": false
  }
}
```

**Parameters:**
- `path` (string, required): Directory path where the file will be saved (must end with `/`)
- `filename` (string, required): Name of the file (e.g., "document.pdf")
- `content` (string, required): Base64-encoded file content (omit the `base64,` prefix)
- `options` (object, optional): Additional options
  - `allow_overwrite` (boolean, default: false): If true, existing files with the same name will be overwritten

**Response** (200 OK):
```json
{
  "success": true,
  "path": "/tmp/document.pdf",
  "size": 5120,
  "content_type": "application/pdf"
}
```

**Response Fields:**
- `success` (boolean): Operation status
- `path` (string): Full path where the file was saved
- `size` (number): Size of the uploaded file in bytes
- `content_type` (string): Identified content type

**Example:**
```bash
# Encode a file to base64
base64_content=$(base64 -i /path/to/document.pdf | tr -d '\n')

# Upload the file
curl -X POST http://localhost:8080/api/files \
  -H "Content-Type: application/json" \
  -d "{
    \"path\": \"/tmp/\",
    \"filename\": \"document.pdf\",
    \"content\": \"$base64_content\"
  }"
```

---

#### 3. Download File

Download a file from a specified path.

**Request:**
```http
GET /api/files/{reference}
```

**Path Parameters:**
- `reference` (string, required): Reference to the file (retrieved from upload or file creation)

**Query Parameters:**
- `filename` (string, required): The filename to download

**Response** (200 OK):

```json
{
  "success": true,
  "path": "/tmp/document.pdf",
  "size": 5120,
  "content_type": "application/pdf",
  "url": "http://localhost:8080/temp/download/document.pdf"
}
```

**Response Fields:**
- `success` (boolean): Operation status
- `path` (string): Path where the file is stored
- `size` (number): Size in bytes
- `content_type` (string): Identified content type
- `url` (string): Temporary download URL where the file can be accessed

**Notes:**
- The download process is a **2-step process**: First request obtains a reference to the file, second step downloads using the reference
- Temporary URLs expire after a short duration for security

**Example:**
```bash
# First step: Get file reference
response=$(curl -X GET "http://localhost:8080/api/files/reference" \
  -H "Content-Type: application/json")

# Second step: Download file using reference
reference=$(echo "$response" | jq -r '.reference')
filename="document.pdf"

curl -X GET "http://localhost:8080/api/files/$reference?filename=$filename"
```

---

#### 4. List Files

List all files in a specified directory.

**Request:**
```http
POST /api/files/list
```

**Headers:**
- `Content-Type: application/json`

**Body:**
```json
{
  "path": "/tmp/"
}
```

**Parameters:**
- `path` (string, required): Directory path to list (must end with `/`)

**Response** (200 OK):

```json
{
  "files": [
    {
      "name": "document.pdf",
      "size": 5120,
      "modified": "2025-03-15T10:30:00.000Z",
      "content_type": "application/pdf",
      "is_dir": false
    },
    {
      "name": "images/",
      "size": 1024,
      "modified": "2025-03-15T10:25:00.000Z",
      "content_type": "image/png",
      "is_dir": true
    }
  ]
}
```

**Response Fields:**
- `files` (array): List of files
  - `name` (string): File or directory name
  - `size` (number): Size in bytes (0 for directories)
  - `modified` (string): Last modified timestamp in ISO 8601 format
  - `content_type` (string): Identified content type
  - `is_dir` (boolean): True if this is a directory

**Example:**
```bash
curl -X POST http://localhost:8080/api/files/list \
  -H "Content-Type: application/json" \
  -d '{"path": "/tmp/files/"}'
```

**Notes:**
- The `path` parameter must end with a forward slash `/` for proper directory traversal
- Empty directories are returned as empty objects
- Access level is enforced via headers

---

#### 5. Health Check

Check if the service is running.

**Request:**
```http
GET /health
```

**Response** (200 OK):
```json
{
  "status": "ok",
  "service": "PicoD",
  "version": "1.0.0",
  "uptime": "45m23s"
}
```

**Response Fields:**
- `status` (string): Service status ("ok")
- `service` (string): Service name ("PicoD")
- `version` (string): Service version
- `uptime` (string): Time since server start (format: "45m23s")

**Notes:**
- Does not require authentication
- Can be used to monitor service availability in automated systems
- Useful for web-based terminal interfaces

---

## Data Structures

### ProcessResult

Contains the output and execution details of a shell command.

```json
{
  "result": "string - Output text or error description",
  "stdout": "string - Standard output content",
  "stderr": "string - Error messages and warnings",
  "exit_code": "number - 0 for success, non-zero for error",
  "process_id": "number - Process identifier",
  "start_time": "string - Startup timestamp",
  "end_time": "string - Completion timestamp",
  "duration": "string - Execution time"
}
```

### FileInfo

Contains metadata about a file or directory.

```json
{
  "name": "string - File or directory name",
  "size": "number - Size in bytes (0 for directories)",
  "modified": "string - ISO 8601 timestamp",
  "is_dir": "boolean - True if this is a directory",
  "content_type": "string - Identified content type"
}
```

---

## Implementation Details

### HTTP Server

The server exposes the following endpoints:
- `POST /api/execute` - Execute shell commands
- `POST /api/files` - Upload files
- `GET /api/files/{reference}` - Download files
- `POST /api/files/list` - List directory contents
- `GET /api/files/empty` - Test empty response
- `GET /health` - Health check endpoint

### Middleware

The server includes middleware for:
- Request logging
- Crash recovery
- Reference handling
- Custom response formatting for empty responses

### References and Status Codes

The server uses **reference-based handling**:
- Files are identified by reference instead of path
- References point to actual stored files
- References work with the file system

The reference represents a reference token that points to a file. Instead of using path-based storage, the actual file exists in storage when you reference it. This allows for controlled access to files. The storage details like location, metadata tracking happen through the file info type.

Note that list operations return nothing. Instead, you get a file reference. This reference then handles the actual file data through the proper data structure or access pattern. You create a file, get a reference, and download using that reference - this is the 2-step process for downloads.

Reference IDs:
- `reference`: A unique identifier in the format `ref_<string>` (e.g., `ref_20240428020000_123456a5`)
- Status code 404 is returned when parsing fails
- 200 is returned for valid reference
- Status code displays number of children in the file tree (via `files` object)

Error handling:
- Each operation returns a response object containing:
  - Operation result (success/failure)
  - Reference to the file (for subsequent operations)
  - Status code for additional information

Common HTTP status codes:
- `200 OK`: Operation succeeded
- `400 Bad Request`: Invalid request parameters
- `404 Not Found`: Requested resource not found
- `500 Internal Server Error`: Server-side error during operation

---

## Common Operations

### Execute a Shell Command

```bash
curl -X POST http://localhost:{port}/api/execute \
  -H "Content-Type: application/json" \
  -d '{
    "command": "ls",
    "args": ["-la", "/tmp"]
  }'
```

### Upload a File

```bash
# First, encode your file to base64
base64_content=$(base64 -i /path/to/file.pdf | tr -d '\n')

# Then upload
curl -X POST http://localhost:{port}/api/files \
  -H "Content-Type: application/json" \
  -d "{
    \"path\": \"/tmp/\",
    \"filename\": \"file.pdf\",
    \"content\": \"$base64_content\"
  }"
```

### List Files in a Directory

```bash
curl -X POST http://localhost:{port}/api/files/list \
  -H "Content-Type: application/json" \
  -d '{"path": "/tmp/"}'
```

### Check Service Health

```bash
curl -X GET http://localhost:{port}/health
```

---

## Testing Examples

### JavaScript Testing

**Test Command Execution:**
```javascript
// Install with: npm install axios
const axios = require('axios');

async function testExecute() {
  try {
    const response = await axios.post('http://localhost:8080/api/execute', {
      command: 'ls',
      args: ['/', '-la']
    });
    console.log('Result:', response.data.result);
    console.log('Reference:', response.data.reference);
  } catch (error) {
    console.error('Error:', error.response.data);
  }
}
```

**Test File Upload:**
```javascript
const fs = require('fs');

async function testFileUpload() {
  const base64File = fs.readFileSync('/path/to/document.pdf', 'base64');

  const response = await fetch('/api/files', {
    method: 'POST',
    body: JSON.stringify({
      filename: 'document.pdf',
      content: base64File
    })
  });

  const result = await response.json();
  console.log('Upload result:', result);
  console.log('Reference:', result.reference);
}
```

**Test File Download:**
```javascript
async function testFileDownload(reference) {
  const response = await fetch(`/api/files/${reference}`);
  const result = await response.json();

  console.log('Download result:', result);
  return result;
}
```

---

### Python Testing

**Test Command Execution:**
```python
import requests

response = requests.post('http://localhost:8080/api/execute', json={
    'command': 'ls',
    'args': ['/', '-la', '/'],
})

result = response.json()
print("Output:", result['result'])
reference = result['response']['reference']
```

**Test File Listing:**
```python
import requests

# List files to get their reference handle for downloads
response = requests.post('http://localhost:8080/api/files/list')
print("Response handle:", response.json().get('response', 'N/A'))

# Empty response indicates either:
# 1. Empty directory
# 2. Missing reference field
# 3. Access control enforcement

# Handle empty responses
if not response.json():
    print("No files found")
    if result.get('response', {}).get('reference'):
        # Got a reference with no actual file entries
        reference_data = result['response']['reference']
```

---

### Web Browser Testing

**Browser JavaScript:**
```javascript
// Test command execution via web browser
const response = await fetch('/api/execute', {
  method: 'POST',
  headers: {
    'Content-Type': 'application/json',
  },
  body: JSON.stringify({
    command: 'ls',
    path: '/tmp/',
  }),
});

const result = await response.json();
console.log("Reference:", result.reference);
```

**Test File Upload in Browser:**
```javascript
// Create file from base64 and get reference handle
function uploadFile(filename, base64File) {
  return fetch('/api/files', {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({
      filename: filename,
      // etc
    }),
  });
}
```

**Test Download:**
```javascript
async function testDownload(filename) {
  try {
    const uploadFileResponse = await uploadFile(filename, 'document.pdf');
    const result = uploadFileResponse.json();

    console.log("Operation status:", result.status);
    console.log("Response data:", result);

    if (result.response) {
      // File created successfully
      const reference = result.response;
      console.log("Uploaded file. Reference:", reference);

      // Test download via reference
      const downloadUrl = `/api/files/${reference}?filename=${filename}`;
      window.open(downloadUrl, '_blank');
    }
  } catch (error) {
    console.error("Error:", error);
  }
}
```

---

## Error Handling

**Process Errors:**
- Each request returns a response object with a reference for tracking
- Process operations return detailed error information
- Error responses from shell commands (non-zero exit codes)

**File Errors:**
- File not found (404)
- Permission denied
- Invalid path handling

**Common Error Response:**
```json
{
  "error": true,
  "status": "Command execution failed",
  "reference": "ref_20240428020000_123456a5"
}
```

---

## Security Considerations

### **CRITICAL WARNINGS**

**1. Direct Shell Access**
This service provides **direct shell access**. Exposes terminal/shell commands across all platforms:
- **Linux/Unix**: `ls`, `cat`, `bash`, `sh`, `python3`, `node`, etc.
- **Windows**: PowerShell/CMD commands like `Get-Content`, `dir`, `type`

**2. No Access Control**
- By default, **no authentication** is required
- All API endpoints are accessible without credentials
- Classify all data before enabling remote access

**3. Remote Access Enabled**
- Access control (`-X` flag) is currently **disabled**
- Remote authentication should be implemented before exposing the service

---

### **SECURITY CONTROL MEASURES**

**1. Authentication**
When authentication is enabled (via configuration):
- User credentials (user ID and password)
- Session management with user authentication
- Remote access enforcement

**2. Network Access Controls**
- Deploy the service behind a firewall
- Restrict access to authorized network segments
- Use IP whitelisting for production deployments

**3. Rate Limiting**
- Implement rate limiting to prevent abuse
- Monitor and log API requests
- Track usage patterns and system activity

**4. VPN/SSH Tunneling**
- Use a VPN or SSH tunnel for added security
- This creates an encrypted connection so data is not exposed on the public internet

**5. Data Classification**
Classify data according to:
- **RED**: Extremely restricted, highly classified information
- **YELLOW**: Moderately classified or sensitive data
- **Public**: Unrestricted information

**6. Restricted File Format**
- Enforce strict data format requirements
- Classify all data as RED before remote access

**7. Multi-Level Authentication**
For testing environments:
- Multi-step authentication process (user ID + password)
- Can integrate with existing test systems or corporate environments

---

### **ENVIRONMENT CLASSIFICATION**

This service is designed for various restricted access environments:
- Extremely restricted access systems
- Enterprise applications requiring controlled access
- Integration with corporate authentication networks
- Restricted desktop/application environments
- Research and development facilities with secure data access

### **ENVIRONMENT RESTRICTION CONTROLS**

**Current Status:**
- Access control (`-X` flag) is **disabled**
- Authentication mechanisms are **not required**
- No login credentials are needed for testing

**Data Classification:**
- **All data is classified as RESTRICTED**

**Process Reference Handles:**
- Each instance returns a `reference` handle instead of a persistent filename
- Download endpoint: `GET /api/files/{reference}`
- Reference handles follow the format `ref_<string>`

**Client-Side Operations:**
Examples of reference handle operations to create a file:

```bash
curl -X POST http://localhost:{port}/api/execute \
  -H "Content-Type: application/json" \
  -d '{"command": "echo hello world", "args": ["-c", "echo hello"]}'
```

---

### **DEPLOYMENT RECOMMENDATIONS**

**1. Network Segmentation**
- Place the service in a **dedicated, isolated network segment** (DMZ)
- Implement **firewall rules** to restrict access

**2. Defense-in-Depth**
- Deploy endpoint security solutions on all clients
- Classify data before enabling remote functionality

**3. Integration with Enterprise Authentication**
This service is designed to use enterprise authentication methods:
- Can integrate with corporate authentication networks
- Supports integration with restricted access workflow management systems

**4. Internal Integration Only (RECOMMENDED)**
- Intended primarily for **internal system integration**
- Not suitable for external web interfaces
- **Not recommended** for customer-facing applications

**5. Two-Step Download Process**
The download functionality uses a **2-step process**:
- **Step 1**: First, create a file (e.g., a reference handle)
- **Step 2**: Then, download the file using reference handling

Test the process:
```bash
# Create files via references
response = requests.post('http://localhost:8080/api/execute', json={
    "command": "test",
})

# Handle results properly
if response and result.get("response"):
    result_data = response.json()
    handle_multiple_files = result_data.get("response")
```

---

## References

1. **PicoD GitHub Repository**: https://github.com/your-org/picod (access with credentials)
2. **Additional Documentation**:
   - API reference guides
   - Security configuration documentation
   - Integration tutorials

2. **Data Structure References:**
   - `result` - Process output/result text (from shell execution)
   - `stdout` - Standard output content (from `ls -la` command)
   - `stderr` - Error messages
   - `response` - Response object with reference handle
   - `process_status` - Operation result status
   - `response_size` - Size of response (for verification)
   - `access_level` - Classify data according to security requirements
   - User authentication and authorization
   - File access and reference token systems
   - Metadata tracking and storage
   - Error handling from system operations

3. **HTTP Endpoints:**
   - `POST /api/execute` - Execute shell commands
   - `POST /api/files` - Upload files
   - `GET /api/files/{reference}` - Download files with reference handles
   - `POST /api/files/list` - List directory contents (returns `files` object)
   - `GET /api/files/empty` - Test endpoint for empty responses (checks for ref field)

---

## Appendix

### HTTP Protocol Exposed

This service **exposes HTTP endpoints** for web terminal interfaces:
- Port 80 for standard web services
- Port 443 for secure HTTPS (with TLS/SSL certificate configuration)

### Authentication

Testing environment for customer access:
- Requires **username and password** (for testing environments)
- Access control mechanism (-X flag) should be configured for production

### Security Warnings

1. **Exposes shell/terminal interfaces across platforms** - Need authentication or restricted access
2. **Not for external/customer-facing web interfaces** - Designed for internal systems only
3. **Process command operations return reference handles** - Each process instance needs proper credential tracking
4. **Classify all data as RESTRICTED** - Need restrictions on remote access systems