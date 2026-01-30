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
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

const (
	jwtHeader = `{"alg":"HS256","typ":"JWT"}`
	testCacheKey = "default:test-sa"
)

func createTestJWT(exp int64) string {
	header := jwtHeader
	claims := map[string]interface{}{
		"exp": exp,
		"iat": time.Now().Unix(),
		"sub": "test-user",
	}
	claimsJSON, _ := json.Marshal(claims)

	headerB64 := base64.RawURLEncoding.EncodeToString([]byte(header))
	claimsB64 := base64.RawURLEncoding.EncodeToString(claimsJSON)
	signature := base64.RawURLEncoding.EncodeToString([]byte("signature"))

	return headerB64 + "." + claimsB64 + "." + signature
}

func TestParseJWTExpiry(t *testing.T) {
	// Precompute various token forms used across test cases.
	validExp := time.Now().Add(1 * time.Hour).Unix()
	validToken := createTestJWT(validExp)

	// Token without exp claim.
	header := jwtHeader
	noExpClaims := map[string]interface{}{
		"iat": time.Now().Unix(),
		"sub": "test-user",
	}
	noExpClaimsJSON, _ := json.Marshal(noExpClaims)

	noExpHeaderB64 := base64.RawURLEncoding.EncodeToString([]byte(header))
	noExpClaimsB64 := base64.RawURLEncoding.EncodeToString(noExpClaimsJSON)
	noExpSignature := base64.RawURLEncoding.EncodeToString([]byte("signature"))
	noExpToken := noExpHeaderB64 + "." + noExpClaimsB64 + "." + noExpSignature

	// Token with exp as float64.
	floatExp := float64(time.Now().Add(1 * time.Hour).Unix())
	floatClaims := map[string]interface{}{
		"exp": floatExp,
	}
	floatClaimsJSON, _ := json.Marshal(floatClaims)
	floatHeaderB64 := base64.RawURLEncoding.EncodeToString([]byte(header))
	floatClaimsB64 := base64.RawURLEncoding.EncodeToString(floatClaimsJSON)
	floatSignature := base64.RawURLEncoding.EncodeToString([]byte("signature"))
	floatToken := floatHeaderB64 + "." + floatClaimsB64 + "." + floatSignature

	// Token with exp as int64 embedded in claims.
	intExp := time.Now().Add(1 * time.Hour).Unix()
	intClaims := map[string]interface{}{
		"exp": intExp,
	}
	intClaimsJSON, _ := json.Marshal(intClaims)
	intHeaderB64 := base64.RawURLEncoding.EncodeToString([]byte(header))
	intClaimsB64 := base64.RawURLEncoding.EncodeToString(intClaimsJSON)
	intSignature := base64.RawURLEncoding.EncodeToString([]byte("signature"))
	intToken := intHeaderB64 + "." + intClaimsB64 + "." + intSignature

	timePtr := func(t time.Time) *time.Time {
		return &t
	}

	tests := []struct {
		name       string
		token      string
		wantZero   bool
		wantApprox *time.Time
	}{
		{
			name:       "valid token helper",
			token:      validToken,
			wantZero:   false,
			wantApprox: timePtr(time.Unix(validExp, 0)),
		},
		{
			name:     "invalid - empty string",
			token:    "",
			wantZero: true,
		},
		{
			name:     "invalid - single part",
			token:    "header",
			wantZero: true,
		},
		{
			name:     "invalid - two parts",
			token:    "header.payload",
			wantZero: true,
		},
		{
			name:     "invalid - four parts",
			token:    "header.payload.signature.extra",
			wantZero: true,
		},
		{
			name:     "invalid - bad base64",
			token:    "header.payload.invalid!",
			wantZero: true,
		},
		{
			name:     "no exp claim",
			token:    noExpToken,
			wantZero: true,
		},
		{
			name:       "exp as float64",
			token:      floatToken,
			wantZero:   false,
			wantApprox: timePtr(time.Unix(int64(floatExp), 0)),
		},
		{
			name:       "exp as int64",
			token:      intToken,
			wantZero:   false,
			wantApprox: timePtr(time.Unix(intExp, 0)),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expiry := parseJWTExpiry(tt.token)
			if tt.wantZero {
				assert.True(t, expiry.IsZero(), "Expected zero time for case %q", tt.name)
				return
			}

			assert.False(t, expiry.IsZero(), "Expected non-zero time for case %q", tt.name)
			if tt.wantApprox != nil {
				assert.WithinDuration(t, *tt.wantApprox, expiry, 1*time.Second)
			}
		})
	}
}

// Note: TestNewClientCache removed - it only verified that struct fields
// match constructor parameters/defaults, which is trivial initialization behavior.

func TestClientCache_SetAndGet(t *testing.T) {
	cache := NewClientCache(10)

	// Test Get on non-existent key
	client := cache.Get("nonexistent-key")
	assert.Nil(t, client)

	// Test Set and Get
	key := testCacheKey
	token := createTestJWT(time.Now().Add(1 * time.Hour).Unix())
	scheme := runtime.NewScheme()
	dynamicClient := dynamicfake.NewSimpleDynamicClient(scheme)
	newClient := &UserK8sClient{
		dynamicClient: dynamicClient,
		namespace:     "default",
	}

	cache.Set(key, token, newClient)

	retrieved := cache.Get(key)
	assert.NotNil(t, retrieved)
	assert.Equal(t, newClient, retrieved)
	assert.Equal(t, 1, cache.Size())
}

func TestClientCache_Get_ExpiredToken(t *testing.T) {
	cache := NewClientCache(10)

	key := testCacheKey
	// Token expired 1 hour ago
	token := createTestJWT(time.Now().Add(-1 * time.Hour).Unix())
	scheme := runtime.NewScheme()
	dynamicClient := dynamicfake.NewSimpleDynamicClient(scheme)
	client := &UserK8sClient{
		dynamicClient: dynamicClient,
		namespace:     "default",
	}

	cache.Set(key, token, client)

	// Should return nil because token is expired
	retrieved := cache.Get(key)
	assert.Nil(t, retrieved)
	assert.Equal(t, 0, cache.Size(), "Expired entry should be removed")
}

func TestClientCache_Get_TokenWithoutExpiry(t *testing.T) {
	cache := NewClientCache(10)

	key := testCacheKey
	// Token without exp claim (invalid JWT)
	//nolint:gosec // G101: This is a test token, not a real credential
	token := "invalid.jwt.token"
	scheme := runtime.NewScheme()
	dynamicClient := dynamicfake.NewSimpleDynamicClient(scheme)
	client := &UserK8sClient{
		dynamicClient: dynamicClient,
		namespace:     "default",
	}

	cache.Set(key, token, client)

	// Should return client because tokenExpiry is zero (no expiry check)
	retrieved := cache.Get(key)
	assert.NotNil(t, retrieved)
	assert.Equal(t, client, retrieved)
}

func TestClientCache_UpdateExisting(t *testing.T) {
	cache := NewClientCache(10)

	key := testCacheKey
	token1 := createTestJWT(time.Now().Add(1 * time.Hour).Unix())
	token2 := createTestJWT(time.Now().Add(2 * time.Hour).Unix())
	scheme := runtime.NewScheme()
	dynamicClient1 := dynamicfake.NewSimpleDynamicClient(scheme)
	dynamicClient2 := dynamicfake.NewSimpleDynamicClient(scheme)
	client1 := &UserK8sClient{
		dynamicClient: dynamicClient1,
		namespace:     "default",
	}
	client2 := &UserK8sClient{
		dynamicClient: dynamicClient2,
		namespace:     "default",
	}

	cache.Set(key, token1, client1)
	assert.Equal(t, 1, cache.Size())

	cache.Set(key, token2, client2)
	assert.Equal(t, 1, cache.Size(), "Size should not increase when updating")

	retrieved := cache.Get(key)
	assert.Equal(t, client2, retrieved, "Should return updated client")
}

func TestClientCache_Eviction(t *testing.T) {
	cache := NewClientCache(3) // Small cache to test eviction

	scheme := runtime.NewScheme()
	dynamicClient := dynamicfake.NewSimpleDynamicClient(scheme)

	// Fill cache to max size
	for i := 0; i < 3; i++ {
		key := "default:sa" + string(rune('0'+i))
		token := createTestJWT(time.Now().Add(1 * time.Hour).Unix())
		client := &UserK8sClient{
			dynamicClient: dynamicClient,
			namespace:     "default",
		}
		cache.Set(key, token, client)
	}

	assert.Equal(t, 3, cache.Size())

	// Add one more - should evict oldest
	newKey := "default:sa3"
	newToken := createTestJWT(time.Now().Add(1 * time.Hour).Unix())
	newClient := &UserK8sClient{
		dynamicClient: dynamicClient,
		namespace:     "default",
	}
	cache.Set(newKey, newToken, newClient)

	assert.Equal(t, 3, cache.Size(), "Size should remain at max")

	// First key should be evicted
	assert.Nil(t, cache.Get("default:sa0"))
	// New key should be present
	assert.NotNil(t, cache.Get(newKey))
}

func TestClientCache_LRUBehavior(t *testing.T) {
	cache := NewClientCache(3)

	scheme := runtime.NewScheme()
	dynamicClient := dynamicfake.NewSimpleDynamicClient(scheme)
	token := createTestJWT(time.Now().Add(1 * time.Hour).Unix())

	// Add three entries
	for i := 0; i < 3; i++ {
		key := "default:sa" + string(rune('0'+i))
		client := &UserK8sClient{
			dynamicClient: dynamicClient,
			namespace:     "default",
		}
		cache.Set(key, token, client)
	}

	// Access first entry (should move to front)
	cache.Get("default:sa0")

	// Add new entry - should evict sa1 (least recently used)
	newKey := "default:sa3"
	newClient := &UserK8sClient{
		dynamicClient: dynamicClient,
		namespace:     "default",
	}
	cache.Set(newKey, token, newClient)

	// sa0 should still be present (was accessed)
	assert.NotNil(t, cache.Get("default:sa0"))
	// sa1 should be evicted
	assert.Nil(t, cache.Get("default:sa1"))
	// sa2 should be present
	assert.NotNil(t, cache.Get("default:sa2"))
	// sa3 should be present
	assert.NotNil(t, cache.Get(newKey))
}

func TestClientCache_Remove(t *testing.T) {
	tests := []struct {
		name        string
		key         string
		prepopulate bool
	}{
		{
			name:        "remove existing key",
			key:         testCacheKey,
			prepopulate: true,
		},
		{
			name:        "remove non-existent key",
			key:         "nonexistent",
			prepopulate: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cache := NewClientCache(10)

			if tt.prepopulate {
				token := createTestJWT(time.Now().Add(1 * time.Hour).Unix())
				scheme := runtime.NewScheme()
				dynamicClient := dynamicfake.NewSimpleDynamicClient(scheme)
				client := &UserK8sClient{
					dynamicClient: dynamicClient,
					namespace:     "default",
				}

				cache.Set(tt.key, token, client)
				assert.Equal(t, 1, cache.Size())
			}

			cache.Remove(tt.key)
			assert.Equal(t, 0, cache.Size())
			assert.Nil(t, cache.Get(tt.key))
		})
	}
}

// Note: TestClientCache_Size removed - it only verified that Size() returns
// the count of entries, which is trivial getter behavior.

// Note: TestMakeCacheKey removed - it only tests string concatenation
// (namespace + ":" + saName), which is trivial and doesn't test meaningful behavior.

// Note: TestNewTokenCache removed - it only verified that struct fields
// match constructor parameters/defaults, which is trivial initialization behavior.

func TestTokenCache_Get_NotFound(t *testing.T) {
	cache := NewTokenCache(10, 5*time.Minute)

	found, authenticated, username := cache.Get("nonexistent-token")
	assert.False(t, found)
	assert.False(t, authenticated)
	assert.Empty(t, username)
}

func TestTokenCache_SetAndGet(t *testing.T) {
	cache := NewTokenCache(10, 5*time.Minute)

	token := "test-token-123"
	username := testServiceAccount

	cache.Set(token, true, username)

	found, authenticated, retrievedUsername := cache.Get(token)
	assert.True(t, found)
	assert.True(t, authenticated)
	assert.Equal(t, username, retrievedUsername)
	assert.Equal(t, 1, cache.Size())
}

func TestTokenCache_Get_Expired(t *testing.T) {
	cache := NewTokenCache(10, 50*time.Millisecond) // Very short TTL

	token := testToken
	username := testServiceAccount

	cache.Set(token, true, username)

	// Should be found immediately
	found, authenticated, _ := cache.Get(token)
	assert.True(t, found)
	assert.True(t, authenticated)

	// Wait for expiration
	time.Sleep(100 * time.Millisecond)

	// Should not be found after expiration
	found, authenticated, _ = cache.Get(token)
	assert.False(t, found)
	assert.False(t, authenticated)
}

func TestTokenCache_UpdateExisting(t *testing.T) {
	cache := NewTokenCache(10, 5*time.Minute)

	token := testToken
	username1 := "system:serviceaccount:default:sa1"
	username2 := "system:serviceaccount:default:sa2"

	cache.Set(token, true, username1)
	assert.Equal(t, 1, cache.Size())

	cache.Set(token, false, username2)
	assert.Equal(t, 1, cache.Size(), "Size should not increase when updating")

	found, authenticated, username := cache.Get(token)
	assert.True(t, found)
	assert.False(t, authenticated, "Should be updated to false")
	assert.Equal(t, username2, username, "Should return updated username")
}

func TestTokenCache_Eviction(t *testing.T) {
	cache := NewTokenCache(3, 5*time.Minute) // Small cache

	// Fill cache to max size
	for i := 0; i < 3; i++ {
		token := "token" + string(rune('0'+i))
		cache.Set(token, true, "user"+string(rune('0'+i)))
	}

	assert.Equal(t, 3, cache.Size())

	// Add one more - should evict oldest
	cache.Set("token3", true, "user3")

	assert.Equal(t, 3, cache.Size(), "Size should remain at max")

	// First token should be evicted
	found, _, _ := cache.Get("token0")
	assert.False(t, found)
	// New token should be present
	found, _, _ = cache.Get("token3")
	assert.True(t, found)
}

func TestTokenCache_LRUBehavior(t *testing.T) {
	cache := NewTokenCache(3, 5*time.Minute)

	// Add three entries
	for i := 0; i < 3; i++ {
		token := "token" + string(rune('0'+i))
		cache.Set(token, true, "user"+string(rune('0'+i)))
	}

	// Access first entry (Get doesn't update LRU, only Set does)
	cache.Get("token0")

	// Add new entry - should evict oldest (token0, since Get doesn't update LRU)
	cache.Set("token3", true, "user3")

	// token0 should be evicted (oldest in LRU list)
	found, _, _ := cache.Get("token0")
	assert.False(t, found)
	// token1 should be present
	found, _, _ = cache.Get("token1")
	assert.True(t, found)
	// token2 should be present
	found, _, _ = cache.Get("token2")
	assert.True(t, found)
	// token3 should be present
	found, _, _ = cache.Get("token3")
	assert.True(t, found)
}

func TestTokenCache_Remove(t *testing.T) {
	tests := []struct {
		name        string
		token       string
		prepopulate bool
	}{
		{
			name:        "remove existing token",
			token:       "test-token",
			prepopulate: true,
		},
		{
			name:        "remove non-existent token",
			token:       "nonexistent",
			prepopulate: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cache := NewTokenCache(10, 5*time.Minute)

			if tt.prepopulate {
				cache.Set(tt.token, true, testServiceAccount)
				assert.Equal(t, 1, cache.Size())
			}

			cache.Remove(tt.token)
			assert.Equal(t, 0, cache.Size())

			found, _, _ := cache.Get(tt.token)
			assert.False(t, found)
		})
	}
}

// Note: TestTokenCache_Size removed - it only verified that Size() returns
// the count of entries, which is trivial getter behavior.

func TestTokenCache_NotAuthenticated(t *testing.T) {
	cache := NewTokenCache(10, 5*time.Minute)

	token := "invalid-token"
	cache.Set(token, false, "")

	found, authenticated, username := cache.Get(token)
	assert.True(t, found)
	assert.False(t, authenticated)
	assert.Empty(t, username)
}

func TestClientCache_ConcurrentAccess(t *testing.T) {
	cache := NewClientCache(100)
	scheme := runtime.NewScheme()
	dynamicClient := dynamicfake.NewSimpleDynamicClient(scheme)
	token := createTestJWT(time.Now().Add(1 * time.Hour).Unix())

	// Concurrent writes
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func(idx int) {
			key := "default:sa" + string(rune('0'+idx))
			client := &UserK8sClient{
				dynamicClient: dynamicClient,
				namespace:     "default",
			}
			cache.Set(key, token, client)
			done <- true
		}(i)
	}

	// Wait for all writes
	for i := 0; i < 10; i++ {
		<-done
	}

	// Verify all entries are present
	assert.Equal(t, 10, cache.Size())
}

func TestTokenCache_ConcurrentAccess(t *testing.T) {
	cache := NewTokenCache(100, 5*time.Minute)

	// Concurrent writes
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func(idx int) {
			token := "token" + string(rune('0'+idx))
			cache.Set(token, true, "user"+string(rune('0'+idx)))
			done <- true
		}(i)
	}

	// Wait for all writes
	for i := 0; i < 10; i++ {
		<-done
	}

	// Verify all entries are present
	assert.Equal(t, 10, cache.Size())
}
