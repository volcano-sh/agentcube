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

func TestParseJWTExpiry_ValidToken(t *testing.T) {
	exp := time.Now().Add(1 * time.Hour).Unix()
	token := createTestJWT(exp)

	expiry := parseJWTExpiry(token)

	assert.False(t, expiry.IsZero())
	assert.WithinDuration(t, time.Unix(exp, 0), expiry, 1*time.Second)
}

func TestParseJWTExpiry_InvalidFormat(t *testing.T) {
	tests := []struct {
		name  string
		token string
	}{
		{
			name:  "empty string",
			token: "",
		},
		{
			name:  "single part",
			token: "header",
		},
		{
			name:  "two parts",
			token: "header.payload",
		},
		{
			name:  "four parts",
			token: "header.payload.signature.extra",
		},
		{
			name:  "invalid base64",
			token: "header.payload.invalid!",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expiry := parseJWTExpiry(tt.token)
			assert.True(t, expiry.IsZero(), "Invalid token should return zero time")
		})
	}
}

func TestParseJWTExpiry_NoExpClaim(t *testing.T) {
	header := jwtHeader
	claims := map[string]interface{}{
		"iat": time.Now().Unix(),
		"sub": "test-user",
	}
	claimsJSON, _ := json.Marshal(claims)

	headerB64 := base64.RawURLEncoding.EncodeToString([]byte(header))
	claimsB64 := base64.RawURLEncoding.EncodeToString(claimsJSON)
	signature := base64.RawURLEncoding.EncodeToString([]byte("signature"))

	token := headerB64 + "." + claimsB64 + "." + signature

	expiry := parseJWTExpiry(token)
	assert.True(t, expiry.IsZero(), "Token without exp claim should return zero time")
}

func TestParseJWTExpiry_ExpAsFloat64(t *testing.T) {
	exp := float64(time.Now().Add(1 * time.Hour).Unix())
	header := jwtHeader
	claims := map[string]interface{}{
		"exp": exp,
	}
	claimsJSON, _ := json.Marshal(claims)

	headerB64 := base64.RawURLEncoding.EncodeToString([]byte(header))
	claimsB64 := base64.RawURLEncoding.EncodeToString(claimsJSON)
	signature := base64.RawURLEncoding.EncodeToString([]byte("signature"))

	token := headerB64 + "." + claimsB64 + "." + signature

	expiry := parseJWTExpiry(token)
	assert.False(t, expiry.IsZero())
	assert.WithinDuration(t, time.Unix(int64(exp), 0), expiry, 1*time.Second)
}

func TestParseJWTExpiry_ExpAsInt64(t *testing.T) {
	exp := time.Now().Add(1 * time.Hour).Unix()
	header := jwtHeader
	claims := map[string]interface{}{
		"exp": exp,
	}
	claimsJSON, _ := json.Marshal(claims)

	headerB64 := base64.RawURLEncoding.EncodeToString([]byte(header))
	claimsB64 := base64.RawURLEncoding.EncodeToString(claimsJSON)
	signature := base64.RawURLEncoding.EncodeToString([]byte("signature"))

	token := headerB64 + "." + claimsB64 + "." + signature

	expiry := parseJWTExpiry(token)
	assert.False(t, expiry.IsZero())
	assert.WithinDuration(t, time.Unix(exp, 0), expiry, 1*time.Second)
}

func TestNewClientCache(t *testing.T) {
	tests := []struct {
		name    string
		maxSize int
		want    int
	}{
		{
			name:    "positive max size",
			maxSize: 50,
			want:    50,
		},
		{
			name:    "zero max size defaults to 100",
			maxSize: 0,
			want:    100,
		},
		{
			name:    "negative max size defaults to 100",
			maxSize: -10,
			want:    100,
		},
		{
			name:    "large max size",
			maxSize: 1000,
			want:    1000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cache := NewClientCache(tt.maxSize)
			assert.NotNil(t, cache)
			assert.Equal(t, tt.want, cache.maxSize)
			assert.Equal(t, 0, cache.Size())
		})
	}
}

func TestClientCache_Get_NotFound(t *testing.T) {
	cache := NewClientCache(10)

	client := cache.Get("nonexistent-key")
	assert.Nil(t, client)
}

func TestClientCache_SetAndGet(t *testing.T) {
	cache := NewClientCache(10)

	key := testCacheKey
	token := createTestJWT(time.Now().Add(1 * time.Hour).Unix())
	scheme := runtime.NewScheme()
	dynamicClient := dynamicfake.NewSimpleDynamicClient(scheme)
	client := &UserK8sClient{
		dynamicClient: dynamicClient,
		namespace:     "default",
	}

	cache.Set(key, token, client)

	retrieved := cache.Get(key)
	assert.NotNil(t, retrieved)
	assert.Equal(t, client, retrieved)
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
	cache := NewClientCache(10)

	key := testCacheKey
	token := createTestJWT(time.Now().Add(1 * time.Hour).Unix())
	scheme := runtime.NewScheme()
	dynamicClient := dynamicfake.NewSimpleDynamicClient(scheme)
	client := &UserK8sClient{
		dynamicClient: dynamicClient,
		namespace:     "default",
	}

	cache.Set(key, token, client)
	assert.Equal(t, 1, cache.Size())

	cache.Remove(key)
	assert.Equal(t, 0, cache.Size())
	assert.Nil(t, cache.Get(key))
}

func TestClientCache_Remove_Nonexistent(t *testing.T) {
	cache := NewClientCache(10)

	// Removing non-existent key should not panic
	cache.Remove("nonexistent")
	assert.Equal(t, 0, cache.Size())
}

func TestClientCache_Size(t *testing.T) {
	cache := NewClientCache(10)

	assert.Equal(t, 0, cache.Size())

	scheme := runtime.NewScheme()
	dynamicClient := dynamicfake.NewSimpleDynamicClient(scheme)
	token := createTestJWT(time.Now().Add(1 * time.Hour).Unix())

	for i := 0; i < 5; i++ {
		key := "default:sa" + string(rune('0'+i))
		client := &UserK8sClient{
			dynamicClient: dynamicClient,
			namespace:     "default",
		}
		cache.Set(key, token, client)
		assert.Equal(t, i+1, cache.Size())
	}
}

func TestMakeCacheKey(t *testing.T) {
	tests := []struct {
		name      string
		namespace string
		saName    string
		want      string
	}{
		{
			name:      "normal values",
			namespace: "default",
			saName:    "test-sa",
			want:      testCacheKey,
		},
		{
			name:      "empty namespace",
			namespace: "",
			saName:    "test-sa",
			want:      ":test-sa",
		},
		{
			name:      "empty sa name",
			namespace: "default",
			saName:    "",
			want:      "default:",
		},
		{
			name:      "both empty",
			namespace: "",
			saName:    "",
			want:      ":",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := makeCacheKey(tt.namespace, tt.saName)
			assert.Equal(t, tt.want, result)
		})
	}
}

func TestNewTokenCache(t *testing.T) {
	tests := []struct {
		name    string
		maxSize int
		ttl     time.Duration
		wantMax int
		wantTTL time.Duration
	}{
		{
			name:    "valid values",
			maxSize: 500,
			ttl:     10 * time.Minute,
			wantMax: 500,
			wantTTL: 10 * time.Minute,
		},
		{
			name:    "zero max size defaults to 1000",
			maxSize: 0,
			ttl:     5 * time.Minute,
			wantMax: 1000,
			wantTTL: 5 * time.Minute,
		},
		{
			name:    "negative max size defaults to 1000",
			maxSize: -10,
			ttl:     5 * time.Minute,
			wantMax: 1000,
			wantTTL: 5 * time.Minute,
		},
		{
			name:    "zero TTL defaults to 5 minutes",
			maxSize: 500,
			ttl:     0,
			wantMax: 500,
			wantTTL: 5 * time.Minute,
		},
		{
			name:    "negative TTL defaults to 5 minutes",
			maxSize: 500,
			ttl:     -1 * time.Minute,
			wantMax: 500,
			wantTTL: 5 * time.Minute,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cache := NewTokenCache(tt.maxSize, tt.ttl)
			assert.NotNil(t, cache)
			assert.Equal(t, tt.wantMax, cache.maxSize)
			assert.Equal(t, tt.wantTTL, cache.ttl)
			assert.Equal(t, 0, cache.Size())
		})
	}
}

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
	username := "system:serviceaccount:default:test-sa"

	cache.Set(token, true, username)

	found, authenticated, retrievedUsername := cache.Get(token)
	assert.True(t, found)
	assert.True(t, authenticated)
	assert.Equal(t, username, retrievedUsername)
	assert.Equal(t, 1, cache.Size())
}

func TestTokenCache_Get_Expired(t *testing.T) {
	cache := NewTokenCache(10, 1*time.Second) // Very short TTL

	token := "test-token"
	username := "system:serviceaccount:default:test-sa"

	cache.Set(token, true, username)

	// Should be found immediately
	found, authenticated, _ := cache.Get(token)
	assert.True(t, found)
	assert.True(t, authenticated)

	// Wait for expiration
	time.Sleep(2 * time.Second)

	// Should not be found after expiration
	found, authenticated, _ = cache.Get(token)
	assert.False(t, found)
	assert.False(t, authenticated)
}

func TestTokenCache_UpdateExisting(t *testing.T) {
	cache := NewTokenCache(10, 5*time.Minute)

	token := "test-token"
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
	cache := NewTokenCache(10, 5*time.Minute)

	token := "test-token"
	username := "system:serviceaccount:default:test-sa"

	cache.Set(token, true, username)
	assert.Equal(t, 1, cache.Size())

	cache.Remove(token)
	assert.Equal(t, 0, cache.Size())

	found, _, _ := cache.Get(token)
	assert.False(t, found)
}

func TestTokenCache_Remove_Nonexistent(t *testing.T) {
	cache := NewTokenCache(10, 5*time.Minute)

	// Removing non-existent token should not panic
	cache.Remove("nonexistent")
	assert.Equal(t, 0, cache.Size())
}

func TestTokenCache_Size(t *testing.T) {
	cache := NewTokenCache(10, 5*time.Minute)

	assert.Equal(t, 0, cache.Size())

	for i := 0; i < 5; i++ {
		token := "token" + string(rune('0'+i))
		cache.Set(token, true, "user"+string(rune('0'+i)))
		assert.Equal(t, i+1, cache.Size())
	}
}

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
