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
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
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

func generateTestSessionPublicKeyPEM(t *testing.T) string {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
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
	os.Setenv(BootstrapPublicKeyEnvVar, pubKeyPEM)
	defer os.Unsetenv(BootstrapPublicKeyEnvVar)

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
			name: "non-existent workspace directory is created",
			setupWorkDir: func(t *testing.T) (string, func()) {
				tmpDir, err := os.MkdirTemp("", "picod-server-test-*")
				require.NoError(t, err)
				// Point to a subdirectory that does not exist yet
				nonExistent := filepath.Join(tmpDir, "workspace", "nested")
				return nonExistent, func() { os.RemoveAll(tmpDir) }
			},
			verifyResult: func(t *testing.T, server *Server) {
				assert.NotNil(t, server)
				assert.True(t, filepath.IsAbs(server.workspaceDir))
				// Directory must have been created by setWorkspace
				info, err := os.Stat(server.workspaceDir)
				assert.NoError(t, err, "workspace directory should exist after NewServer")
				assert.True(t, info.IsDir())
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

			server := NewServer(context.Background(), config)

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
	os.Setenv(BootstrapPublicKeyEnvVar, pubKeyPEM)
	defer os.Unsetenv(BootstrapPublicKeyEnvVar)

	config := Config{
		Port:      8080,
		Workspace: tmpDir,
	}

	server := NewServer(context.Background(), config)

	// Test that routes are registered by making requests
	ts := httptest.NewServer(server.engine)
	defer ts.Close()

	// Health check should be accessible without auth
	resp, err := http.Get(ts.URL + "/health")
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// Initialize server with session key so it's fully ready
	sessionPubPEM := generateTestSessionPublicKeyPEM(t)
	err = server.authManager.SetSessionPublicKey(sessionPubPEM)
	require.NoError(t, err)

	// API routes should require auth (will return 401 instead of 503 since initialized)
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
	os.Unsetenv(BootstrapPublicKeyEnvVar)

	// NewServer should fail (calls klog.Fatalf which will panic in tests)
	// We can't easily test klog.Fatalf without mocking, but we can verify
	// that LoadBootstrapPublicKey would fail
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	authManager := NewAuthManager(ctx)
	err = authManager.LoadBootstrapPublicKey()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), BootstrapPublicKeyEnvVar)
}

func TestHealthCheckHandler(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "picod-server-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	pubKeyPEM := generateTestPublicKeyPEM(t)
	os.Setenv(BootstrapPublicKeyEnvVar, pubKeyPEM)
	defer os.Unsetenv(BootstrapPublicKeyEnvVar)

	config := Config{
		Port:      8080,
		Workspace: tmpDir,
	}

	server := NewServer(context.Background(), config)

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
	os.Setenv(BootstrapPublicKeyEnvVar, pubKeyPEM)
	defer os.Unsetenv(BootstrapPublicKeyEnvVar)

	config := Config{
		Port:      8080,
		Workspace: tmpDir,
	}

	server := NewServer(context.Background(), config)

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
	os.Setenv(BootstrapPublicKeyEnvVar, pubKeyPEM)
	defer os.Unsetenv(BootstrapPublicKeyEnvVar)

	config := Config{
		Port:      8080,
		Workspace: tmpDir,
	}

	server := NewServer(context.Background(), config)

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
	os.Setenv(BootstrapPublicKeyEnvVar, pubKeyPEM)
	defer os.Unsetenv(BootstrapPublicKeyEnvVar)

	config := Config{
		Port:      8080,
		Workspace: tmpDir,
	}

	server := NewServer(context.Background(), config)

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
	os.Setenv(BootstrapPublicKeyEnvVar, pubKeyPEM)
	defer os.Unsetenv(BootstrapPublicKeyEnvVar)

	ports := []int{8080, 9090, 3000, 0}

	for _, port := range ports {
		t.Run(fmt.Sprintf("port_%d", port), func(t *testing.T) {
			config := Config{
				Port:      port,
				Workspace: tmpDir,
			}

			server := NewServer(context.Background(), config)

			assert.NotNil(t, server)
			assert.Equal(t, port, server.config.Port)
		})
	}
}

func TestServer_GzipMiddleware_CompressesResponse(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "picod-server-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	pubKeyPEM := generateTestPublicKeyPEM(t)
	os.Setenv(BootstrapPublicKeyEnvVar, pubKeyPEM)
	defer os.Unsetenv(BootstrapPublicKeyEnvVar)

	server := NewServer(context.Background(), Config{
		Port:      8080,
		Workspace: tmpDir,
	})

	ts := httptest.NewServer(server.engine)
	defer ts.Close()

	// A client that sends Accept-Encoding: gzip should receive a gzip-compressed response.
	// The /health endpoint responds with a JSON body that gin-contrib/gzip will compress.
	// However, /health is in the excluded paths — so we use /api/execute (which returns
	// 401 Unauthorized, a non-empty JSON body that gzip will still compress).
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/execute", nil)
	require.NoError(t, err)
	req.Header.Set("Accept-Encoding", "gzip")

	client := &http.Client{
		// Disable automatic transparent decompression so we can inspect the raw header.
		Transport: &http.Transport{
			DisableCompression: true,
		},
	}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	// The middleware must set Content-Encoding: gzip on the wire.
	assert.Equal(t, "gzip", resp.Header.Get("Content-Encoding"),
		"response should be gzip-compressed when client advertises Accept-Encoding: gzip")
}

func TestServer_GzipMiddleware_ExcludesHealthEndpoint(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "picod-server-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	pubKeyPEM := generateTestPublicKeyPEM(t)
	os.Setenv(BootstrapPublicKeyEnvVar, pubKeyPEM)
	defer os.Unsetenv(BootstrapPublicKeyEnvVar)

	server := NewServer(context.Background(), Config{
		Port:      8080,
		Workspace: tmpDir,
	})

	ts := httptest.NewServer(server.engine)
	defer ts.Close()

	// /health is in the WithExcludedPaths list, so even when the client requests gzip
	// the middleware must NOT set Content-Encoding: gzip on that path.
	req, err := http.NewRequest(http.MethodGet, ts.URL+"/health", nil)
	require.NoError(t, err)
	req.Header.Set("Accept-Encoding", "gzip")

	client := &http.Client{
		Transport: &http.Transport{
			DisableCompression: true,
		},
	}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.NotEqual(t, "gzip", resp.Header.Get("Content-Encoding"),
		"/health is an excluded path and must not be gzip-compressed")
}

func TestNewServer_JWTMode_RequiresAuth(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "picod-server-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	pubKeyPEM := generateTestPublicKeyPEM(t)
	os.Setenv(BootstrapPublicKeyEnvVar, pubKeyPEM)
	defer os.Unsetenv(BootstrapPublicKeyEnvVar)

	config := Config{
		Port:      8080,
		Workspace: tmpDir,
	}

	server := NewServer(context.Background(), config)
	ts := httptest.NewServer(server.engine)
	defer ts.Close()

	// API endpoint should return 503 when daemon is not initialized
	resp, err := http.Post(ts.URL+"/api/execute", "application/json", nil)
	require.NoError(t, err)
	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode, "should require initialization first")
	resp.Body.Close()

	// Initialize session key
	sessionPubPEM := generateTestSessionPublicKeyPEM(t)
	err = server.authManager.SetSessionPublicKey(sessionPubPEM)
	require.NoError(t, err)

	// API endpoint should return 401 when authorization header is missing after init
	resp, err = http.Post(ts.URL+"/api/execute", "application/json", nil)
	require.NoError(t, err)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, "JWT mode should require auth")
	resp.Body.Close()
}

func TestServer_MaxBodySizeMiddleware(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "picod-server-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	pubKeyPEM := generateTestPublicKeyPEM(t)
	os.Setenv(BootstrapPublicKeyEnvVar, pubKeyPEM)
	defer os.Unsetenv(BootstrapPublicKeyEnvVar)

	server := NewServer(context.Background(), Config{
		Port:      8080,
		Workspace: tmpDir,
	})

	ts := httptest.NewServer(server.engine)
	defer ts.Close()

	// When Content-Length exceeds MaxBodySize, the global body-size limiter
	// middleware should reject the request with 413 before any other
	// middleware (auth, handler) gets a chance to run.
	oversizedBody := strings.NewReader(strings.Repeat("x", int(MaxBodySize)+1))
	resp, err := http.Post(ts.URL+"/api/execute", "application/json", oversizedBody)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Contains(t, string(body), "request body too large")
}

func TestInitHandler(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "picod-server-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	// Generate bootstrap keys
	bootstrapPrivKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	pubKeyBytes, err := x509.MarshalPKIXPublicKey(&bootstrapPrivKey.PublicKey)
	require.NoError(t, err)

	pubKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: pubKeyBytes,
	})

	os.Setenv(BootstrapPublicKeyEnvVar, string(pubKeyPEM))
	defer os.Unsetenv(BootstrapPublicKeyEnvVar)

	os.Setenv(SessionIDEnvVar, "test-session")
	defer os.Unsetenv(SessionIDEnvVar)

	config := Config{
		Port:      8080,
		Workspace: tmpDir,
	}

	// Helper to generate a token signed by bootstrap private key
	generateToken := func(claims jwt.MapClaims) string {
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		tokenStr, err := token.SignedString(bootstrapPrivKey)
		require.NoError(t, err)
		return tokenStr
	}

	sessionPubPEM := generateTestSessionPublicKeyPEM(t)

	t.Run("invalid request format", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request, _ = http.NewRequest("POST", "/init", bytes.NewBufferString("{invalid-json}"))
		c.Request.Header.Set("Content-Type", "application/json")

		freshServer := NewServer(context.Background(), config)
		freshServer.InitHandler(c)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		var resp map[string]string
		err := json.Unmarshal(w.Body.Bytes(), &resp)
		require.NoError(t, err)
		assert.Contains(t, resp["error"], "invalid request format")
	})

	t.Run("invalid token", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		body, _ := json.Marshal(map[string]string{"token": "invalid.token.here"})
		c.Request, _ = http.NewRequest("POST", "/init", bytes.NewBuffer(body))
		c.Request.Header.Set("Content-Type", "application/json")

		freshServer := NewServer(context.Background(), config)
		freshServer.InitHandler(c)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
		var resp map[string]string
		err := json.Unmarshal(w.Body.Bytes(), &resp)
		require.NoError(t, err)
		assert.Contains(t, resp["error"], "bootstrap token verification failed")
	})

	t.Run("successful initialization", func(t *testing.T) {
		claims := jwt.MapClaims{
			"iss":                "agentcube-workload-manager",
			"sub":                "test-session",
			"exp":                time.Now().Add(time.Minute).Unix(),
			"iat":                time.Now().Unix(),
			"session_public_key": sessionPubPEM,
			"jti":                "test-jti-1",
		}
		token := generateToken(claims)

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		body, _ := json.Marshal(map[string]string{"token": token})
		c.Request, _ = http.NewRequest("POST", "/init", bytes.NewBuffer(body))
		c.Request.Header.Set("Content-Type", "application/json")

		freshServer := NewServer(context.Background(), config)
		freshServer.InitHandler(c)

		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]string
		err := json.Unmarshal(w.Body.Bytes(), &resp)
		require.NoError(t, err)
		assert.Equal(t, "initialized successfully", resp["status"])
	})

	t.Run("already initialized", func(t *testing.T) {
		// Create a fresh server for this sub-test to avoid state leak
		freshServer := NewServer(context.Background(), config)

		claims := jwt.MapClaims{
			"iss":                "agentcube-workload-manager",
			"sub":                "test-session",
			"exp":                time.Now().Add(time.Minute).Unix(),
			"iat":                time.Now().Unix(),
			"session_public_key": sessionPubPEM,
			"jti":                "test-jti-2",
		}
		token := generateToken(claims)

		// First initialization should succeed
		w1 := httptest.NewRecorder()
		c1, _ := gin.CreateTestContext(w1)
		body1, _ := json.Marshal(map[string]string{"token": token})
		c1.Request, _ = http.NewRequest("POST", "/init", bytes.NewBuffer(body1))
		c1.Request.Header.Set("Content-Type", "application/json")
		freshServer.InitHandler(c1)
		require.Equal(t, http.StatusOK, w1.Code)

		// Second initialization should fail
		claims2 := jwt.MapClaims{
			"iss":                "agentcube-workload-manager",
			"sub":                "test-session",
			"exp":                time.Now().Add(time.Minute).Unix(),
			"iat":                time.Now().Unix(),
			"session_public_key": sessionPubPEM,
			"jti":                "test-jti-3",
		}
		token2 := generateToken(claims2)

		w2 := httptest.NewRecorder()
		c2, _ := gin.CreateTestContext(w2)
		body2, _ := json.Marshal(map[string]string{"token": token2})
		c2.Request, _ = http.NewRequest("POST", "/init", bytes.NewBuffer(body2))
		c2.Request.Header.Set("Content-Type", "application/json")

		freshServer.InitHandler(c2)

		assert.Equal(t, http.StatusConflict, w2.Code)
		var resp map[string]string
		err := json.Unmarshal(w2.Body.Bytes(), &resp)
		require.NoError(t, err)
		assert.Contains(t, resp["error"], "session already initialized")
	})
}
