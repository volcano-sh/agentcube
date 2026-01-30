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

package workloadmanager

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testToken = "test-token"
	testServiceAccount = "system:serviceaccount:default:test-sa"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func setupTestServerWithAuth(enableAuth bool) *Server {
	// Create a minimal K8sClient for testing
	// We'll use nil clientset and test with cached tokens to avoid API calls
	k8sClient := &K8sClient{}
	tokenCache := NewTokenCache(100, 5*time.Minute)

	return &Server{
		config: &Config{
			EnableAuth: enableAuth,
		},
		k8sClient:  k8sClient,
		tokenCache: tokenCache,
	}
}

func TestAuthMiddleware_AuthDisabled(t *testing.T) {
	server := setupTestServerWithAuth(false)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/test", nil)

	server.authMiddleware(c)

	// Should pass through without authentication
	assert.Equal(t, http.StatusOK, w.Code)
	assert.False(t, c.IsAborted())
}

func TestAuthMiddleware_InvalidHeaderFormat(t *testing.T) {
	server := setupTestServerWithAuth(true)

	tests := []struct {
		name             string
		header           string
		expectedBodyPart string
	}{
		{
			name:             "missing authorization header",
			header:           "",
			expectedBodyPart: "Missing authorization header",
		},
		{
			name:             "no Bearer prefix",
			header:           "token123",
			expectedBodyPart: "Invalid authorization header format",
 		},
		{
			name:             "wrong prefix",
			header:           "Basic token123",
			expectedBodyPart: "Invalid authorization header format",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request, _ = http.NewRequest("GET", "/test", nil)

			if tt.header != "" {
				c.Request.Header.Set("Authorization", tt.header)
			}

			server.authMiddleware(c)

			assert.Equal(t, http.StatusUnauthorized, w.Code)
			assert.True(t, c.IsAborted())
			assert.Contains(t, w.Body.String(), tt.expectedBodyPart)
		})
	}
}

func TestAuthMiddleware_InvalidServiceAccountFormat(t *testing.T) {
	server := setupTestServerWithAuth(true)

	// Test with a token that has invalid username format (not in system:serviceaccount: format)
	token := testToken
	server.tokenCache.Set(token, true, "invalid-format") // Not in system:serviceaccount: format

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/test", nil)
	c.Request.Header.Set("Authorization", "Bearer "+token)

	// Since token is cached, it will be used but format validation will fail
	server.authMiddleware(c)

	// Should fail on format validation
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.True(t, c.IsAborted())
}

func TestValidateServiceAccountToken_CacheHit(t *testing.T) {
	server := setupTestServerWithAuth(true)

	tests := []struct {
		name           string
		token          string
		authenticated  bool
		serviceAccount string
		expectAuth     bool
		expectSA       string
	}{
		{
			name:           "authenticated cache hit",
			token:          "test-token-123",
			authenticated:  true,
			serviceAccount: testServiceAccount,
			expectAuth:     true,
			expectSA:       testServiceAccount,
		},
		{
			name:           "unauthenticated cache hit",
			token:          "invalid-token",
			authenticated:  false,
			serviceAccount: "",
			expectAuth:     false,
			expectSA:       "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server.tokenCache.Set(tt.token, tt.authenticated, tt.serviceAccount)

			authenticated, serviceAccount, err := server.validateServiceAccountToken(context.Background(), tt.token)

			assert.NoError(t, err)
			assert.Equal(t, tt.expectAuth, authenticated)
			assert.Equal(t, tt.expectSA, serviceAccount)
		})
	}
}

// Note: Tests for API call failures are removed because they require a real clientset
// and would panic with nil clientset. These scenarios are better tested in integration tests.

// Note: TestExtractUserInfo removed - it only verified that context values
// match what was set, which is trivial getter behavior. The extractUserInfo
// function is tested indirectly through authMiddleware tests.

func TestAuthMiddleware_ServiceAccountParsing(t *testing.T) {
	server := setupTestServerWithAuth(true)

	tests := []struct {
		name           string
		username       string
		shouldSucceed  bool
		expectedNS     string
		expectedSAName string
	}{
		{
			name:           "valid format",
			username:       testServiceAccount,
			shouldSucceed:  true,
			expectedNS:     "default",
			expectedSAName: "test-sa",
		},
		{
			name:          "invalid format - too few parts",
			username:      "system:serviceaccount:default",
			shouldSucceed: false,
		},
		{
			name:          "invalid format - wrong prefix",
			username:      "user:serviceaccount:default:test-sa",
			shouldSucceed: false,
		},
		{
			name:          "invalid format - wrong second part",
			username:      "system:user:default:test-sa",
			shouldSucceed: false,
		},
		{
			name:           "valid format with different namespace",
			username:       "system:serviceaccount:kube-system:admin-sa",
			shouldSucceed:  true,
			expectedNS:     "kube-system",
			expectedSAName: "admin-sa",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token := "token-" + tt.name
			server.tokenCache.Set(token, true, tt.username)

			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request, _ = http.NewRequest("GET", "/test", nil)
			c.Request.Header.Set("Authorization", "Bearer "+token)

			server.authMiddleware(c)
			// Create a handler that checks context values
			handler := func(c *gin.Context) {
				if tt.shouldSucceed {
					tokenVal, _ := c.Request.Context().Value(contextKeyUserToken).(string)
					namespace, _ := c.Request.Context().Value(contextKeyNamespace).(string)
					sa, _ := c.Request.Context().Value(contextKeyServiceAccount).(string)
					saName, _ := c.Request.Context().Value(contextKeyServiceAccountName).(string)

					assert.Equal(t, token, tokenVal)
					assert.Equal(t, tt.expectedNS, namespace)
					assert.Equal(t, tt.username, sa)
					assert.Equal(t, tt.expectedSAName, saName)
				}
				c.JSON(http.StatusOK, gin.H{"status": "ok"})
			}

			// Use router to chain handlers
			router := gin.New()
			router.Use(server.authMiddleware)
			router.GET("/test", handler)
			router.ServeHTTP(w, c.Request)

			if tt.shouldSucceed {
				assert.Equal(t, http.StatusOK, w.Code)
				assert.False(t, c.IsAborted(), "Should not abort for valid format")
			} else {
				assert.Equal(t, http.StatusUnauthorized, w.Code)
				assert.True(t, c.IsAborted(), "Should abort for invalid format")
			}
		})
	}
}

func TestValidateServiceAccountToken_CacheBehavior(t *testing.T) {
	server := setupTestServerWithAuth(true)

	// Test that cache is used on second call
	token := "cache-test-token"
	username := testServiceAccount

	// First call - cache miss, will try API (but we don't have real client)
	// So we'll set it in cache first
	server.tokenCache.Set(token, true, username)

	// First call - should hit cache
	authenticated1, sa1, err1 := server.validateServiceAccountToken(context.Background(), token)
	require.NoError(t, err1)
	assert.True(t, authenticated1)
	assert.Equal(t, username, sa1)

	// Second call - should also hit cache
	authenticated2, sa2, err2 := server.validateServiceAccountToken(context.Background(), token)
	require.NoError(t, err2)
	assert.True(t, authenticated2)
	assert.Equal(t, username, sa2)

	// Verify cache size increased
	assert.Greater(t, server.tokenCache.Size(), 0)
}
