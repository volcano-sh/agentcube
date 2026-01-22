/*
Copyright The Volcano Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
you may obtain a copy of the License at

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
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestClientCache(t *testing.T) {
	cache := NewClientCache(2)
	assert.Equal(t, 0, cache.Size())

	client1 := &UserK8sClient{namespace: "ns1"}
	client2 := &UserK8sClient{namespace: "ns2"}
	client3 := &UserK8sClient{namespace: "ns3"}

	// Test Set and Get
	cache.Set("key1", "token1", client1)
	assert.Equal(t, client1, cache.Get("key1"))
	assert.Equal(t, 1, cache.Size())

	// Test LRU Eviction
	cache.Set("key2", "token2", client2)
	assert.Equal(t, 2, cache.Size())
	
	cache.Set("key3", "token3", client3)
	assert.Equal(t, 2, cache.Size())
	assert.Nil(t, cache.Get("key1")) // key1 should be evicted
	assert.Equal(t, client2, cache.Get("key2"))
	assert.Equal(t, client3, cache.Get("key3"))

	// Test Remove
	cache.Remove("key2")
	assert.Nil(t, cache.Get("key2"))
	assert.Equal(t, 1, cache.Size())
}

func TestParseJWTExpiry(t *testing.T) {
	// 1. Valid JWT with exp
	now := time.Now().Unix()
	payload := fmt.Sprintf(`{"exp": %d}`, now+100)
	payloadB64 := base64.RawURLEncoding.EncodeToString([]byte(payload))
	token := "header." + payloadB64 + ".signature"
	
	expiry := parseJWTExpiry(token)
	assert.Equal(t, now+100, expiry.Unix())

	// 2. Invalid format
	assert.True(t, parseJWTExpiry("invalid").IsZero())

	// 3. No exp claim
	payloadNoExp := `{"user": "test"}`
	payloadNoExpB64 := base64.RawURLEncoding.EncodeToString([]byte(payloadNoExp))
	tokenNoExp := "header." + payloadNoExpB64 + ".signature"
	assert.True(t, parseJWTExpiry(tokenNoExp).IsZero())
}

func TestClientCache_TokenExpiration(t *testing.T) {
	cache := NewClientCache(10)
	
	// Create token that expires in the past
	past := time.Now().Add(-1 * time.Hour).Unix()
	payload := fmt.Sprintf(`{"exp": %d}`, past)
	payloadB64 := base64.RawURLEncoding.EncodeToString([]byte(payload))
	token := "header." + payloadB64 + ".signature"
	
	client := &UserK8sClient{namespace: "ns"}
	cache.Set("key", token, client)
	
	// Get should return nil and evict the entry
	assert.Nil(t, cache.Get("key"))
	assert.Equal(t, 0, cache.Size())
}

func TestTokenCache_LRU(t *testing.T) {
	tc := NewTokenCache(2, time.Hour)
	
	tc.Set("t1", true, "u1")
	tc.Set("t2", true, "u2")
	assert.Equal(t, 2, tc.Size())
	
	tc.Set("t3", true, "u3")
	assert.Equal(t, 2, tc.Size())
	
	found, _, _ := tc.Get("t1")
	assert.False(t, found)
}
