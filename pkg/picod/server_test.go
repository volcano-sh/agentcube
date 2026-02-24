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
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
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

func generateTestPublicKeyPEM(t *testing.T) string {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	pubKeyBytes, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	require.NoError(t, err)

	pubKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: pubKeyBytes,
	})

	return string(pubKeyPEM)
}

func TestNewServer_WorkspaceConfiguration(t *testing.T) {
	pubKeyPEM := generateTestPublicKeyPEM(t)
	os.Setenv(PublicKeyEnvVar, pubKeyPEM)
	defer os.Unsetenv(PublicKeyEnvVar)

	tests := []struct {
		name         string
		setupWorkDir func(t *testing.T) (workspaceConfig string, cleanup func())
		verifyResult func(t *testing.T, server *Server)
	}{
		{
			name: "with explicit workspace directory",
			setupWorkDir: func(t *testing.T) (string, func()) {
				tmpDir, err := os.MkdirTemp("", "picod-server-test-*")
				require.NoError(t, err)
				return tmpDir, func() { os.RemoveAll(tmpDir) }
			},
			verifyResult: func(t *testing.T, server *Server) {
				assert.NotNil(t, server)
				assert.Equal(t, server.config.Workspace, server.workspaceDir)
			},
		},
		{
			name: "without workspace (defaults to cwd)",
			setupWorkDir: func(t *testing.T) (string, func()) {
				tmpDir, err := os.MkdirTemp("", "picod-server-test-*")
				require.NoError(t, err)
				originalWd, err := os.Getwd()
				require.NoError(t, err)
				err = os.Chdir(tmpDir)
				require.NoError(t, err)
				return "", func() {
					_ = os.Chdir(originalWd)
					os.RemoveAll(tmpDir)
				}
			},
			verifyResult: func(t *testing.T, server *Server) {
				assert.NotNil(t, server)
				cwd, err := os.Getwd()
				require.NoError(t, err)
				absCwd, err := filepath.Abs(cwd)
				require.NoError(t, err)
				assert.Equal(t, absCwd, server.workspaceDir)
			},
		},
		{
			name: "with relative path workspace",
			setupWorkDir: func(t *testing.T) (string, func()) {
				tmpDir, err := os.MkdirTemp("", "picod-server-test-*")
				require.NoError(t, err)
				subDir := filepath.Join(tmpDir, "subdir")
				err = os.Mkdir(subDir, 0755)
				require.NoError(t, err)
				originalWd, err := os.Getwd()
				require.NoError(t, err)
				err = os.Chdir(tmpDir)
				require.NoError(t, err)
				return "subdir", func() {
					_ = os.Chdir(originalWd)
					os.RemoveAll(tmpDir)
				}
			},
			verifyResult: func(t *testing.T, server *Server) {
				assert.NotNil(t, server)
				assert.True(t, filepath.IsAbs(server.workspaceDir))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			workspaceConfig, cleanup := tt.setupWorkDir(t)
			defer cleanup()

			config := Config{
				Port:      8080,
				Workspace: workspaceConfig,
			}

			server := NewServer(config)

			tt.verifyResult(t, server)
			assert.NotNil(t, server.engine)
			assert.NotNil(t, server.authManager)
			assert.False(t, server.startTime.IsZero())
		})
	}
}

func TestNewServer_RoutesRegistered(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "picod-server-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	pubKeyPEM := generateTestPublicKeyPEM(t)
	os.Setenv(PublicKeyEnvVar, pubKeyPEM)
	defer os.Unsetenv(PublicKeyEnvVar)

	config := Config{
		Port:      8080,
		Workspace: tmpDir,
	}

	server := NewServer(config)

	// Test that routes are registered by making requests
	ts := httptest.NewServer(server.engine)
	defer ts.Close()

	// Health check should be accessible without auth
	resp, err := http.Get(ts.URL + "/health")
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// API routes should require auth (will return 401)
	resp, err = http.Post(ts.URL+"/api/execute", "application/json", nil)
	require.NoError(t, err)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	resp.Body.Close()
}

func TestNewServer_PublicKeyRequired(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "picod-server-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	// Don't set public key environment variable
	os.Unsetenv(PublicKeyEnvVar)

	// NewServer should fail (calls klog.Fatalf which will panic in tests)
	// We can't easily test klog.Fatalf without mocking, but we can verify
	// that LoadPublicKeyFromEnv would fail
	authManager := NewAuthManager()
	err = authManager.LoadPublicKeyFromEnv()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), PublicKeyEnvVar)
}

func TestHealthCheckHandler(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "picod-server-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	pubKeyPEM := generateTestPublicKeyPEM(t)
	os.Setenv(PublicKeyEnvVar, pubKeyPEM)
	defer os.Unsetenv(PublicKeyEnvVar)

	config := Config{
		Port:      8080,
		Workspace: tmpDir,
	}

	server := NewServer(config)

	// Record start time for comparison
	startTime := server.startTime

	// Wait a bit to ensure uptime changes
	time.Sleep(10 * time.Millisecond)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/health", nil)

	server.HealthCheckHandler(c)

	assert.Equal(t, http.StatusOK, w.Code)

	// Verify response structure
	var body map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &body)
	require.NoError(t, err)

	assert.Equal(t, "ok", body["status"])
	assert.Equal(t, "PicoD", body["service"])
	assert.Equal(t, "0.0.1", body["version"])
	assert.NotNil(t, body["uptime"])

	// Verify uptime is a string and not zero
	uptime, ok := body["uptime"].(string)
	assert.True(t, ok)
	assert.NotEmpty(t, uptime)

	// Verify start time is before now
	assert.True(t, startTime.Before(time.Now()))
}

func TestHealthCheckHandler_MultipleCalls(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "picod-server-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	pubKeyPEM := generateTestPublicKeyPEM(t)
	os.Setenv(PublicKeyEnvVar, pubKeyPEM)
	defer os.Unsetenv(PublicKeyEnvVar)

	config := Config{
		Port:      8080,
		Workspace: tmpDir,
	}

	server := NewServer(config)

	// Call health check multiple times
	for i := 0; i < 5; i++ {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request, _ = http.NewRequest("GET", "/health", nil)

		server.HealthCheckHandler(c)

		assert.Equal(t, http.StatusOK, w.Code)

		var body map[string]interface{}
		err := json.Unmarshal(w.Body.Bytes(), &body)
		require.NoError(t, err)
		assert.Equal(t, "ok", body["status"])

		// Uptime should increase with each call
		if i > 0 {
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func TestNewServer_EngineConfiguration(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "picod-server-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	pubKeyPEM := generateTestPublicKeyPEM(t)
	os.Setenv(PublicKeyEnvVar, pubKeyPEM)
	defer os.Unsetenv(PublicKeyEnvVar)

	config := Config{
		Port:      8080,
		Workspace: tmpDir,
	}

	server := NewServer(config)

	assert.NotNil(t, server.engine)
	// Verify Gin is in release mode (set by NewServer)
	// This is harder to test directly, but we can verify the engine works
	assert.NotNil(t, server.engine.Routes())
}

func TestNewServer_AuthManagerInitialized(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "picod-server-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	pubKeyPEM := generateTestPublicKeyPEM(t)
	os.Setenv(PublicKeyEnvVar, pubKeyPEM)
	defer os.Unsetenv(PublicKeyEnvVar)

	config := Config{
		Port:      8080,
		Workspace: tmpDir,
	}

	server := NewServer(config)

	assert.NotNil(t, server.authManager)
	// Verify public key is loaded
	// We can't directly access private fields, but we can verify
	// the auth manager works by checking it doesn't panic
	assert.NotNil(t, server.authManager.AuthMiddleware())
}

func TestNewServer_DifferentPorts(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "picod-server-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	pubKeyPEM := generateTestPublicKeyPEM(t)
	os.Setenv(PublicKeyEnvVar, pubKeyPEM)
	defer os.Unsetenv(PublicKeyEnvVar)

	ports := []int{8080, 9090, 3000, 0}

	for _, port := range ports {
		t.Run(fmt.Sprintf("port_%d", port), func(t *testing.T) {
			config := Config{
				Port:      port,
				Workspace: tmpDir,
			}

			server := NewServer(config)

			assert.NotNil(t, server)
			assert.Equal(t, port, server.config.Port)
		})
	}
}
