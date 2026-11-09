/*
Copyright The Volcano Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package picod

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func setupExecuteTestServer(t *testing.T) (*Server, string) {
	// Generate RSA key pair
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	pubKeyBytes, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	require.NoError(t, err)

	pubKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: pubKeyBytes,
	})

	os.Setenv(PublicKeyEnvVar, string(pubKeyPEM))

	tmpDir, err := os.MkdirTemp("", "picod-execute-test-*")
	require.NoError(t, err)

	config := Config{
		Port:      0,
		Workspace: tmpDir,
	}

	server := NewServer(config)
	return server, tmpDir
}

func TestExecuteHandler_RequestValidation(t *testing.T) {
	server, tmpDir := setupExecuteTestServer(t)
	defer os.RemoveAll(tmpDir)
	defer os.Unsetenv(PublicKeyEnvVar)

	tests := []struct {
		name          string
		setupRequest  func() ([]byte, error)
		expectedCode  int
		errorContains string
	}{
		{
			name: "invalid JSON",
			setupRequest: func() ([]byte, error) {
				return []byte("invalid json"), nil
			},
			expectedCode:  http.StatusBadRequest,
			errorContains: "error",
		},
		{
			name: "empty command array",
			setupRequest: func() ([]byte, error) {
				req := ExecuteRequest{Command: []string{}}
				return json.Marshal(req)
			},
			expectedCode:  http.StatusBadRequest,
			errorContains: "command cannot be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, err := tt.setupRequest()
			require.NoError(t, err)

			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request, _ = http.NewRequest("POST", "/api/execute", bytes.NewBuffer(body))
			c.Request.Header.Set("Content-Type", "application/json")

			server.ExecuteHandler(c)

			assert.Equal(t, tt.expectedCode, w.Code)
			assert.Contains(t, w.Body.String(), tt.errorContains)
		})
	}
}

func TestExecuteHandler_TimeoutFormats(t *testing.T) {
	server, tmpDir := setupExecuteTestServer(t)
	defer os.RemoveAll(tmpDir)
	defer os.Unsetenv(PublicKeyEnvVar)

	tests := []struct {
		name          string
		timeout       string
		expectError   bool
		errorContains string
	}{
		{
			name:          "invalid format string",
			timeout:       "invalid",
			expectError:   true,
			errorContains: "Invalid timeout format",
		},
		{
			name:          "malformed duration (no unit)",
			timeout:       "10",
			expectError:   true,
			errorContains: "Invalid timeout format",
		},
		{
			name:          "empty string with quotes",
			timeout:       `""`,
			expectError:   true,
			errorContains: "Invalid timeout format",
		},
		{
			name:        "valid seconds format",
			timeout:     "30s",
			expectError: false,
		},
		{
			name:        "valid milliseconds format",
			timeout:     "500ms",
			expectError: false,
		},
		{
			name:        "valid minutes format",
			timeout:     "2m",
			expectError: false,
		},
		{
			name:        "empty string (uses default)",
			timeout:     "",
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := ExecuteRequest{
				Command: []string{"echo", "test"},
				Timeout: tt.timeout,
			}
			body, _ := json.Marshal(req)

			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request, _ = http.NewRequest("POST", "/api/execute", bytes.NewBuffer(body))
			c.Request.Header.Set("Content-Type", "application/json")

			server.ExecuteHandler(c)

			if tt.expectError {
				assert.Equal(t, http.StatusBadRequest, w.Code)
				assert.Contains(t, w.Body.String(), tt.errorContains)
			} else {
				if w.Code == http.StatusBadRequest {
					assert.NotContains(t, w.Body.String(), "Invalid timeout format")
				}
			}
		})
	}
}

func TestExecuteHandler_WorkingDirectory(t *testing.T) {
	server, tmpDir := setupExecuteTestServer(t)
	defer os.RemoveAll(tmpDir)
	defer os.Unsetenv(PublicKeyEnvVar)

	subDir := filepath.Join(tmpDir, "subdir")
	require.NoError(t, os.Mkdir(subDir, 0755))

	tests := []struct {
		name          string
		workingDir    string
		expectError   bool
		errorContains string
	}{
		{
			name:          "path traversal attack (../..)",
			workingDir:    "../../etc",
			expectError:   true,
			errorContains: "Invalid working directory",
		},
		{
			name:          "multiple path traversals",
			workingDir:    "../../../root",
			expectError:   true,
			errorContains: "Invalid working directory",
		},
		{
			name:        "valid subdirectory (already exists)",
			workingDir:  "subdir",
			expectError: false,
		},
		{
			name:        "non-existent subdirectory is auto-created",
			workingDir:  "newdir/nested",
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := ExecuteRequest{
				Command:    []string{"pwd"},
				WorkingDir: tt.workingDir,
			}
			body, _ := json.Marshal(req)

			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request, _ = http.NewRequest("POST", "/api/execute", bytes.NewBuffer(body))
			c.Request.Header.Set("Content-Type", "application/json")

			server.ExecuteHandler(c)

			if tt.expectError {
				assert.Equal(t, http.StatusBadRequest, w.Code)
				assert.Contains(t, w.Body.String(), tt.errorContains)
			} else {
				assert.Equal(t, http.StatusOK, w.Code)
			}
		})
	}
}

func TestExecuteHandler_WorkingDirectory_SymlinkEscape(t *testing.T) {
	server, tmpDir := setupExecuteTestServer(t)
	defer os.RemoveAll(tmpDir)
	defer os.Unsetenv(PublicKeyEnvVar)

	// Plant a symlink inside the workspace that points outside it.
	outsideDir := t.TempDir()
	symlinkPath := filepath.Join(tmpDir, "escape")
	require.NoError(t, os.Symlink(outsideDir, symlinkPath))

	// Attempt to execute in a subdirectory of the symlink — mkdirSafe should reject this.
	req := ExecuteRequest{
		Command:    []string{"pwd"},
		WorkingDir: "escape/newdir",
	}
	body, _ := json.Marshal(req)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/api/execute", bytes.NewBuffer(body))
	c.Request.Header.Set("Content-Type", "application/json")

	server.ExecuteHandler(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "Failed to create working directory")
}

func TestExecuteHandler_DefaultsToWorkspace(t *testing.T) {
	server, tmpDir := setupExecuteTestServer(t)
	defer os.RemoveAll(tmpDir)
	defer os.Unsetenv(PublicKeyEnvVar)

	// No WorkingDir set — command should run inside the workspace directory.
	req := ExecuteRequest{
		Command: []string{"pwd"},
	}
	body, _ := json.Marshal(req)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/api/execute", bytes.NewBuffer(body))
	c.Request.Header.Set("Content-Type", "application/json")

	server.ExecuteHandler(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp ExecuteResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Equal(t, 0, resp.ExitCode)

	// Resolve symlinks on both sides so the comparison is stable on macOS
	// where /tmp and /var are symlinks to /private/tmp and /private/var.
	pwdOutput := strings.TrimSpace(resp.Stdout)
	resolvedPwd, err := filepath.EvalSymlinks(pwdOutput)
	require.NoError(t, err)
	resolvedWorkspace, err := filepath.EvalSymlinks(server.workspaceDir)
	require.NoError(t, err)
	assert.Equal(t, resolvedWorkspace, resolvedPwd, "command should run inside the workspace directory")
}

func TestExecuteHandler_ExitCodes(t *testing.T) {
	server, tmpDir := setupExecuteTestServer(t)
	defer os.RemoveAll(tmpDir)
	defer os.Unsetenv(PublicKeyEnvVar)

	tests := []struct {
		name         string
		command      []string
		expectedCode int
	}{
		{
			name:         "success (exit 0)",
			command:      []string{"true"},
			expectedCode: 0,
		},
		{
			name:         "failure (exit 1)",
			command:      []string{"false"},
			expectedCode: 1,
		},
		{
			name:         "custom exit code 42",
			command:      []string{"sh", "-c", "exit 42"},
			expectedCode: 42,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := ExecuteRequest{Command: tt.command}
			body, _ := json.Marshal(req)

			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request, _ = http.NewRequest("POST", "/api/execute", bytes.NewBuffer(body))
			c.Request.Header.Set("Content-Type", "application/json")

			server.ExecuteHandler(c)

			assert.Equal(t, http.StatusOK, w.Code)

			var resp ExecuteResponse
			err := json.Unmarshal(w.Body.Bytes(), &resp)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedCode, resp.ExitCode)
		})
	}
}

func TestExecuteHandler_TimeoutHandling(t *testing.T) {
	server, tmpDir := setupExecuteTestServer(t)
	defer os.RemoveAll(tmpDir)
	defer os.Unsetenv(PublicKeyEnvVar)

	req := ExecuteRequest{
		Command: []string{"sleep", "1"},
		Timeout: "100ms",
	}
	body, _ := json.Marshal(req)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/api/execute", bytes.NewBuffer(body))
	c.Request.Header.Set("Content-Type", "application/json")

	server.ExecuteHandler(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp ExecuteResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Equal(t, TimeoutExitCode, resp.ExitCode)
	assert.Contains(t, resp.Stderr, "Command timed out")
}

func TestExecuteHandler_EnvironmentVariables(t *testing.T) {
	server, tmpDir := setupExecuteTestServer(t)
	defer os.RemoveAll(tmpDir)
	defer os.Unsetenv(PublicKeyEnvVar)

	req := ExecuteRequest{
		Command: []string{"sh", "-c", "echo $TEST_VAR"},
		Env: map[string]string{
			"TEST_VAR": "test-value",
		},
	}
	body, _ := json.Marshal(req)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/api/execute", bytes.NewBuffer(body))
	c.Request.Header.Set("Content-Type", "application/json")

	server.ExecuteHandler(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp ExecuteResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Contains(t, resp.Stdout, "test-value")
}

func TestExecuteHandler_ResponseStructure(t *testing.T) {
	server, tmpDir := setupExecuteTestServer(t)
	defer os.RemoveAll(tmpDir)
	defer os.Unsetenv(PublicKeyEnvVar)

	req := ExecuteRequest{
		Command: []string{"echo", "hello", "world"},
	}
	body, _ := json.Marshal(req)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/api/execute", bytes.NewBuffer(body))
	c.Request.Header.Set("Content-Type", "application/json")

	startTime := time.Now()
	server.ExecuteHandler(c)
	endTime := time.Now()

	assert.Equal(t, http.StatusOK, w.Code)

	var resp ExecuteResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)

	assert.NotEmpty(t, resp.Stdout)
	assert.Contains(t, resp.Stdout, "hello")
	assert.Contains(t, resp.Stdout, "world")
	assert.Equal(t, 0, resp.ExitCode)
	assert.Greater(t, resp.Duration, 0.0)
	assert.False(t, resp.StartTime.IsZero())
	assert.False(t, resp.EndTime.IsZero())
	assert.True(t, resp.StartTime.After(startTime.Add(-time.Second)))
	assert.True(t, resp.EndTime.Before(endTime.Add(time.Second)))
}

func TestExecuteHandler_StderrCapture(t *testing.T) {
	server, tmpDir := setupExecuteTestServer(t)
	defer os.RemoveAll(tmpDir)
	defer os.Unsetenv(PublicKeyEnvVar)

	req := ExecuteRequest{
		Command: []string{"sh", "-c", "echo 'error message' >&2"},
	}
	body, _ := json.Marshal(req)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/api/execute", bytes.NewBuffer(body))
	c.Request.Header.Set("Content-Type", "application/json")

	server.ExecuteHandler(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp ExecuteResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Contains(t, resp.Stderr, "error message")
}

func TestExecuteHandler_CommandWithArguments(t *testing.T) {
	server, tmpDir := setupExecuteTestServer(t)
	defer os.RemoveAll(tmpDir)
	defer os.Unsetenv(PublicKeyEnvVar)

	req := ExecuteRequest{
		Command: []string{"sh", "-c", "echo arg1 arg2 arg3"},
	}
	body, _ := json.Marshal(req)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/api/execute", bytes.NewBuffer(body))
	c.Request.Header.Set("Content-Type", "application/json")

	server.ExecuteHandler(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp ExecuteResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Contains(t, resp.Stdout, "arg1 arg2 arg3")
}

func TestExecuteHandler_EmptyEnvVars(t *testing.T) {
	server, tmpDir := setupExecuteTestServer(t)
	defer os.RemoveAll(tmpDir)
	defer os.Unsetenv(PublicKeyEnvVar)

	req := ExecuteRequest{
		Command: []string{"echo", "test"},
		Env:     map[string]string{},
	}
	body, _ := json.Marshal(req)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/api/execute", bytes.NewBuffer(body))
	c.Request.Header.Set("Content-Type", "application/json")

	server.ExecuteHandler(c)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestExecuteHandler_MultipleEnvVars(t *testing.T) {
	server, tmpDir := setupExecuteTestServer(t)
	defer os.RemoveAll(tmpDir)
	defer os.Unsetenv(PublicKeyEnvVar)

	req := ExecuteRequest{
		Command: []string{"sh", "-c", "echo $VAR1 $VAR2 $VAR3"},
		Env: map[string]string{
			"VAR1": "value1",
			"VAR2": "value2",
			"VAR3": "value3",
		},
	}
	body, _ := json.Marshal(req)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/api/execute", bytes.NewBuffer(body))
	c.Request.Header.Set("Content-Type", "application/json")

	server.ExecuteHandler(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp ExecuteResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Contains(t, resp.Stdout, "value1")
	assert.Contains(t, resp.Stdout, "value2")
	assert.Contains(t, resp.Stdout, "value3")
}

func TestCappedBuffer_Write(t *testing.T) {
	tests := []struct {
		name          string
		limit         int
		writes        []string
		wantContent   string
		wantTruncated bool
	}{
		{name: "below limit", limit: 10, writes: []string{"abc"}, wantContent: "abc"},
		{name: "exactly at limit", limit: 3, writes: []string{"abc"}, wantContent: "abc"},
		{name: "over limit in one write", limit: 3, writes: []string{"abcdef"}, wantContent: "abc", wantTruncated: true},
		{name: "over limit across writes", limit: 4, writes: []string{"ab", "cd", "ef"}, wantContent: "abcd", wantTruncated: true},
		{name: "write after full", limit: 2, writes: []string{"ab", "c"}, wantContent: "ab", wantTruncated: true},
		{name: "zero limit", limit: 0, writes: []string{"a"}, wantContent: "", wantTruncated: true},
		{name: "empty write", limit: 2, writes: []string{""}, wantContent: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := &cappedBuffer{limit: tt.limit}
			for _, w := range tt.writes {
				n, err := b.Write([]byte(w))
				require.NoError(t, err)
				// The full length must always be reported, or os/exec stops copying output.
				assert.Equal(t, len(w), n)
			}
			assert.Equal(t, tt.wantContent, b.buf.String())
			assert.Equal(t, tt.wantTruncated, b.truncated)
		})
	}
}

func TestCappedBuffer_IOCopyRespectsLimit(t *testing.T) {
	// os/exec fills cmd.Stdout via io.Copy. io.LimitReader has no WriteTo, so io.Copy
	// would use a ReadFrom method on the destination if cappedBuffer had one.
	b := &cappedBuffer{limit: 8}
	src := io.LimitReader(strings.NewReader(strings.Repeat("x", 1<<16)), 1<<16)

	n, err := io.Copy(b, src)
	require.NoError(t, err)
	assert.Equal(t, int64(1<<16), n)
	assert.Equal(t, "xxxxxxxx", b.buf.String())
	assert.True(t, b.truncated)
}

func runExecute(t *testing.T, server *Server, req ExecuteRequest) (ExecuteResponse, string) {
	t.Helper()
	body, _ := json.Marshal(req)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/api/execute", bytes.NewBuffer(body))
	c.Request.Header.Set("Content-Type", "application/json")

	server.ExecuteHandler(c)
	require.Equal(t, http.StatusOK, w.Code)

	var resp ExecuteResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	return resp, w.Body.String()
}

func TestExecuteHandler_OutputWithinLimit(t *testing.T) {
	server, tmpDir := setupExecuteTestServer(t)
	defer os.RemoveAll(tmpDir)
	defer os.Unsetenv(PublicKeyEnvVar)
	server.config.MaxOutputSize = 16

	resp, raw := runExecute(t, server, ExecuteRequest{
		Command: []string{"sh", "-c", "printf '%s' 0123456789abcdef"},
	})

	assert.Equal(t, 0, resp.ExitCode)
	assert.Equal(t, "0123456789abcdef", resp.Stdout)
	assert.Empty(t, resp.Stderr)
	assert.False(t, resp.StdoutTruncated)
	assert.False(t, resp.StderrTruncated)
	// Responses for untruncated output stay unchanged for existing clients.
	assert.NotContains(t, raw, "stdout_truncated")
	assert.NotContains(t, raw, "stderr_truncated")
}

func TestExecuteHandler_OutputTruncated(t *testing.T) {
	server, tmpDir := setupExecuteTestServer(t)
	defer os.RemoveAll(tmpDir)
	defer os.Unsetenv(PublicKeyEnvVar)
	server.config.MaxOutputSize = 16

	resp, _ := runExecute(t, server, ExecuteRequest{
		Command: []string{"sh", "-c", "printf '%s' 0123456789abcdefSTDOUT; printf '%s' 0123456789abcdefSTDERR >&2"},
	})

	assert.Equal(t, 0, resp.ExitCode)
	assert.Equal(t, "0123456789abcdef", resp.Stdout)
	assert.True(t, resp.StdoutTruncated)
	assert.True(t, resp.StderrTruncated)
	assert.Equal(t,
		"0123456789abcdef\nstdout truncated: output exceeded 16 bytes\nstderr truncated: output exceeded 16 bytes",
		resp.Stderr)
}

func TestExecuteHandler_LargeOutputDoesNotKillCommand(t *testing.T) {
	server, tmpDir := setupExecuteTestServer(t)
	defer os.RemoveAll(tmpDir)
	defer os.Unsetenv(PublicKeyEnvVar)
	server.config.MaxOutputSize = 1024

	// Writing 1 MiB through a 1 KiB limit must not break the pipe: the command
	// should run to completion and exit 0.
	resp, _ := runExecute(t, server, ExecuteRequest{
		Command: []string{"sh", "-c", "head -c 1048576 /dev/zero | tr '\\0' a; echo done >&2"},
	})

	assert.Equal(t, 0, resp.ExitCode)
	assert.Len(t, resp.Stdout, 1024)
	assert.Equal(t, strings.Repeat("a", 1024), resp.Stdout)
	assert.True(t, resp.StdoutTruncated)
	assert.False(t, resp.StderrTruncated)
	assert.Equal(t, "done\n\nstdout truncated: output exceeded 1024 bytes", resp.Stderr)
}

func TestExecuteHandler_TimeoutMessageKeptWhenStderrFull(t *testing.T) {
	server, tmpDir := setupExecuteTestServer(t)
	defer os.RemoveAll(tmpDir)
	defer os.Unsetenv(PublicKeyEnvVar)
	server.config.MaxOutputSize = 8

	// exec replaces the shell with sleep, so the timeout kill closes the output pipes.
	resp, _ := runExecute(t, server, ExecuteRequest{
		Command: []string{"sh", "-c", "printf '%s' 0123456789 >&2; exec sleep 5"},
		Timeout: "300ms",
	})

	assert.Equal(t, TimeoutExitCode, resp.ExitCode)
	assert.True(t, resp.StderrTruncated)
	assert.True(t, strings.HasPrefix(resp.Stderr, "01234567Command timed out"), "stderr: %q", resp.Stderr)
	assert.True(t, strings.HasSuffix(resp.Stderr, "\nstderr truncated: output exceeded 8 bytes"), "stderr: %q", resp.Stderr)
}

func TestExecuteHandler_CommandNotFound(t *testing.T) {
	server, tmpDir := setupExecuteTestServer(t)
	defer os.RemoveAll(tmpDir)
	defer os.Unsetenv(PublicKeyEnvVar)

	// The process never starts, so there is no ProcessState and the start error goes to stderr.
	resp, _ := runExecute(t, server, ExecuteRequest{
		Command: []string{"picod-test-command-that-does-not-exist"},
	})

	assert.Equal(t, 1, resp.ExitCode)
	assert.Empty(t, resp.Stdout)
	assert.Contains(t, resp.Stderr, "executable file not found")
	assert.False(t, resp.StdoutTruncated)
	assert.False(t, resp.StderrTruncated)
}

func TestExecuteHandler_DefaultOutputLimit(t *testing.T) {
	// A zero MaxOutputSize falls back to DefaultMaxOutputSize instead of discarding all output.
	server := &Server{workspaceDir: t.TempDir()}

	resp, _ := runExecute(t, server, ExecuteRequest{
		Command: []string{"sh", "-c", "printf hello"},
	})

	assert.Equal(t, "hello", resp.Stdout)
	assert.False(t, resp.StdoutTruncated)
}
