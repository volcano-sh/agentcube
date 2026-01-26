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
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
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
				// Check standard claims are present
				assert.NotZero(t, claims["exp"])
				assert.NotZero(t, claims["iat"])

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

	// Verify keys match
	assert.Equal(t, originalManager.privateKey, newManager.privateKey)
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

// Note: we intentionally avoid tests that only verify getters return the same
// value multiple times, per maintainer feedback. The remaining tests focus on
// meaningful behavior like token generation, validation, and error handling.
