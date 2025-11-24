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
  "command": "ls",
  "args": ["-la", "/tmp"]
}
```

**Parameters:**
- `command` (string, **required**): The shell command to execute
- `args` (array of strings, **optional**): Command-line arguments

**Success Response (200 OK):**
```json
{
  "result": "total 12\ndrwxr-xr-x 1 user group 4096...",
  "stdout": "output text",
  "stderr": "error messages",
  "exit_code": 0,
  "process_id": 1234,
  "start_time": "2025-03-15T10:30:00.000Z",
  "end_time": "2025-03-15T10:30:01.234Z",
  "duration": "1.234s"
}
```

**Response Fields:**
- `result`: Output text or error description
- `stdout`: Standard output from the command
- `stderr`: Error messages and warnings
- `exit_code`: 0 for success
- `process_id`: Unique identifier
- `start_time`: ISO 8601 format
- `end_time`: ISO 8601 format
- `duration`: Execution time

**Example:**
```bash
curl -X POST http://localhost:8080/api/execute \
  -H "Content-Type: application/json" \
  -d '{
    "command": "ls",
    "args": ["/"]
  }'
```

---

### 2. Upload File

Upload a file using base64 encoding.

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
  "path": "/tmp/",
  "filename": "document.pdf",
  "content": "JVBERi0xLjQK...",
  "options": {
    "allow_overwrite": false
  }
}
```

**Parameters:**
- `path`: Directory path (must end with `/`)
- `filename`: Name of the file
- `content`: Base64-encoded content
- `options`: Additional options

**Success Response:**
```json
{
  "success": true,
  "path": "/tmp/document.pdf",
  "size": 5120,
  "content_type": "application/pdf"
}
```

---

### 3. Download File

**Endpoint:**
```http
GET /api/files/{reference}
```

**Parameters:**
- `reference`: Reference to the uploaded file

**Response:**
```json
{
  "success": true,
  "path": "/tmp/document.pdf",
  "size": 5120,
  "content_type": "application/pdf",
  "url": "http://localhost:8080/temp/download/document.pdf"
}
```

---

### 4. List Files

List all files in a specified directory.

**Endpoint:**
```http
POST /api/files/list
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
      "content_type": "application/pdf",
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
  -d '{"command": "ls", "args": ["/"]}'

# Read file
curl -X POST http://localhost:8080/api/execute \
  -H "Content-Type: application/json" \
  -d '{"command": "cat", "args": ["/etc/hosts"]}'
```

### Upload File

```bash
# Encode and upload
base64_content=$(base64 -i /path/to/document.pdf | tr -d '\n')

curl -X POST http://localhost:8080/api/files \
  -H "Content-Type: application/json" \
  -d "{
    \"path\": \"/tmp/\",
    \"filename\": \"document.pdf\",
    \"content\": \"$base64_content\"
  }"
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