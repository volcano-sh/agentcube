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
	"github.com/stretchr/testify/require"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func generateTestRSAKeyPair() (*rsa.PrivateKey, string, error) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, "", err
	}

	publicKey := &privateKey.PublicKey

	// Encode public key to PEM
	pubKeyBytes, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		return nil, "", err
	}

	pubKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: pubKeyBytes,
	})

	return privateKey, string(pubKeyPEM), nil
}

func TestNewAuthManager(t *testing.T) {
	manager := NewAuthManager()
	assert.NotNil(t, manager)
}

func TestLoadPublicKeyFromEnv(t *testing.T) {
	tests := []struct {
		name        string
		setupEnv    func() string
		wantErr     bool
		errContains string
	}{
		{
			name: "valid RSA public key",
			setupEnv: func() string {
				_, pubKeyPEM, err := generateTestRSAKeyPair()
				require.NoError(t, err)
				return pubKeyPEM
			},
			wantErr: false,
		},
		{
			name: "missing environment variable",
			setupEnv: func() string {
				return ""
			},
			wantErr:     true,
			errContains: "not set",
		},
		{
			name: "invalid PEM data",
			setupEnv: func() string {
				return "invalid PEM data"
			},
			wantErr:     true,
			errContains: "failed to decode PEM",
		},
		{
			name: "invalid key format (certificate instead of public key)",
			setupEnv: func() string {
				return `-----BEGIN CERTIFICATE-----
MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEA...
-----END CERTIFICATE-----`
			},
			wantErr: true,
		},
		{
			name: "invalid key format (non-RSA public key)",
			setupEnv: func() string {
				return `-----BEGIN PUBLIC KEY-----
MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAinvalid
-----END PUBLIC KEY-----`
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			envValue := tt.setupEnv()
			if envValue != "" {
				os.Setenv(PublicKeyEnvVar, envValue)
				defer os.Unsetenv(PublicKeyEnvVar)
			} else {
				os.Unsetenv(PublicKeyEnvVar)
			}

			manager := NewAuthManager()
			err := manager.LoadPublicKeyFromEnv()

			if tt.wantErr {
				assert.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestAuthMiddleware_HeaderValidation(t *testing.T) {
	_, pubKeyPEM, err := generateTestRSAKeyPair()
	require.NoError(t, err)

	os.Setenv(PublicKeyEnvVar, pubKeyPEM)
	defer os.Unsetenv(PublicKeyEnvVar)

	manager := NewAuthManager()
	err = manager.LoadPublicKeyFromEnv()
	require.NoError(t, err)

	tests := []struct {
		name           string
		authHeader     string
		expectedStatus int
		errorContains  string
	}{
		{
			name:           "missing Authorization header",
			authHeader:     "",
			expectedStatus: http.StatusUnauthorized,
			errorContains:  "Missing Authorization header",
		},
		{
			name:           "no Bearer prefix",
			authHeader:     "token123",
			expectedStatus: http.StatusUnauthorized,
			errorContains:  "Invalid Authorization header format",
		},
		{
			name:           "wrong prefix",
			authHeader:     "Basic token123",
			expectedStatus: http.StatusUnauthorized,
			errorContains:  "Invalid Authorization header format",
		},
		{
			name:           "empty token after Bearer",
			authHeader:     "Bearer ",
			expectedStatus: http.StatusUnauthorized,
			errorContains:  "JWT verification failed",
		},
		{
			name:           "multiple spaces",
			authHeader:     "Bearer  token123",
			expectedStatus: http.StatusUnauthorized,
			errorContains:  "Invalid Authorization header format",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request, _ = http.NewRequest("POST", "/api/execute", nil)
			if tt.authHeader != "" {
				c.Request.Header.Set("Authorization", tt.authHeader)
			}

			handler := manager.AuthMiddleware()
			handler(c)

			assert.Equal(t, tt.expectedStatus, w.Code)
			assert.Contains(t, w.Body.String(), tt.errorContains)
		})
	}
}

func TestAuthMiddleware_TokenValidation(t *testing.T) {
	privateKey, pubKeyPEM, err := generateTestRSAKeyPair()
	require.NoError(t, err)

	os.Setenv(PublicKeyEnvVar, pubKeyPEM)
	defer os.Unsetenv(PublicKeyEnvVar)

	manager := NewAuthManager()
	err = manager.LoadPublicKeyFromEnv()
	require.NoError(t, err)

	tests := []struct {
		name           string
		setupToken     func() string
		expectedStatus int
		errorContains  string
	}{
		{
			name: "valid token",
			setupToken: func() string {
				token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
					"exp": time.Now().Add(time.Hour).Unix(),
					"iat": time.Now().Unix(),
				})
				tokenString, _ := token.SignedString(privateKey)
				return tokenString
			},
			expectedStatus: http.StatusOK,
		},
		{
			name: "expired token",
			setupToken: func() string {
				token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
					"exp": time.Now().Add(-time.Hour).Unix(),
					"iat": time.Now().Add(-2 * time.Hour).Unix(),
				})
				tokenString, _ := token.SignedString(privateKey)
				return tokenString
			},
			expectedStatus: http.StatusUnauthorized,
			errorContains:  "Invalid token",
		},
		{
			name: "invalid signature",
			setupToken: func() string {
				wrongPrivateKey, _ := rsa.GenerateKey(rand.Reader, 2048)
				token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
					"exp": time.Now().Add(time.Hour).Unix(),
					"iat": time.Now().Unix(),
				})
				tokenString, _ := token.SignedString(wrongPrivateKey)
				return tokenString
			},
			expectedStatus: http.StatusUnauthorized,
			errorContains:  "Invalid token",
		},
		{
			name: "wrong signing method (HS256)",
			setupToken: func() string {
				token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
					"exp": time.Now().Add(time.Hour).Unix(),
					"iat": time.Now().Unix(),
				})
				tokenString, _ := token.SignedString([]byte("secret"))
				return tokenString
			},
			expectedStatus: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokenString := tt.setupToken()

			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request, _ = http.NewRequest("POST", "/api/execute", nil)
			c.Request.Header.Set("Authorization", "Bearer "+tokenString)

			handler := manager.AuthMiddleware()
			handler(c)

			if tt.expectedStatus == http.StatusOK {
				assert.NotEqual(t, http.StatusUnauthorized, w.Code)
			} else {
				assert.Equal(t, tt.expectedStatus, w.Code)
				if tt.errorContains != "" {
					assert.Contains(t, w.Body.String(), tt.errorContains)
				}
			}
		})
	}
}

func TestAuthMiddleware_MaxBodySize(t *testing.T) {
	privateKey, pubKeyPEM, err := generateTestRSAKeyPair()
	require.NoError(t, err)

	os.Setenv(PublicKeyEnvVar, pubKeyPEM)
	defer os.Unsetenv(PublicKeyEnvVar)

	manager := NewAuthManager()
	err = manager.LoadPublicKeyFromEnv()
	require.NoError(t, err)

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"exp": time.Now().Add(time.Hour).Unix(),
		"iat": time.Now().Unix(),
	})
	tokenString, err := token.SignedString(privateKey)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/api/execute", nil)
	c.Request.Header.Set("Authorization", "Bearer "+tokenString)

	handler := manager.AuthMiddleware()
	handler(c)

	assert.NotNil(t, c.Request.Body)
}
