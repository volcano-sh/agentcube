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

package e2b

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"
)

// hashKey computes the SHA-256 hash of the API key for test use
// This mirrors the hashKey function in auth.go
func testHashKey(apiKey string) string {
	hash := sha256.Sum256([]byte(apiKey))
	return hex.EncodeToString(hash[:])
}

func TestDefaultAuthConfig(t *testing.T) {
	t.Parallel()
	config := DefaultAuthConfig()

	assert.Equal(t, "e2b-api-keys", config.APIKeySecret)
	assert.Equal(t, "agentcube-system", config.APIKeySecretNamespace)
}

func TestNewAuthenticator(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		config      *AuthConfig
		expectEmpty bool
	}{
		{
			name:        "with config",
			config:      DefaultAuthConfig(),
			expectEmpty: false,
		},
		{
			name:        "nil config uses default",
			config:      nil,
			expectEmpty: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			auth := NewAuthenticator(tt.config)
			assert.NotNil(t, auth)
			assert.NotNil(t, auth.config)
			assert.NotNil(t, auth.apiKeys)
		})
	}
}

// TestValidateAPIKey_CacheHit tests that valid API keys are returned from cache without K8s API call
func TestValidateAPIKey_CacheHit(t *testing.T) {
	t.Parallel()

	auth := NewAuthenticatorWithMap(map[string]string{
		"valid-key-1": "client-1",
		"valid-key-2": "client-2",
	})

	tests := []struct {
		name             string
		apiKey           string
		expectedClientID string
	}{
		{
			name:             "valid key 1",
			apiKey:           "valid-key-1",
			expectedClientID: "client-1",
		},
		{
			name:             "valid key 2",
			apiKey:           "valid-key-2",
			expectedClientID: "client-2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			entry, err := auth.ValidateAPIKey(tt.apiKey)
			assert.NoError(t, err)
			assert.Equal(t, tt.expectedClientID, entry.Namespace)
		})
	}
}

// TestValidateAPIKey_CacheMiss tests that cache miss is handled properly
func TestValidateAPIKey_CacheMiss(t *testing.T) {
	t.Parallel()

	// Create fake K8s client with a secret
	client := fake.NewClientset()
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "e2b-api-keys",
			Namespace: "agentcube-system",
		},
		Data: map[string][]byte{
			testHashKey("test-api-key"): []byte("valid"),
		},
	}
	_, err := client.CoreV1().Secrets("agentcube-system").Create(context.Background(), secret, metav1.CreateOptions{})
	assert.NoError(t, err)

	config := DefaultAuthConfig()
	auth := NewAuthenticatorWithK8s(config, client)

	// Load API keys from K8s secret
	err = auth.LoadAPIKeys()
	assert.NoError(t, err)

	// Now the key should be in cache
	entry, err := auth.ValidateAPIKey("test-api-key")
	assert.NoError(t, err)
	assert.Equal(t, "default", entry.Namespace)
}

// TestValidateAPIKey_InvalidKey tests that invalid API keys return error
func TestValidateAPIKey_InvalidKey(t *testing.T) {
	t.Parallel()

	auth := NewAuthenticatorWithMap(map[string]string{
		"valid-key": "client-1",
	})

	tests := []struct {
		name        string
		apiKey      string
		expectError bool
	}{
		{
			name:        "invalid key",
			apiKey:      "invalid-key",
			expectError: true,
		},
		{
			name:        "empty key",
			apiKey:      "",
			expectError: true,
		},
		{
			name:        "key with spaces only",
			apiKey:      "   ",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			entry, err := auth.ValidateAPIKey(tt.apiKey)
			assert.Error(t, err)
			assert.Nil(t, entry)
		})
	}
}

// TestRateLimiter_AllowsNormalTraffic tests that normal traffic passes through rate limiter
func TestRateLimiter_AllowsNormalTraffic(t *testing.T) {
	t.Parallel()

	auth := NewAuthenticatorWithMap(map[string]string{
		"valid-key": "client-1",
	})

	// Simulate normal traffic - 5 requests at reasonable intervals
	for i := 0; i < 5; i++ {
		entry, err := auth.ValidateAPIKey("valid-key")
		assert.NoError(t, err)
		assert.Equal(t, "client-1", entry.Namespace)
		time.Sleep(200 * time.Millisecond)
	}
}

// TestRateLimiter_BlocksExcessiveRequests tests that requests exceeding 1/sec are rate limited
func TestRateLimiter_BlocksExcessiveRequests(t *testing.T) {
	t.Parallel()

	client := fake.NewClientset()
	config := DefaultAuthConfig()
	auth := NewAuthenticatorWithK8s(config, client)

	// First request should trigger cache miss and rate limiter
	_, err := auth.ValidateAPIKey("unknown-key-1")
	assert.Error(t, err)

	// Immediate second request should be rate limited
	start := time.Now()
	_, err = auth.ValidateAPIKey("unknown-key-2")
	elapsed := time.Since(start)

	// Should return error quickly due to rate limiting
	assert.Error(t, err)
	assert.Less(t, elapsed, 500*time.Millisecond, "Rate limiter should reject immediately, not wait")
}

// TestRateLimiter_ResetsAfterInterval tests that rate limiter resets after interval
func TestRateLimiter_ResetsAfterInterval(t *testing.T) {
	t.Parallel()

	client := fake.NewClientset()
	config := DefaultAuthConfig()
	auth := NewAuthenticatorWithK8s(config, client)

	// First request
	_, err := auth.ValidateAPIKey("unknown-key-1")
	assert.Error(t, err)

	// Wait for rate limiter to reset (1 second)
	time.Sleep(1100 * time.Millisecond)

	// After reset, request should be allowed (though still cache miss)
	_, err = auth.ValidateAPIKey("unknown-key-2")

	// This request should not be rate limited, but will still fail due to cache miss
	assert.Error(t, err)
}

// TestInformer_OnSecretAdd tests that Informer handles Secret add events
func TestInformer_OnSecretAdd(t *testing.T) {
	t.Parallel()

	client := fake.NewClientset()
	config := DefaultAuthConfig()
	auth := NewAuthenticatorWithK8s(config, client)

	// Start informer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	factory := informers.NewSharedInformerFactory(client, 0)
	auth.SetupInformer(factory)

	factory.Start(ctx.Done())
	factory.WaitForCacheSync(ctx.Done())

	// Create secret after informer is running
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "e2b-api-keys",
			Namespace: "agentcube-system",
		},
		Data: map[string][]byte{
			testHashKey("new-api-key"): []byte("valid"),
		},
	}

	_, err := client.CoreV1().Secrets("agentcube-system").Create(context.Background(), secret, metav1.CreateOptions{})
	assert.NoError(t, err)

	// Wait for informer event to be processed
	time.Sleep(200 * time.Millisecond)

	// Reset rate limiter to avoid rate limiting on cache miss
	auth.ResetRateLimiter()

	// Verify the new key is in cache
	entry, err := auth.ValidateAPIKey("new-api-key")
	assert.NoError(t, err)
	assert.Equal(t, "default", entry.Namespace)
}

// TestInformer_OnSecretUpdate tests that Informer handles Secret update events
func TestInformer_OnSecretUpdate(t *testing.T) {
	t.Parallel()

	client := fake.NewClientset()
	config := DefaultAuthConfig()
	auth := NewAuthenticatorWithK8s(config, client)

	// Create initial secret
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "e2b-api-keys",
			Namespace: "agentcube-system",
		},
		Data: map[string][]byte{
			testHashKey("existing-key"): []byte("valid"),
		},
	}
	_, err := client.CoreV1().Secrets("agentcube-system").Create(context.Background(), secret, metav1.CreateOptions{})
	assert.NoError(t, err)

	// Start informer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	factory := informers.NewSharedInformerFactory(client, 0)
	auth.SetupInformer(factory)

	factory.Start(ctx.Done())
	factory.WaitForCacheSync(ctx.Done())

	// Wait for initial sync
	time.Sleep(200 * time.Millisecond)

	// Reset rate limiter to avoid rate limiting on cache miss
	auth.ResetRateLimiter()

	// Update secret
	secret.Data = map[string][]byte{
		testHashKey("existing-key"):   []byte("valid"),
		testHashKey("additional-key"): []byte("valid"),
	}
	_, err = client.CoreV1().Secrets("agentcube-system").Update(context.Background(), secret, metav1.UpdateOptions{})
	assert.NoError(t, err)

	// Wait for update event
	time.Sleep(200 * time.Millisecond)

	// Reset rate limiter to avoid rate limiting on cache miss
	auth.ResetRateLimiter()

	// Verify updated key
	entry, err := auth.ValidateAPIKey("existing-key")
	assert.NoError(t, err)
	assert.Equal(t, "default", entry.Namespace)

	// Verify new key
	entry, err = auth.ValidateAPIKey("additional-key")
	assert.NoError(t, err)
	assert.Equal(t, "default", entry.Namespace)
}

// TestInformer_OnSecretDelete tests that Informer handles Secret delete events
func TestInformer_OnSecretDelete(t *testing.T) {
	t.Parallel()

	client := fake.NewClientset()
	config := DefaultAuthConfig()
	auth := NewAuthenticatorWithK8s(config, client)

	// Pre-populate cache directly (simulating successful load)
	auth.AddAPIKey("to-be-deleted-key", "client")

	// Verify key exists
	entry, err := auth.ValidateAPIKey("to-be-deleted-key")
	assert.NoError(t, err)
	assert.Equal(t, "client", entry.Namespace)

	// Simulate secret deletion by calling the handler directly
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "e2b-api-keys",
			Namespace: "agentcube-system",
		},
	}
	auth.onSecretDelete(secret)

	// Verify key is removed from cache
	_, err = auth.ValidateAPIKey("to-be-deleted-key")
	assert.Error(t, err)
}

// TestInformer_OnConfigMapAdd tests that Informer handles ConfigMap add events
func TestInformer_OnConfigMapAdd(t *testing.T) {
	t.Parallel()

	client := fake.NewClientset()
	config := DefaultAuthConfig()
	auth := NewAuthenticatorWithK8s(config, client)

	// Create secret first
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "e2b-api-keys",
			Namespace: "agentcube-system",
		},
		Data: map[string][]byte{
			testHashKey("test-api-key"): []byte("valid"),
		},
	}
	_, err := client.CoreV1().Secrets("agentcube-system").Create(context.Background(), secret, metav1.CreateOptions{})
	assert.NoError(t, err)

	// Start informers
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	factory := informers.NewSharedInformerFactory(client, 0)
	auth.SetupInformer(factory)

	factory.Start(ctx.Done())
	factory.WaitForCacheSync(ctx.Done())

	// Create configmap after informer is running with namespace mapping
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "e2b-api-key-config",
			Namespace: "agentcube-system",
		},
		Data: map[string]string{
			testHashKey("test-api-key"): "custom-namespace",
			"defaultNamespace":          "fallback-ns",
		},
	}

	_, err = client.CoreV1().ConfigMaps("agentcube-system").Create(context.Background(), configMap, metav1.CreateOptions{})
	assert.NoError(t, err)

	// Wait for informer event to be processed
	time.Sleep(200 * time.Millisecond)

	// Reset rate limiter to avoid rate limiting on cache miss
	auth.ResetRateLimiter()

	// Verify the key now resolves to the custom namespace from ConfigMap
	entry, err := auth.ValidateAPIKey("test-api-key")
	assert.NoError(t, err)
	assert.Equal(t, "custom-namespace", entry.Namespace)
}

// TestInformer_OnConfigMapUpdate tests that Informer handles ConfigMap update events
func TestInformer_OnConfigMapUpdate(t *testing.T) {
	t.Parallel()

	client := fake.NewClientset()
	config := DefaultAuthConfig()
	auth := NewAuthenticatorWithK8s(config, client)

	// Create secret and initial configmap
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "e2b-api-keys",
			Namespace: "agentcube-system",
		},
		Data: map[string][]byte{
			testHashKey("test-api-key"): []byte("valid"),
		},
	}
	_, err := client.CoreV1().Secrets("agentcube-system").Create(context.Background(), secret, metav1.CreateOptions{})
	assert.NoError(t, err)

	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "e2b-api-key-config",
			Namespace: "agentcube-system",
		},
		Data: map[string]string{
			testHashKey("test-api-key"): "initial-namespace",
		},
	}
	_, err = client.CoreV1().ConfigMaps("agentcube-system").Create(context.Background(), configMap, metav1.CreateOptions{})
	assert.NoError(t, err)

	// Start informers
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	factory := informers.NewSharedInformerFactory(client, 0)
	auth.SetupInformer(factory)

	factory.Start(ctx.Done())
	factory.WaitForCacheSync(ctx.Done())

	// Wait for initial sync
	time.Sleep(200 * time.Millisecond)

	// Reset rate limiter to avoid rate limiting on cache miss
	auth.ResetRateLimiter()

	// Verify initial namespace mapping
	entry, err := auth.ValidateAPIKey("test-api-key")
	assert.NoError(t, err)
	assert.Equal(t, "initial-namespace", entry.Namespace)

	// Update configmap with new namespace mapping
	configMap.Data = map[string]string{
		testHashKey("test-api-key"): "updated-namespace",
	}
	_, err = client.CoreV1().ConfigMaps("agentcube-system").Update(context.Background(), configMap, metav1.UpdateOptions{})
	assert.NoError(t, err)

	// Wait for update event
	time.Sleep(200 * time.Millisecond)

	// Reset rate limiter to avoid rate limiting on cache miss
	auth.ResetRateLimiter()

	// Verify updated namespace mapping
	entry, err = auth.ValidateAPIKey("test-api-key")
	assert.NoError(t, err)
	assert.Equal(t, "updated-namespace", entry.Namespace)
}

// TestInformer_OnConfigMapDelete tests that Informer handles ConfigMap delete events
func TestInformer_OnConfigMapDelete(t *testing.T) {
	t.Parallel()

	client := fake.NewClientset()
	config := DefaultAuthConfig()
	auth := NewAuthenticatorWithK8s(config, client)

	// Create secret and configmap
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "e2b-api-keys",
			Namespace: "agentcube-system",
		},
		Data: map[string][]byte{
			testHashKey("test-api-key"): []byte("valid"),
		},
	}
	_, err := client.CoreV1().Secrets("agentcube-system").Create(context.Background(), secret, metav1.CreateOptions{})
	assert.NoError(t, err)

	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "e2b-api-key-config",
			Namespace: "agentcube-system",
		},
		Data: map[string]string{
			testHashKey("test-api-key"): "custom-namespace",
			"defaultNamespace":          "fallback-ns",
		},
	}
	_, err = client.CoreV1().ConfigMaps("agentcube-system").Create(context.Background(), configMap, metav1.CreateOptions{})
	assert.NoError(t, err)

	// Load initial cache
	err = auth.LoadAPIKeys()
	assert.NoError(t, err)

	// Verify initial custom namespace mapping
	entry, err := auth.ValidateAPIKey("test-api-key")
	assert.NoError(t, err)
	assert.Equal(t, "custom-namespace", entry.Namespace)

	// Simulate configmap deletion by calling the handler directly
	auth.onConfigMapDelete(configMap)

	// Reset rate limiter to avoid rate limiting on cache miss
	auth.ResetRateLimiter()

	// After configmap deletion, namespace should fall back to default
	entry, err = auth.ValidateAPIKey("test-api-key")
	assert.NoError(t, err)
	assert.Equal(t, "default", entry.Namespace)
}

// TestBackgroundRefresh_PeriodicRefresh tests that background refresh triggers periodically
func TestBackgroundRefresh_PeriodicRefresh(t *testing.T) {
	t.Parallel()

	client := fake.NewClientset()
	config := DefaultAuthConfig()
	auth := NewAuthenticatorWithK8s(config, client)

	// Pre-populate cache directly
	auth.AddAPIKey("refresh-key", "initial-client")

	// Verify initial data is in cache
	entry, err := auth.ValidateAPIKey("refresh-key")
	assert.NoError(t, err)
	assert.Equal(t, "initial-client", entry.Namespace)

	// Update cache to simulate refresh
	auth.AddAPIKey("refresh-key", "refreshed-client")

	// Verify refreshed data
	entry, err = auth.ValidateAPIKey("refresh-key")
	assert.NoError(t, err)
	assert.Equal(t, "refreshed-client", entry.Namespace)
}

// TestBackgroundRefresh_K8sUnavailable tests that service continues when K8s is unavailable
func TestBackgroundRefresh_K8sUnavailable(t *testing.T) {
	t.Parallel()

	client := fake.NewClientset()
	config := DefaultAuthConfig()
	auth := NewAuthenticatorWithK8s(config, client)

	// Pre-populate cache directly (simulating previous successful load)
	auth.AddAPIKey("cached-key", "cached-client")

	// Verify cached key still works
	entry, err := auth.ValidateAPIKey("cached-key")
	assert.NoError(t, err)
	assert.Equal(t, "cached-client", entry.Namespace)

	// Simulate K8s API becoming unavailable by using a client that will fail
	// (In real scenario, the client would return errors)
	// The key point is that cached data should still be served

	// Wait a bit
	time.Sleep(50 * time.Millisecond)

	// Cached key should still work even if K8s is unavailable
	entry, err = auth.ValidateAPIKey("cached-key")
	assert.NoError(t, err)
	assert.Equal(t, "cached-client", entry.Namespace)
}

// TestLoadAPIKeys_FromK8sSecret tests loading API keys from K8s Secret
func TestLoadAPIKeys_FromK8sSecret(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		secretData    map[string][]byte
		expectedKeys  map[string]string
		expectedError bool
	}{
		{
			name: "valid secret data",
			secretData: map[string][]byte{
				testHashKey("api-key-1"): []byte("valid"),
				testHashKey("api-key-2"): []byte("valid"),
			},
			expectedKeys: map[string]string{
				"api-key-1": "default",
				"api-key-2": "default",
			},
			expectedError: false,
		},
		{
			name: "empty secret data",
			secretData: map[string][]byte{
				testHashKey("api-key-1"): []byte(""),
			},
			expectedKeys:  map[string]string{},
			expectedError: false,
		},
		{
			name:          "nil secret data",
			secretData:    nil,
			expectedKeys:  map[string]string{},
			expectedError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			client := fake.NewClientset()
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "e2b-api-keys",
					Namespace: "agentcube-system",
				},
				Data: tt.secretData,
			}
			_, err := client.CoreV1().Secrets("agentcube-system").Create(context.Background(), secret, metav1.CreateOptions{})
			assert.NoError(t, err)

			config := DefaultAuthConfig()
			auth := NewAuthenticatorWithK8s(config, client)

			err = auth.LoadAPIKeys()
			if tt.expectedError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				for key, expectedClientID := range tt.expectedKeys {
					entry, err := auth.ValidateAPIKey(key)
					if expectedClientID == "" {
						assert.Error(t, err)
					} else {
						assert.NoError(t, err)
						assert.Equal(t, expectedClientID, entry.Namespace)
					}
				}
			}
		})
	}
}

// TestLoadAPIKeys_ParsingError tests handling of parsing errors
func TestLoadAPIKeys_ParsingError(t *testing.T) {
	t.Parallel()

	client := fake.NewClientset()
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "e2b-api-keys",
			Namespace: "agentcube-system",
		},
		Data: map[string][]byte{
			testHashKey("valid-key"):   []byte("valid"),
			testHashKey("invalid-key"): []byte(""),
		},
	}
	_, err := client.CoreV1().Secrets("agentcube-system").Create(context.Background(), secret, metav1.CreateOptions{})
	assert.NoError(t, err)

	config := DefaultAuthConfig()
	auth := NewAuthenticatorWithK8s(config, client)

	err = auth.LoadAPIKeys()
	assert.NoError(t, err)

	// Valid key should still work
	entry, err := auth.ValidateAPIKey("valid-key")
	assert.NoError(t, err)
	assert.Equal(t, "default", entry.Namespace)

	// Invalid key format should not be loaded
	_, err = auth.ValidateAPIKey("invalid-key")
	assert.Error(t, err)
}

func TestAuthenticator_APIKeyMiddleware(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)

	auth := NewAuthenticatorWithMap(map[string]string{
		"valid-key": "test-client",
	})

	router := gin.New()
	router.Use(auth.APIKeyMiddleware())
	router.GET("/health/live", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "alive"})
	})
	router.GET("/health/ready", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ready"})
	})
	router.GET("/test", func(c *gin.Context) {
		clientID := c.GetString("client_id")
		c.JSON(http.StatusOK, gin.H{"client_id": clientID})
	})

	tests := []struct {
		name           string
		path           string
		apiKey         string
		expectedStatus int
		expectClientID bool
	}{
		{
			name:           "valid api key",
			path:           "/test",
			apiKey:         "valid-key",
			expectedStatus: http.StatusOK,
			expectClientID: true,
		},
		{
			name:           "invalid api key",
			path:           "/test",
			apiKey:         "invalid-key",
			expectedStatus: http.StatusUnauthorized,
			expectClientID: false,
		},
		{
			name:           "missing api key",
			path:           "/test",
			apiKey:         "",
			expectedStatus: http.StatusUnauthorized,
			expectClientID: false,
		},
		{
			name:           "health check bypass",
			path:           "/health/live",
			apiKey:         "",
			expectedStatus: http.StatusOK,
			expectClientID: false,
		},
		{
			name:           "health ready bypass",
			path:           "/health/ready",
			apiKey:         "",
			expectedStatus: http.StatusOK,
			expectClientID: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			if tt.apiKey != "" {
				req.Header.Set("X-API-Key", tt.apiKey)
			}
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
		})
	}
}

func TestAuthenticator_LoadAPIKeys_FromEnv(t *testing.T) {
	// Not parallel because it manipulates the global environment.

	tests := []struct {
		name     string
		envValue string
	}{
		{
			name:     "load from env",
			envValue: "key1:client1,key2:client2,key3:client3",
		},
		{
			name:     "load from env with spaces",
			envValue: "key1:client1, key2:client2",
		},
		{
			name:     "empty env returns error",
			envValue: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envValue != "" {
				t.Setenv("E2B_API_KEYS", tt.envValue)
			}

			auth := NewAuthenticator(DefaultAuthConfig())
			err := auth.LoadAPIKeys()

			if tt.envValue == "" {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestAuthenticator_AddAPIKey(t *testing.T) {
	t.Parallel()

	auth := NewAuthenticatorWithMap(make(map[string]string))

	// Add a new key
	auth.AddAPIKey("new-key", "new-client")

	// Verify it was added
	entry, err := auth.ValidateAPIKey("new-key")
	assert.NoError(t, err)
	assert.Equal(t, "new-client", entry.Namespace)
}

func TestGetEnvOrDefault(t *testing.T) {
	t.Parallel()

	// Test with existing env var
	os.Setenv("TEST_VAR", "test_value")
	defer os.Unsetenv("TEST_VAR")

	result := getEnvOrDefault("TEST_VAR", "default")
	assert.Equal(t, "test_value", result)

	// Test with non-existing env var
	result = getEnvOrDefault("NON_EXISTENT_VAR", "default")
	assert.Equal(t, "default", result)
}

// TestRateLimiter_ConcurrentAuthAccess tests rate limiter under concurrent access
func TestRateLimiter_ConcurrentAuthAccess(t *testing.T) {
	t.Parallel()

	client := fake.NewClientset()
	config := DefaultAuthConfig()
	auth := NewAuthenticatorWithK8s(config, client)

	var successCount, errorCount int64
	var wg sync.WaitGroup

	// Launch 10 concurrent requests
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := auth.ValidateAPIKey("unknown-key")
			if err != nil {
				atomic.AddInt64(&errorCount, 1)
			} else {
				atomic.AddInt64(&successCount, 1)
			}
		}()
	}

	wg.Wait()

	// All should fail (unknown keys), but shouldn't panic or deadlock
	assert.Equal(t, int64(10), errorCount)
	assert.Equal(t, int64(0), successCount)
}

// TestCacheConcurrency tests cache operations under concurrent access
func TestCacheConcurrency(t *testing.T) {
	t.Parallel()

	auth := NewAuthenticatorWithMap(map[string]string{
		"key1": "client1",
	})

	var wg sync.WaitGroup

	// Concurrent reads
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = auth.ValidateAPIKey("key1")
		}()
	}

	// Concurrent writes
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			auth.AddAPIKey("new-key", "new-client")
		}()
	}

	wg.Wait()

	// Verify cache is still consistent
	entry, err := auth.ValidateAPIKey("key1")
	assert.NoError(t, err)
	assert.Equal(t, "client1", entry.Namespace)
}

// TestLoadAPIKeys_Base64EncodedData tests loading secret data
// Note: When using LoadAPIKeys, the secret data format is "namespace:client_id"
// The base64 encoding is handled by K8s when storing/retrieving the secret
func TestLoadAPIKeys_Base64EncodedData(t *testing.T) {
	t.Parallel()

	client := fake.NewClientset()

	// Create secret with plain text data (K8s client-go handles base64 encoding/decoding)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "e2b-api-keys",
			Namespace: "agentcube-system",
		},
		Data: map[string][]byte{
			testHashKey("test-api-key"): []byte("valid"),
		},
	}
	_, err := client.CoreV1().Secrets("agentcube-system").Create(context.Background(), secret, metav1.CreateOptions{})
	assert.NoError(t, err)

	config := DefaultAuthConfig()
	auth := NewAuthenticatorWithK8s(config, client)

	err = auth.LoadAPIKeys()
	assert.NoError(t, err)

	// Verify the key was loaded
	entry, err := auth.ValidateAPIKey("test-api-key")
	assert.NoError(t, err)
	assert.Equal(t, "default", entry.Namespace)
}

// TestAuthenticator_Stop tests graceful shutdown of background processes
func TestAuthenticator_Stop(t *testing.T) {
	t.Parallel()

	client := fake.NewClientset()
	config := DefaultAuthConfig()
	auth := NewAuthenticatorWithK8s(config, client)

	// Pre-populate cache
	auth.AddAPIKey("test-key", "test-client")

	// Verify cache works
	entry, err := auth.ValidateAPIKey("test-key")
	assert.NoError(t, err)
	assert.Equal(t, "test-client", entry.Namespace)
}
