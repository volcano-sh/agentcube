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
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
			name:        "valid subdirectory",
			workingDir:  "subdir",
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
