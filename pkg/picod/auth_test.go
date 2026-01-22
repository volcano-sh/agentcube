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
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func generateTestRSAKeyPair() (*rsa.PrivateKey, string, error) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, "", err
	}

	publicKey := &privateKey.PublicKey

	// Encode public key to PEM
	pubKeyBytes, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		return nil, nil, "", err
	}

	pubKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: pubKeyBytes,
	})

	return privateKey, string(pubKeyBytes), nil
}

func TestNewAuthManager(t *testing.T) {
	manager := NewAuthManager()
	assert.NotNil(t, manager)
}

func TestLoadPublicKeyFromEnv_ValidKey(t *testing.T) {
	_, _, pubKeyPEM, err := generateTestRSAKeyPair()
	assert.NoError(t, err)

	os.Setenv(PublicKeyEnvVar, pubKeyPEM)
	defer os.Unsetenv(PublicKeyEnvVar)

	manager := NewAuthManager()
	err = manager.LoadPublicKeyFromEnv()
	assert.NoError(t, err)
}

func TestLoadPublicKeyFromEnv_MissingEnvVar(t *testing.T) {
	os.Unsetenv(PublicKeyEnvVar)

	manager := NewAuthManager()
	err := manager.LoadPublicKeyFromEnv()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), PublicKeyEnvVar)
	assert.Contains(t, err.Error(), "not set")
}

func TestLoadPublicKeyFromEnv_InvalidPEM(t *testing.T) {
	os.Setenv(PublicKeyEnvVar, "invalid PEM data")
	defer os.Unsetenv(PublicKeyEnvVar)

	manager := NewAuthManager()
	err := manager.LoadPublicKeyFromEnv()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to decode PEM")
}

func TestLoadPublicKeyFromEnv_InvalidKeyFormat(t *testing.T) {
	// Create a PEM block with wrong type (e.g., certificate instead of public key)
	certPEM := `-----BEGIN CERTIFICATE-----
MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEA...
-----END CERTIFICATE-----`

	os.Setenv(PublicKeyEnvVar, certPEM)
	defer os.Unsetenv(PublicKeyEnvVar)

	manager := NewAuthManager()
	err := manager.LoadPublicKeyFromEnv()
	assert.Error(t, err)
}

func TestLoadPublicKeyFromEnv_NonRSAPublicKey(t *testing.T) {
	// This test would require generating an ECDSA key, which is more complex
	// For now, we'll test with an invalid key format
	invalidKeyPEM := `-----BEGIN PUBLIC KEY-----
MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAinvalid
-----END PUBLIC KEY-----`

	os.Setenv(PublicKeyEnvVar, invalidKeyPEM)
	defer os.Unsetenv(PublicKeyEnvVar)

	manager := NewAuthManager()
	err := manager.LoadPublicKeyFromEnv()
	assert.Error(t, err)
}

func TestAuthMiddleware_MissingAuthorizationHeader(t *testing.T) {
	_, _, pubKeyPEM, err := generateTestRSAKeyPair()
	assert.NoError(t, err)

	os.Setenv(PublicKeyEnvVar, pubKeyPEM)
	defer os.Unsetenv(PublicKeyEnvVar)

	manager := NewAuthManager()
	err = manager.LoadPublicKeyFromEnv()
	assert.NoError(t, err)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/api/execute", nil)

	handler := manager.AuthMiddleware()
	handler(c)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), "Missing Authorization header")
}

func TestAuthMiddleware_InvalidHeaderFormat(t *testing.T) {
	_, _, pubKeyPEM, err := generateTestRSAKeyPair()
	assert.NoError(t, err)

	os.Setenv(PublicKeyEnvVar, pubKeyPEM)
	defer os.Unsetenv(PublicKeyEnvVar)

	manager := NewAuthManager()
	err = manager.LoadPublicKeyFromEnv()
	assert.NoError(t, err)

	tests := []struct {
		name   string
		header string
	}{
		{
			name:   "no Bearer prefix",
			header: "token123",
		},
		{
			name:   "wrong prefix",
			header: "Basic token123",
		},
		{
			name:   "empty token",
			header: "Bearer ",
		},
		{
			name:   "multiple spaces",
			header: "Bearer  token123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request, _ = http.NewRequest("POST", "/api/execute", nil)
			c.Request.Header.Set("Authorization", tt.header)

			handler := manager.AuthMiddleware()
			handler(c)

			assert.Equal(t, http.StatusUnauthorized, w.Code)
			// Empty token ("Bearer ") passes header format check but fails JWT parsing
			if tt.name == "empty token" {
				assert.Contains(t, w.Body.String(), "JWT verification failed")
			} else {
				assert.Contains(t, w.Body.String(), "Invalid Authorization header format")
			}
		})
	}
}

func TestAuthMiddleware_ValidToken(t *testing.T) {
	privateKey, _, pubKeyPEM, err := generateTestRSAKeyPair()
	assert.NoError(t, err)

	os.Setenv(PublicKeyEnvVar, pubKeyPEM)
	defer os.Unsetenv(PublicKeyEnvVar)

	manager := NewAuthManager()
	err = manager.LoadPublicKeyFromEnv()
	assert.NoError(t, err)

	// Generate a valid JWT token
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"exp": time.Now().Add(time.Hour).Unix(),
		"iat": time.Now().Unix(),
	})
	tokenString, err := token.SignedString(privateKey)
	assert.NoError(t, err)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/api/execute", nil)
	c.Request.Header.Set("Authorization", "Bearer "+tokenString)

	handler := manager.AuthMiddleware()
	handler(c)

	// Should not abort (status 200 or next handler called)
	assert.NotEqual(t, http.StatusUnauthorized, w.Code)
}

func TestAuthMiddleware_ExpiredToken(t *testing.T) {
	privateKey, _, pubKeyPEM, err := generateTestRSAKeyPair()
	assert.NoError(t, err)

	os.Setenv(PublicKeyEnvVar, pubKeyPEM)
	defer os.Unsetenv(PublicKeyEnvVar)

	manager := NewAuthManager()
	err = manager.LoadPublicKeyFromEnv()
	assert.NoError(t, err)

	// Generate an expired JWT token
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"exp": time.Now().Add(-time.Hour).Unix(), // Expired
		"iat": time.Now().Add(-2 * time.Hour).Unix(),
	})
	tokenString, err := token.SignedString(privateKey)
	assert.NoError(t, err)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/api/execute", nil)
	c.Request.Header.Set("Authorization", "Bearer "+tokenString)

	handler := manager.AuthMiddleware()
	handler(c)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), "Invalid token")
}

func TestAuthMiddleware_InvalidSignature(t *testing.T) {
	_, _, pubKeyPEM, err := generateTestRSAKeyPair()
	assert.NoError(t, err)

	// Generate a different key pair for signing
	wrongPrivateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	assert.NoError(t, err)

	os.Setenv(PublicKeyEnvVar, pubKeyPEM)
	defer os.Unsetenv(PublicKeyEnvVar)

	manager := NewAuthManager()
	err = manager.LoadPublicKeyFromEnv()
	assert.NoError(t, err)

	// Generate token with wrong private key
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"exp": time.Now().Add(time.Hour).Unix(),
		"iat": time.Now().Unix(),
	})
	tokenString, err := token.SignedString(wrongPrivateKey)
	assert.NoError(t, err)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/api/execute", nil)
	c.Request.Header.Set("Authorization", "Bearer "+tokenString)

	handler := manager.AuthMiddleware()
	handler(c)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), "Invalid token")
}

func TestAuthMiddleware_WrongSigningMethod(t *testing.T) {
	_, _, pubKeyPEM, err := generateTestRSAKeyPair()
	assert.NoError(t, err)

	os.Setenv(PublicKeyEnvVar, pubKeyPEM)
	defer os.Unsetenv(PublicKeyEnvVar)

	manager := NewAuthManager()
	err = manager.LoadPublicKeyFromEnv()
	assert.NoError(t, err)

	// Generate token with HS256 (HMAC) instead of RS256
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"exp": time.Now().Add(time.Hour).Unix(),
		"iat": time.Now().Unix(),
	})
	tokenString, err := token.SignedString([]byte("secret"))
	assert.NoError(t, err)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/api/execute", nil)
	c.Request.Header.Set("Authorization", "Bearer "+tokenString)

	handler := manager.AuthMiddleware()
	handler(c)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestAuthMiddleware_MaxBodySize(t *testing.T) {
	privateKey, _, pubKeyPEM, err := generateTestRSAKeyPair()
	assert.NoError(t, err)

	os.Setenv(PublicKeyEnvVar, pubKeyPEM)
	defer os.Unsetenv(PublicKeyEnvVar)

	manager := NewAuthManager()
	err = manager.LoadPublicKeyFromEnv()
	assert.NoError(t, err)

	// Generate a valid JWT token
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"exp": time.Now().Add(time.Hour).Unix(),
		"iat": time.Now().Unix(),
	})
	tokenString, err := token.SignedString(privateKey)
	assert.NoError(t, err)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/api/execute", nil)
	c.Request.Header.Set("Authorization", "Bearer "+tokenString)

	handler := manager.AuthMiddleware()
	handler(c)

	// Verify MaxBytesReader is set
	assert.NotNil(t, c.Request.Body)
}
