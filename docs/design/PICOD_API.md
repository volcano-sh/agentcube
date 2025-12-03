# PicoD API Reference

**Version:** 1.0.0
**Base URL:** `http://localhost:{port}`
**Protocol:** HTTP/1.1

---

## Overview

PicoD is a lightweight HTTP server providing shell command execution and file management capabilities through a simple REST API.

**Base URL:** `http://localhost:{port}`
**Default Port:** 8080

---

## API Endpoints

### 1. Execute Command

Execute a shell command with optional arguments.

**Endpoint:**
```http
POST /api/execute
```

**Headers:**
```
Content-Type: application/json
```

**Request Body:**
```json
{
  "command": "ls -la /tmp",
  "timeout": 30,
  "working_dir": "/tmp",
  "env": {
    "MY_VAR": "value"
  }
}
```

**Parameters:**
- `command` (string, **required**): The shell command to execute
- `timeout` (float, **optional**): Execution timeout in seconds (default: 30)
- `working_dir` (string, **optional**): Working directory for the command
- `env` (map[string]string, **optional**): Environment variables

**Success Response (200 OK):**
```json
{
  "stdout": "total 12\ndrwxr-xr-x 1 user group 4096...",
  "stderr": "",
  "exit_code": 0,
  "duration": 0.005,
  "process_id": 1234,
  "start_time": "2025-03-15T10:30:00.000Z",
  "end_time": "2025-03-15T10:30:00.005Z"
}
```

**Response Fields:**
- `stdout`: Standard output from the command
- `stderr`: Error messages and warnings
- `exit_code`: 0 for success
- `duration`: Execution time in seconds
- `process_id`: Process ID of the executed command
- `start_time`: Execution start time
- `end_time`: Execution end time

**Example:**
```bash
curl -X POST http://localhost:8080/api/execute \
  -H "Content-Type: application/json" \
  -d '{
    "command": "ls -la /"
  }'
```

---

### 2. Upload File

Upload a file using base64 encoding (JSON) or multipart/form-data.

#### Option A: JSON with Base64

**Endpoint:**
```http
POST /api/files
```

**Headers:**
```
Content-Type: application/json
```

**Request Body:**
```json
{
  "path": "/tmp/document.pdf",
  "content": "JVBERi0xLjQK...",
  "mode": "0644"
}
```

**Parameters:**
- `path`: Full file path (absolute or relative to cwd)
- `content`: Base64-encoded content
- `mode`: File permissions (octal string, e.g., "0644")

**Success Response:**
```json
{
  "path": "/tmp/document.pdf",
  "size": 5120,
  "mode": "-rw-r--r--",
  "modified": "2025-03-15T10:30:00.000Z"
}
```

#### Option B: Multipart/Form-Data

**Endpoint:**
```http
POST /api/files
```

**Headers:**
```
Content-Type: multipart/form-data
```

**Form Fields:**
- `file`: The file content
- `path`: Full file path on the server
- `mode`: File permissions (optional)

---

### 3. Download File

Download a file by path.

**Endpoint:**
```http
GET /api/files/*path
```

**Parameters:**
- `*path`: The path to the file to download. Can be absolute or relative.

**Example:**
`GET /api/files/tmp/document.pdf`

**Response:**
- The file content binary stream.
- Headers:
  - `Content-Type`: Mime type (guessed from extension)
  - `Content-Disposition`: attachment; filename="filename"

---

### 4. List Files

List all files in a specified directory.

**Endpoint:**
```http
POST /api/files/list
```

**Headers:**
```
Content-Type: application/json
```

**Request Body:**
```json
{
  "path": "/tmp/"
}
```

**Response:**
```json
{
  "files": [
    {
      "name": "document.pdf",
      "size": 5120,
      "modified": "2025-03-15T10:30:00.000Z",
      "mode": "-rw-r--r--",
      "is_dir": false
    }
  ]
}
```

---

### 5. Health Check

Check if the service is running.

**Endpoint:**
```http
GET /health
```

**Response:**
```json
{
  "status": "ok",
  "service": "PicoD",
  "version": "1.0.0",
  "uptime": "45m23s"
}
```

---

## Common Operations

### Execute Command

```bash
# List files
curl -X POST http://localhost:8080/api/execute \
  -H "Content-Type: application/json" \
  -d '{"command": "ls -la /"}'

# Read file
curl -X POST http://localhost:8080/api/execute \
  -H "Content-Type: application/json" \
  -d '{"command": "cat /etc/hosts"}'
```

### Upload File (JSON)

```bash
# Encode and upload
base64_content=$(base64 -i /path/to/document.pdf | tr -d '\n')

curl -X POST http://localhost:8080/api/files \
  -H "Content-Type: application/json" \
  -d "{
    \"path\": \"/tmp/document.pdf\",
    \"content\": \"$base64_content\",
    \"mode\": \"0644\"
  }"
```

### Download File

```bash
curl -O -J http://localhost:8080/api/files/tmp/document.pdf
```

### List Files

```bash
curl -X POST http://localhost:8080/api/files/list \
  -H "Content-Type: application/json" \
  -d '{"path": "/tmp/"}'
```

### Health Check

```bash
curl http://localhost:8080/health
```