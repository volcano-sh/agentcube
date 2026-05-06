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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnvdEnvHandler(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "picod-envd-test-*")
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

	// Set a known environment variable for verification
	os.Setenv("PICOD_TEST_ENV_VAR", "test_value_123")
	defer os.Unsetenv("PICOD_TEST_ENV_VAR")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/envd/env", nil)

	// Generate a valid JWT for authentication
	privateKey, _, err := generateTestRSAKeyPair()
	require.NoError(t, err)
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"sub": "test",
		"iat": time.Now().Unix(),
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	tokenString, err := token.SignedString(privateKey)
	require.NoError(t, err)
	c.Request.Header.Set("Authorization", "Bearer "+tokenString)

	server.EnvdEnvHandler(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var body map[string]string
	err = json.Unmarshal(w.Body.Bytes(), &body)
	require.NoError(t, err)

	// Verify the known environment variable is present
	assert.Equal(t, "test_value_123", body["PICOD_TEST_ENV_VAR"])

	// Verify some standard environment variables exist
	assert.NotEmpty(t, body["PATH"])
}

func TestEnvdEnvHandler_Unauthorized(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "picod-envd-test-*")
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

	ts := httptest.NewServer(server.engine)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/envd/env")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestEnvdHealthHandler(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "picod-envd-test-*")
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

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/envd/health", nil)

	server.engine.ServeHTTP(w, c.Request)

	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Empty(t, w.Body.String())
}

func TestEnvdHealthHandler_NoAuthRequired(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "picod-envd-test-*")
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

	ts := httptest.NewServer(server.engine)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/envd/health")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
}

func TestEnvdRoutesRegistered(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "picod-envd-test-*")
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

	ts := httptest.NewServer(server.engine)
	defer ts.Close()

	// /envd/health should be accessible without auth
	resp, err := http.Get(ts.URL + "/envd/health")
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)

	// /envd/env should require auth (no token → 401)
	resp, err = http.Get(ts.URL + "/envd/env")
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}
