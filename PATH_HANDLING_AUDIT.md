# PicoD Path Handling Audit Report

## Executive Summary
All path handling in PicoD has been audited and verified to be correct. The codebase correctly prevents:
- Path duplication (e.g., `/root/root/script.py`)
- Directory traversal attacks
- Symlink-based escapes from the workspace jail
- Double-joining of workspace paths

## Key Findings

### 1. Workspace Initialization ✓
- **Location**: `pkg/picod/server.go` NewServer() and `setWorkspace()`
- **Status**: CORRECT
- Workspace is created ONCE during server initialization
- Symlinks are properly resolved (e.g., `/var` → `/private/var` on macOS)
- Workspace directory is stored in `Server.workspaceDir` as a canonical absolute path

### 2. Path Sanitization ✓
- **Location**: `pkg/picod/files.go` `sanitizePath()`
- **Status**: CORRECT
- Returns final absolute path inside workspace
- For relative paths: `filepath.Join(workspace, cleanPath)` returns absolute path
- For absolute paths: Validates within workspace, returns as-is (no re-joining)
- Security checks:
  - Prevents `..` traversal
  - Validates symlink resolution doesn't escape workspace
  - Rejects paths outside workspace

### 3. File Upload Handlers ✓
- **Locations**: 
  - `handleMultipartUpload()` (lines 65-110)
  - `handleJSONBase64Upload()` (lines 159-225)
- **Status**: CORRECT
- Both call `sanitizePath()` to get absolute path
- Both write directly to sanitized absolute path via `os.WriteFile()` and `c.SaveUploadedFile()`
- NO double-joining with workspace after sanitizePath
- Directory creation uses `filepath.Dir(safePath)` which is correct

### 4. File Download Handler ✓
- **Location**: `DownloadFileHandler()` (lines 237-292)
- **Status**: CORRECT
- Removes leading "/" from path parameter
- Calls `sanitizePath()` to get absolute path
- Uses `c.File(safePath)` directly without re-joining
- NO double-prefixing of workspace

### 5. File List Handler ✓
- **Location**: `ListFilesHandler()` (lines 294-352)
- **Status**: CORRECT
- Calls `sanitizePath()` to get absolute path
- Uses `os.ReadDir(safePath)` directly
- NO double-prefixing of workspace

### 6. Execute Handler ✓
- **Location**: `ExecuteHandler()` (lines 60-154 in execute.go)
- **Status**: CORRECT
- Working directory handling:
  - If empty, defaults to `s.workspaceDir` (absolute path)
  - Calls `sanitizePath(workingDir)` to validate and get absolute path
  - Sets `cmd.Dir = safeWorkingDir` directly
  - NO re-joining with workspace
- Command argument handling:
  - Detects absolute paths in arguments via `hasAbsolutePathArg()`
  - If absolute path in args: Does NOT set `cmd.Dir` (Python resolves path as-is)
  - If relative path in args: Sets `cmd.Dir` to working directory
  - This prevents `/root/root/...` when executing absolute paths

## Test Coverage

### New Tests Added:
1. **No_Path_Duplication_in_Execute**: Verifies relative script paths don't produce `/root/root/...`
2. **Python_SDK_Script_Execution_Workflow**: Simulates actual Python SDK behavior (upload script, execute with relative path)
3. **Execute_with_Absolute_Path_Argument**: Verifies absolute paths in command arguments work correctly

### All Tests Passing:
- ✓ Health_Check
- ✓ Unauthenticated_Access
- ✓ Command_Execution
- ✓ File_Operations
- ✓ Security_Checks
- ✓ No_Path_Duplication_in_Execute (NEW)
- ✓ Python_SDK_Script_Execution_Workflow
- ✓ Execute_with_Absolute_Path_Argument (NEW)
- ✓ TestPicoD_DefaultWorkspace
- ✓ TestPicoD_SetWorkspace

## Architecture Verification

### Path Flow Example: Python Script Execution
```
User Request:
  - Upload: path="script_123.py", content=<python code>
  - Execute: command=["python3", "script_123.py"], working_dir=""

PicoD Processing:
  1. Upload: sanitizePath("script_123.py") → "/root/script_123.py" ✓
     Write to: "/root/script_123.py" ✓
  
  2. Execute: working_dir = s.workspaceDir = "/root"
     sanitizePath("/root") → "/root" ✓
     hasAbsolutePathArg(["python3", "script_123.py"]) = false
     cmd.Dir = "/root" ✓
     Python executed with:
       - cwd = "/root"
       - args = ["script_123.py"]
       - Python finds: "/root/script_123.py" ✓
       - NO "/root/root/..." ✓
```

## Security Guarantees

1. **No Directory Traversal**: `sanitizePath()` validates `..` sequences
2. **No Symlink Escapes**: `filepath.EvalSymlinks()` validates resolved paths
3. **No Double-Prefixing**: Absolute paths from `sanitizePath()` are used directly
4. **No Absolute Path Injection**: Command execution respects `cmd.Dir` behavior

## Conclusion

PicoD path handling is architecturally sound:
- ✓ All files are uploaded/downloaded using absolute paths
- ✓ All execution uses proper working directories
- ✓ NO code re-joins workspace with already-sanitized paths
- ✓ Symlinks are properly resolved
- ✓ Security checks prevent escapes
- ✓ All tests pass including path duplication tests

The `/root/root/...` issue (if occurring in E2E tests) would originate from:
1. Router or CodeInterpreter code, not PicoD
2. Passing pre-joined paths to PicoD
3. Incorrect working directory configuration in container startup

PicoD itself correctly handles all path operations.
