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
	"crypto/x509"
	"encoding/pem"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewJWTManager(t *testing.T) {
	jm, err := NewJWTManager()
	require.NoError(t, err)
	require.NotNil(t, jm)
	assert.NotNil(t, jm.privateKey)
	assert.NotNil(t, jm.publicKey)
	assert.Equal(t, &jm.privateKey.PublicKey, jm.publicKey)
}

func TestGenerateToken(t *testing.T) {
	jm, err := NewJWTManager()
	require.NoError(t, err)

	claims := map[string]interface{}{
		"sub":  "test-subject",
		"role": "admin",
	}

	tokenString, err := jm.GenerateToken(claims)
	require.NoError(t, err)
	assert.NotEmpty(t, tokenString)

	// Verify token
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		// Validate signing method
		if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, jwt.ErrSignatureInvalid
		}
		return jm.publicKey, nil
	})

	require.NoError(t, err)
	assert.True(t, token.Valid)

	// Validate claims
	mapClaims, ok := token.Claims.(jwt.MapClaims)
	assert.True(t, ok)
	assert.Equal(t, "test-subject", mapClaims["sub"])
	assert.Equal(t, "admin", mapClaims["role"])

	// Validate expiration exists
	assert.Contains(t, mapClaims, "exp")
	assert.Contains(t, mapClaims, "iat")
}

func TestGetPublicKeyPEM(t *testing.T) {
	jm, err := NewJWTManager()
	require.NoError(t, err)

	pemBytes, err := jm.GetPublicKeyPEM()
	require.NoError(t, err)
	assert.NotEmpty(t, pemBytes)

	// Parse PEM
	block, _ := pem.Decode(pemBytes)
	require.NotNil(t, block)
	assert.Equal(t, "PUBLIC KEY", block.Type)

	// Parse Key
	pubKey, err := x509.ParsePKIXPublicKey(block.Bytes)
	require.NoError(t, err)
	assert.NotNil(t, pubKey)
}

func TestTokenExpiration(t *testing.T) {
	jm, err := NewJWTManager()
	require.NoError(t, err)

	claims := map[string]interface{}{"foo": "bar"}
	tokenString, err := jm.GenerateToken(claims)
	require.NoError(t, err)

	token, err := jwt.Parse(tokenString, func(_ *jwt.Token) (interface{}, error) {
		return jm.publicKey, nil
	})
	require.NoError(t, err)

	mapClaims, ok := token.Claims.(jwt.MapClaims)
	assert.True(t, ok)

	expFloat, ok := mapClaims["exp"].(float64)
	assert.True(t, ok)
	exp := int64(expFloat)

	iatFloat, ok := mapClaims["iat"].(float64)
	assert.True(t, ok)
	iat := int64(iatFloat)
	// jwtExpiration is 5 minutes
	expectedExp := iat + int64(5*time.Minute/time.Second)

	// Allow 1 second delta for execution time
	assert.InDelta(t, expectedExp, exp, 1)
}

func TestVerifyTokenWithDifferentKey(t *testing.T) {
	jm1, err := NewJWTManager()
	require.NoError(t, err)

	jm2, err := NewJWTManager()
	require.NoError(t, err)

	tokenString, err := jm1.GenerateToken(map[string]interface{}{"foo": "bar"})
	require.NoError(t, err)

	// Try to verify with jm2's public key (should fail)
	token, err := jwt.Parse(tokenString, func(_ *jwt.Token) (interface{}, error) {
		return jm2.publicKey, nil
	})

	assert.Error(t, err)
	if token != nil {
		assert.False(t, token.Valid)
	}
}

func TestJWTManager_GetPublicKeyPEM(t *testing.T) {
	jm, err := NewJWTManager()
	assert.NoError(t, err)

	publicKeyPEM, err := jm.GetPublicKeyPEM()
	assert.NoError(t, err)
	assert.NotEmpty(t, publicKeyPEM)

	privateKeyPEM := jm.GetPrivateKeyPEM()
	assert.NotEmpty(t, privateKeyPEM)

	jm2, err := NewJWTManagerWithPEM(publicKeyPEM, privateKeyPEM)
	assert.NoError(t, err)
	assert.NotNil(t, jm2)
}
