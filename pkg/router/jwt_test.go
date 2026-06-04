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

package router

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"testing"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
)

func TestNewJWTManager(t *testing.T) {
	manager, err := NewJWTManager()
	assert.NoError(t, err)
	assert.NotNil(t, manager)
	assert.NotNil(t, manager.privateKey)
	assert.NotNil(t, manager.publicKey)
}

func TestGenerateToken(t *testing.T) {
	manager, err := NewJWTManager()
	assert.NoError(t, err)

	tests := []struct {
		name   string
		claims map[string]interface{}
	}{
		{
			name:   "empty claims",
			claims: map[string]interface{}{},
		},
		{
			name: "single claim",
			claims: map[string]interface{}{
				"session_id": "test-session-123",
			},
		},
		{
			name: "multiple claims",
			claims: map[string]interface{}{
				"session_id": "test-session-123",
				"user_id":    "user-456",
				"role":       "admin",
			},
		},
		{
			name: "claims with different types",
			claims: map[string]interface{}{
				"session_id": "test-session-123",
				"count":      42,
				"active":     true,
				"tags":       []string{"test", "sandbox"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokenString, err := manager.GenerateToken(tt.claims)
			assert.NoError(t, err)
			assert.NotEmpty(t, tokenString)

			// Verify token can be parsed and validated
			token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
				if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
					return nil, jwt.ErrSignatureInvalid
				}
				return manager.publicKey, nil
			})

			assert.NoError(t, err)
			assert.True(t, token.Valid)

			// Verify claims
			if claims, ok := token.Claims.(jwt.MapClaims); ok {
				// Check standard claims
				assert.NotZero(t, claims["exp"])
				assert.NotZero(t, claims["iat"])
				assert.Equal(t, "agentcube-router", claims["iss"])

				// Check custom claims
				for k, v := range tt.claims {
					claimValue := claims[k]
					// JWT library converts numbers to float64 and arrays to []interface{}
					switch expected := v.(type) {
					case int:
						// Convert int to float64 for comparison
						actualFloat, ok := claimValue.(float64)
						assert.True(t, ok, "claim %s should be float64", k)
						assert.Equal(t, float64(expected), actualFloat)
					case []string:
						// Convert []string to []interface{} for comparison
						actualSlice, ok := claimValue.([]interface{})
						assert.True(t, ok, "claim %s should be []interface{}", k)
						assert.Equal(t, len(expected), len(actualSlice))
						for i, expectedVal := range expected {
							assert.Equal(t, expectedVal, actualSlice[i])
						}
					default:
						// For other types (string, bool), direct comparison works
						assert.Equal(t, v, claimValue)
					}
				}
			}
		})
	}
}

func TestGenerateToken_Expiration(t *testing.T) {
	manager, err := NewJWTManager()
	assert.NoError(t, err)

	tokenString, err := manager.GenerateToken(map[string]interface{}{})
	assert.NoError(t, err)

	token, err := jwt.Parse(tokenString, func(_ *jwt.Token) (interface{}, error) {
		return manager.publicKey, nil
	})
	assert.NoError(t, err)

	if claims, ok := token.Claims.(jwt.MapClaims); ok {
		exp, ok := claims["exp"].(float64)
		assert.True(t, ok, "exp claim should be float64")
		iat, ok := claims["iat"].(float64)
		assert.True(t, ok, "iat claim should be float64")
		expectedExp := int64(iat) + int64(jwtExpiration.Seconds())
		assert.Equal(t, expectedExp, int64(exp))
	}
}

func TestGetPublicKeyPEM(t *testing.T) {
	manager, err := NewJWTManager()
	assert.NoError(t, err)

	pemBytes, err := manager.GetPublicKeyPEM()
	assert.NoError(t, err)
	assert.NotEmpty(t, pemBytes)

	// Verify PEM format
	block, _ := pem.Decode(pemBytes)
	assert.NotNil(t, block)
	assert.Equal(t, "PUBLIC KEY", block.Type)

	// Verify key can be parsed
	pubKey, err := x509.ParsePKIXPublicKey(block.Bytes)
	assert.NoError(t, err)
	assert.NotNil(t, pubKey)
	assert.IsType(t, &rsa.PublicKey{}, pubKey)
}

func TestGetPrivateKeyPEM(t *testing.T) {
	manager, err := NewJWTManager()
	assert.NoError(t, err)

	pemBytes := manager.GetPrivateKeyPEM()
	assert.NotEmpty(t, pemBytes)

	// Verify PEM format
	block, _ := pem.Decode(pemBytes)
	assert.NotNil(t, block)
	assert.Equal(t, "RSA PRIVATE KEY", block.Type)

	// Verify key can be parsed
	privateKey, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	assert.NoError(t, err)
	assert.NotNil(t, privateKey)
	assert.Equal(t, manager.privateKey, privateKey)
}

func TestLoadPrivateKeyPEM(t *testing.T) {
	// Create a manager and get its private key PEM
	originalManager, err := NewJWTManager()
	assert.NoError(t, err)

	privateKeyPEM := originalManager.GetPrivateKeyPEM()

	// Create a new manager and load the key
	newManager := &JWTManager{}
	err = newManager.loadPrivateKeyPEM(privateKeyPEM)
	assert.NoError(t, err)
	assert.NotNil(t, newManager.privateKey)
	assert.NotNil(t, newManager.publicKey)

	// Verify keys match mathematically (comparing big.Int values, not byte representations)
	originalKey := originalManager.privateKey
	loadedKey := newManager.privateKey

	// Compare public key components (N and E)
	assert.Equal(t, originalKey.PublicKey.N, loadedKey.PublicKey.N, "Public key N should match")
	assert.Equal(t, originalKey.PublicKey.E, loadedKey.PublicKey.E, "Public key E should match")

	// Compare private exponent D
	assert.Equal(t, 0, originalKey.D.Cmp(loadedKey.D), "Private exponent D should match")

	// Compare primes
	assert.Equal(t, len(originalKey.Primes), len(loadedKey.Primes), "Number of primes should match")
	for i := range originalKey.Primes {
		assert.Equal(t, 0, originalKey.Primes[i].Cmp(loadedKey.Primes[i]), "Prime %d should match", i)
	}

	// Compare precomputed values if present
	if originalKey.Precomputed.Dp != nil && loadedKey.Precomputed.Dp != nil {
		assert.Equal(t, 0, originalKey.Precomputed.Dp.Cmp(loadedKey.Precomputed.Dp), "Precomputed Dp should match")
	}
	if originalKey.Precomputed.Dq != nil && loadedKey.Precomputed.Dq != nil {
		assert.Equal(t, 0, originalKey.Precomputed.Dq.Cmp(loadedKey.Precomputed.Dq), "Precomputed Dq should match")
	}
	if originalKey.Precomputed.Qinv != nil && loadedKey.Precomputed.Qinv != nil {
		assert.Equal(t, 0, originalKey.Precomputed.Qinv.Cmp(loadedKey.Precomputed.Qinv), "Precomputed Qinv should match")
	}

	// Verify the keys are functionally equivalent by testing sign/verify
	testData := []byte("test data for key verification")
	hash := crypto.SHA256.New()
	hash.Write(testData)
	hashed := hash.Sum(nil)

	// Sign with original key
	signature1, err := rsa.SignPKCS1v15(rand.Reader, originalKey, crypto.SHA256, hashed)
	assert.NoError(t, err)

	// Verify with loaded key's public key
	err = rsa.VerifyPKCS1v15(&loadedKey.PublicKey, crypto.SHA256, hashed, signature1)
	assert.NoError(t, err, "Signature from original key should verify with loaded key's public key")

	// Sign with loaded key
	signature2, err := rsa.SignPKCS1v15(rand.Reader, loadedKey, crypto.SHA256, hashed)
	assert.NoError(t, err)

	// Verify with original key's public key
	err = rsa.VerifyPKCS1v15(&originalKey.PublicKey, crypto.SHA256, hashed, signature2)
	assert.NoError(t, err, "Signature from loaded key should verify with original key's public key")
}

func TestLoadPrivateKeyPEM_InvalidPEM(t *testing.T) {
	manager := &JWTManager{}

	tests := []struct {
		name    string
		pemData []byte
	}{
		{
			name:    "empty data",
			pemData: []byte{},
		},
		{
			name:    "invalid PEM format",
			pemData: []byte("not a valid PEM block"),
		},
		{
			name:    "wrong PEM type",
			pemData: []byte("-----BEGIN CERTIFICATE-----\ninvalid\n-----END CERTIFICATE-----"),
		},
		{
			name:    "corrupted PEM data",
			pemData: []byte("-----BEGIN RSA PRIVATE KEY-----\ncorrupted\n-----END RSA PRIVATE KEY-----"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := manager.loadPrivateKeyPEM(tt.pemData)
			assert.Error(t, err)
		})
	}
}

func TestJWTManager_KeyConsistency(t *testing.T) {
	manager, err := NewJWTManager()
	assert.NoError(t, err)

	// Generate multiple tokens and verify they can all be validated with the same public key
	tokens := make([]string, 10)
	for i := 0; i < 10; i++ {
		token, err := manager.GenerateToken(map[string]interface{}{
			"index": i,
		})
		assert.NoError(t, err)
		tokens[i] = token
	}

	// Verify all tokens can be validated
	for i, tokenString := range tokens {
		token, err := jwt.Parse(tokenString, func(_ *jwt.Token) (interface{}, error) {
			return manager.publicKey, nil
		})
		assert.NoError(t, err, "Token %d should be valid", i)
		assert.True(t, token.Valid, "Token %d should be valid", i)

		if claims, ok := token.Claims.(jwt.MapClaims); ok {
			assert.Equal(t, float64(i), claims["index"])
		}
	}
}

func TestGenerateToken_DifferentExpirationTimes(t *testing.T) {
	manager, err := NewJWTManager()
	assert.NoError(t, err)

	// Generate tokens with different claims
	token1, err := manager.GenerateToken(map[string]interface{}{
		"session_id": "session-1",
	})
	assert.NoError(t, err)

	token2, err := manager.GenerateToken(map[string]interface{}{
		"session_id": "session-2",
	})
	assert.NoError(t, err)

	// Tokens should be different
	assert.NotEqual(t, token1, token2)

	// But both should be valid
	for i, tokenString := range []string{token1, token2} {
		token, err := jwt.Parse(tokenString, func(_ *jwt.Token) (interface{}, error) {
			return manager.publicKey, nil
		})
		assert.NoError(t, err, "Token %d should be valid", i)
		assert.True(t, token.Valid, "Token %d should be valid", i)
	}
}

func TestGetPublicKeyPEM_Consistency(t *testing.T) {
	manager, err := NewJWTManager()
	assert.NoError(t, err)

	// Get public key PEM multiple times
	pem1, err := manager.GetPublicKeyPEM()
	assert.NoError(t, err)

	pem2, err := manager.GetPublicKeyPEM()
	assert.NoError(t, err)

	// Should be identical
	assert.Equal(t, pem1, pem2)
}

func TestGetPrivateKeyPEM_Consistency(t *testing.T) {
	manager, err := NewJWTManager()
	assert.NoError(t, err)

	// Get private key PEM multiple times
	pem1 := manager.GetPrivateKeyPEM()
	pem2 := manager.GetPrivateKeyPEM()

	// Should be identical
	assert.Equal(t, pem1, pem2)
}

func generateECPrivateKeyPEM(t *testing.T) (string, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate ecdsa key: %v", err)
	}

	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("failed to marshal ecdsa key: %v", err)
	}

	block := &pem.Block{
		Type:  "EC PRIVATE KEY",
		Bytes: der,
	}
	pemBytes := pem.EncodeToMemory(block)
	return string(pemBytes), key
}

func TestJWTManager_LRUCache(t *testing.T) {
	manager, err := NewJWTManager()
	assert.NoError(t, err)

	// 1. Test GenerateTokenWithKey behavior when privateKeyPEM is omitted vs provided
	session1 := "session-1"
	claims := map[string]interface{}{"foo": "bar"}

	// When key is not cached and privateKeyPEM is omitted -> ErrKeyNotCached
	_, err = manager.GenerateTokenWithKey(session1, claims, "")
	assert.ErrorIs(t, err, ErrKeyNotCached)

	// When key is not cached and privateKeyPEM is provided -> success & cached
	pemStr, expectedKey := generateECPrivateKeyPEM(t)
	tokenStr, err := manager.GenerateTokenWithKey(session1, claims, pemStr)
	assert.NoError(t, err)
	assert.NotEmpty(t, tokenStr)

	// Retrieve from cache to verify key is cached and matches
	cachedKey, ok := manager.GetCachedKey(session1)
	assert.True(t, ok)
	assert.Equal(t, expectedKey, cachedKey)

	// When key is cached and privateKeyPEM is omitted -> success
	tokenStr2, err := manager.GenerateTokenWithKey(session1, claims, "")
	assert.NoError(t, err)
	assert.NotEmpty(t, tokenStr2)

	// 2. Test cache hit / move-to-front
	session2 := "session-2"
	pemStr2, _ := generateECPrivateKeyPEM(t)
	_, err = manager.GenerateTokenWithKey(session2, claims, pemStr2)
	assert.NoError(t, err)

	// In keyCache, session2 is the most recently added, so it should be at the front
	front := manager.evictList.Front()
	assert.NotNil(t, front)
	entry, ok := front.Value.(*cacheEntry)
	assert.True(t, ok)
	assert.Equal(t, session2, entry.sessionID)

	// Accessing session1 should move it to the front
	_, ok = manager.GetCachedKey(session1)
	assert.True(t, ok)
	front = manager.evictList.Front()
	assert.NotNil(t, front)
	entry, ok = front.Value.(*cacheEntry)
	assert.True(t, ok)
	assert.Equal(t, session1, entry.sessionID)

	// 3. Test LRU eviction when size exceeds keyCacheMaxSize (1000)
	manager2, err := NewJWTManager()
	assert.NoError(t, err)

	pems := make([]string, 1005)
	for i := 0; i < 1005; i++ {
		pems[i], _ = generateECPrivateKeyPEM(t)
	}

	for i := 0; i < 1005; i++ {
		sessID := fmt.Sprintf("sess-%d", i)
		_, err := manager2.GenerateTokenWithKey(sessID, claims, pems[i])
		assert.NoError(t, err)
	}

	// The first 5 sessions ("sess-0" to "sess-4") should be evicted
	for i := 0; i < 5; i++ {
		sessID := fmt.Sprintf("sess-%d", i)
		_, ok := manager2.GetCachedKey(sessID)
		assert.False(t, ok, "session %s should have been evicted", sessID)
	}

	// The last 1000 sessions ("sess-5" to "sess-1004") should still be cached
	for i := 5; i < 1005; i++ {
		sessID := fmt.Sprintf("sess-%d", i)
		_, ok := manager2.GetCachedKey(sessID)
		assert.True(t, ok, "session %s should still be cached", sessID)
	}
}
