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
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"testing"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateSessionKeyPair(t *testing.T) {
	privPEM, pubPEM, err := GenerateSessionKeyPair()
	require.NoError(t, err)
	assert.NotEmpty(t, privPEM)
	assert.NotEmpty(t, pubPEM)

	// Verify private key parses
	blockPriv, _ := pem.Decode([]byte(privPEM))
	require.NotNil(t, blockPriv)
	assert.Equal(t, "EC PRIVATE KEY", blockPriv.Type)

	privKey, err := x509.ParseECPrivateKey(blockPriv.Bytes)
	require.NoError(t, err)
	assert.Equal(t, "P-256", privKey.Curve.Params().Name)

	// Verify public key parses
	blockPub, _ := pem.Decode([]byte(pubPEM))
	require.NotNil(t, blockPub)
	assert.Equal(t, "PUBLIC KEY", blockPub.Type)

	pubKey, err := x509.ParsePKIXPublicKey(blockPub.Bytes)
	require.NoError(t, err)
	_, ok := pubKey.(*ecdsa.PublicKey)
	assert.True(t, ok)
}

func TestGenerateInitJWT(t *testing.T) {
	m := newTestBootstrapAuth(t)

	sandboxID := "test-session-id"
	sessionPublicKey := "test-session-public-key"

	tokenStr, err := m.GenerateInitJWT(sandboxID, sessionPublicKey)
	require.NoError(t, err)
	assert.NotEmpty(t, tokenStr)

	// Verify the JWT using the generated Bootstrap Public Key
	bootstrapPubPEM := m.PublicKeyPEM()
	block, _ := pem.Decode([]byte(bootstrapPubPEM))
	require.NotNil(t, block)

	pubKeyInterface, err := x509.ParsePKIXPublicKey(block.Bytes)
	require.NoError(t, err)
	bootstrapPubKey, ok := pubKeyInterface.(*rsa.PublicKey)
	require.True(t, ok)

	token, err := jwt.Parse(tokenStr, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, assert.AnError
		}
		return bootstrapPubKey, nil
	})
	require.NoError(t, err)
	assert.True(t, token.Valid)

	claims, ok := token.Claims.(jwt.MapClaims)
	assert.True(t, ok)
	assert.Equal(t, sandboxID, claims["sub"])
	assert.Equal(t, sessionPublicKey, claims["session_public_key"])
}
