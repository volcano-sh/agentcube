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
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupProcessTestServer(t *testing.T) (*Server, string) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	pubKeyBytes, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	require.NoError(t, err)

	pubKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: pubKeyBytes,
	})

	os.Setenv(PublicKeyEnvVar, string(pubKeyPEM))

	tmpDir, err := os.MkdirTemp("", "picod-process-test-*")
	require.NoError(t, err)

	config := Config{
		Port:      0,
		Workspace: tmpDir,
	}

	server := NewServer(config)
	return server, tmpDir
}

func TestEnvdProcessStartHandler_Validation(t *testing.T) {
	server, tmpDir := setupProcessTestServer(t)
	defer os.RemoveAll(tmpDir)
	defer os.Unsetenv(PublicKeyEnvVar)

	tests := []struct {
		name          string
		body          interface{}
		expectedCode  int
		errorContains string
	}{
		{
			name:          "invalid JSON",
			body:          []byte("invalid json"),
			expectedCode:  http.StatusBadRequest,
			errorContains: "invalid request body",
		},
		{
			name:          "empty cmd",
			body:          ProcessStartRequest{Cmd: []string{}},
			expectedCode:  http.StatusBadRequest,
			errorContains: "cmd is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var body []byte
			var err error
			if b, ok := tt.body.([]byte); ok {
				body = b
			} else {
				body, err = json.Marshal(tt.body)
				require.NoError(t, err)
			}

			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request, _ = http.NewRequest("POST", "/envd/process/start", bytes.NewBuffer(body))
			c.Request.Header.Set("Content-Type", "application/json")

			server.EnvdProcessStartHandler(c)

			assert.Equal(t, tt.expectedCode, w.Code)
			assert.Contains(t, w.Body.String(), tt.errorContains)
		})
	}
}

func TestEnvdProcessStartHandler_Success(t *testing.T) {
	server, tmpDir := setupProcessTestServer(t)
	defer os.RemoveAll(tmpDir)
	defer os.Unsetenv(PublicKeyEnvVar)

	req := ProcessStartRequest{
		Cmd: []string{"echo", "hello"},
	}
	body, _ := json.Marshal(req)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/envd/process/start", bytes.NewBuffer(body))
	c.Request.Header.Set("Content-Type", "application/json")

	server.EnvdProcessStartHandler(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var mp ManagedProcess
	err := json.Unmarshal(w.Body.Bytes(), &mp)
	require.NoError(t, err)
	assert.NotEmpty(t, mp.ProcessID)
	assert.Equal(t, ProcessStateRunning, mp.State)
}

func TestEnvdProcessInputHandler(t *testing.T) {
	server, tmpDir := setupProcessTestServer(t)
	defer os.RemoveAll(tmpDir)
	defer os.Unsetenv(PublicKeyEnvVar)

	// Start a process
	startReq := ProcessStartRequest{Cmd: []string{"cat"}}
	body, _ := json.Marshal(startReq)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/envd/process/start", bytes.NewBuffer(body))
	c.Request.Header.Set("Content-Type", "application/json")
	server.EnvdProcessStartHandler(c)
	require.Equal(t, http.StatusOK, w.Code)

	var mp ManagedProcess
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &mp))

	// Send input
	inputReq := ProcessInputRequest{
		ProcessID: mp.ProcessID,
		Data:      "hello",
	}
	body, _ = json.Marshal(inputReq)
	w = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/envd/process/input", bytes.NewBuffer(body))
	c.Request.Header.Set("Content-Type", "application/json")
	server.EnvdProcessInputHandler(c)

	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestEnvdProcessInputHandler_NotFound(t *testing.T) {
	server, tmpDir := setupProcessTestServer(t)
	defer os.RemoveAll(tmpDir)
	defer os.Unsetenv(PublicKeyEnvVar)

	inputReq := ProcessInputRequest{
		ProcessID: "nonexistent",
		Data:      "hello",
	}
	body, _ := json.Marshal(inputReq)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/envd/process/input", bytes.NewBuffer(body))
	c.Request.Header.Set("Content-Type", "application/json")
	server.EnvdProcessInputHandler(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "not found")
}

func TestEnvdProcessCloseStdinHandler(t *testing.T) {
	server, tmpDir := setupProcessTestServer(t)
	defer os.RemoveAll(tmpDir)
	defer os.Unsetenv(PublicKeyEnvVar)

	// Start a process
	startReq := ProcessStartRequest{Cmd: []string{"cat"}}
	body, _ := json.Marshal(startReq)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/envd/process/start", bytes.NewBuffer(body))
	c.Request.Header.Set("Content-Type", "application/json")
	server.EnvdProcessStartHandler(c)
	require.Equal(t, http.StatusOK, w.Code)

	var mp ManagedProcess
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &mp))

	// Close stdin
	closeReq := ProcessCloseStdinRequest{ProcessID: mp.ProcessID}
	body, _ = json.Marshal(closeReq)
	w = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/envd/process/close-stdin", bytes.NewBuffer(body))
	c.Request.Header.Set("Content-Type", "application/json")
	server.EnvdProcessCloseStdinHandler(c)

	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestEnvdProcessSignalHandler(t *testing.T) {
	server, tmpDir := setupProcessTestServer(t)
	defer os.RemoveAll(tmpDir)
	defer os.Unsetenv(PublicKeyEnvVar)

	// Start a long-running process
	startReq := ProcessStartRequest{Cmd: []string{"sleep", "10"}}
	body, _ := json.Marshal(startReq)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/envd/process/start", bytes.NewBuffer(body))
	c.Request.Header.Set("Content-Type", "application/json")
	server.EnvdProcessStartHandler(c)
	require.Equal(t, http.StatusOK, w.Code)

	var mp ManagedProcess
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &mp))

	// Send signal
	signalReq := ProcessSignalRequest{
		ProcessID: mp.ProcessID,
		Signal:    15,
	}
	body, _ = json.Marshal(signalReq)
	w = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/envd/process/signal", bytes.NewBuffer(body))
	c.Request.Header.Set("Content-Type", "application/json")
	server.EnvdProcessSignalHandler(c)

	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestEnvdProcessSignalHandler_NotFound(t *testing.T) {
	server, tmpDir := setupProcessTestServer(t)
	defer os.RemoveAll(tmpDir)
	defer os.Unsetenv(PublicKeyEnvVar)

	signalReq := ProcessSignalRequest{
		ProcessID: "nonexistent",
		Signal:    9,
	}
	body, _ := json.Marshal(signalReq)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/envd/process/signal", bytes.NewBuffer(body))
	c.Request.Header.Set("Content-Type", "application/json")
	server.EnvdProcessSignalHandler(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "not found")
}

func TestEnvdProcessListHandler(t *testing.T) {
	server, tmpDir := setupProcessTestServer(t)
	defer os.RemoveAll(tmpDir)
	defer os.Unsetenv(PublicKeyEnvVar)

	// Start a process
	startReq := ProcessStartRequest{Cmd: []string{"echo", "hello"}}
	body, _ := json.Marshal(startReq)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/envd/process/start", bytes.NewBuffer(body))
	c.Request.Header.Set("Content-Type", "application/json")
	server.EnvdProcessStartHandler(c)
	require.Equal(t, http.StatusOK, w.Code)

	// List processes
	w = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/envd/process/list", nil)
	server.EnvdProcessListHandler(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string][]ManagedProcess
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.NotEmpty(t, resp["processes"])
}

func TestEnvdProcessStartHandler_WithCwd(t *testing.T) {
	server, tmpDir := setupProcessTestServer(t)
	defer os.RemoveAll(tmpDir)
	defer os.Unsetenv(PublicKeyEnvVar)

	// Use a relative path within the workspace
	req := ProcessStartRequest{
		Cmd: []string{"pwd"},
		Cwd: ".",
	}
	body, _ := json.Marshal(req)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/envd/process/start", bytes.NewBuffer(body))
	c.Request.Header.Set("Content-Type", "application/json")

	server.EnvdProcessStartHandler(c)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestEnvdProcessStartHandler_WithEnv(t *testing.T) {
	server, tmpDir := setupProcessTestServer(t)
	defer os.RemoveAll(tmpDir)
	defer os.Unsetenv(PublicKeyEnvVar)

	req := ProcessStartRequest{
		Cmd: []string{"sh", "-c", "echo $TEST_VAR"},
		Env: map[string]string{"TEST_VAR": "value"},
	}
	body, _ := json.Marshal(req)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/envd/process/start", bytes.NewBuffer(body))
	c.Request.Header.Set("Content-Type", "application/json")

	server.EnvdProcessStartHandler(c)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestEnvdProcessStartHandler_WithTimeout(t *testing.T) {
	server, tmpDir := setupProcessTestServer(t)
	defer os.RemoveAll(tmpDir)
	defer os.Unsetenv(PublicKeyEnvVar)

	req := ProcessStartRequest{
		Cmd:     []string{"sleep", "10"},
		Timeout: 1,
	}
	body, _ := json.Marshal(req)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/envd/process/start", bytes.NewBuffer(body))
	c.Request.Header.Set("Content-Type", "application/json")

	server.EnvdProcessStartHandler(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var mp ManagedProcess
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &mp))
	assert.NotEmpty(t, mp.ProcessID)

	// Wait for timeout
	time.Sleep(1500 * time.Millisecond)

	got, err := server.processRegistry.Get(mp.ProcessID)
	require.NoError(t, err)
	assert.Equal(t, ProcessStateExited, got.State)
}
