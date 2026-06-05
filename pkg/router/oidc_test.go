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
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testOIDCServer creates an httptest.Server that serves:
// 1. GET /.well-known/openid-configuration → OIDC discovery JSON
// 2. GET /jwks → JWKS key set JSON
func testOIDCServer(t *testing.T) (*httptest.Server, *rsa.PrivateKey) {
	t.Helper()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	var issuer string
	mux := http.NewServeMux()

	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		discovery := map[string]interface{}{
			"issuer":   issuer,
			"jwks_uri": issuer + "/jwks",
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(discovery)
	})

	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		jwks := map[string]interface{}{
			"keys": []map[string]interface{}{
				{
					"kty": "RSA",
					"kid": "test-key-1",
					"use": "sig",
					"alg": "RS256",
					"n":   base64.RawURLEncoding.EncodeToString(privateKey.N.Bytes()),
					"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(privateKey.E)).Bytes()),
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(jwks)
	})

	ts := httptest.NewServer(mux)
	issuer = ts.URL
	return ts, privateKey
}

// mintTestJWT creates a signed JWT for testing with configurable claims.
func mintTestJWT(t *testing.T, privateKey *rsa.PrivateKey, claims jwt.MapClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = "test-key-1"
	tokenString, err := token.SignedString(privateKey)
	require.NoError(t, err)
	return tokenString
}

func TestNewOIDCValidator(t *testing.T) {
	ts, _ := testOIDCServer(t)
	defer ts.Close()

	cfg := OIDCConfig{
		IssuerURL:  ts.URL,
		Audience:   "agentcube-api",
		RolesClaim: "realm_access.roles",
	}

	validator, err := NewOIDCValidator(context.Background(), cfg)
	require.NoError(t, err)
	assert.NotNil(t, validator)
	assert.Equal(t, ts.URL, validator.issuer)
	assert.Equal(t, "agentcube-api", validator.audience)
	assert.Equal(t, "realm_access.roles", validator.rolesClaim)
}

func TestValidateToken_ValidToken(t *testing.T) {
	ts, privateKey := testOIDCServer(t)
	defer ts.Close()

	validator, err := NewOIDCValidator(context.Background(), OIDCConfig{
		IssuerURL:  ts.URL,
		Audience:   "agentcube-api",
		RolesClaim: "realm_access.roles",
	})
	require.NoError(t, err)

	rawToken := mintTestJWT(t, privateKey, jwt.MapClaims{
		"iss":   ts.URL,
		"sub":   "user-123",
		"aud":   "agentcube-api",
		"email": "test@example.com",
		"exp":   time.Now().Add(5 * time.Minute).Unix(),
		"iat":   time.Now().Unix(),
		"realm_access": map[string]interface{}{
			"roles": []interface{}{"sandbox:invoke", "sandbox:manage"},
		},
	})

	claims, err := validator.ValidateToken(context.Background(), rawToken)
	require.NoError(t, err)
	assert.Equal(t, "user-123", claims.Subject)
	assert.Equal(t, "test@example.com", claims.Email)
	assert.Equal(t, []string{"sandbox:invoke", "sandbox:manage"}, claims.Roles)
}

func TestValidateToken_ExpiredToken(t *testing.T) {
	ts, privateKey := testOIDCServer(t)
	defer ts.Close()

	validator, err := NewOIDCValidator(context.Background(), OIDCConfig{
		IssuerURL: ts.URL,
		Audience:  "agentcube-api",
	})
	require.NoError(t, err)

	rawToken := mintTestJWT(t, privateKey, jwt.MapClaims{
		"iss": ts.URL,
		"sub": "user-123",
		"aud": "agentcube-api",
		"exp": time.Now().Add(-1 * time.Hour).Unix(),
		"iat": time.Now().Add(-2 * time.Hour).Unix(),
	})

	_, err = validator.ValidateToken(context.Background(), rawToken)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "expired")
}

func TestValidateToken_WrongAudience(t *testing.T) {
	ts, privateKey := testOIDCServer(t)
	defer ts.Close()

	validator, err := NewOIDCValidator(context.Background(), OIDCConfig{
		IssuerURL: ts.URL,
		Audience:  "agentcube-api",
	})
	require.NoError(t, err)

	rawToken := mintTestJWT(t, privateKey, jwt.MapClaims{
		"iss": ts.URL,
		"sub": "user-123",
		"aud": "wrong-audience",
		"exp": time.Now().Add(5 * time.Minute).Unix(),
	})

	_, err = validator.ValidateToken(context.Background(), rawToken)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid audience")
}

func TestValidateToken_WrongIssuer(t *testing.T) {
	ts, privateKey := testOIDCServer(t)
	defer ts.Close()

	validator, err := NewOIDCValidator(context.Background(), OIDCConfig{
		IssuerURL: ts.URL,
		Audience:  "agentcube-api",
	})
	require.NoError(t, err)

	rawToken := mintTestJWT(t, privateKey, jwt.MapClaims{
		"iss": "http://wrong-issuer.example.com",
		"sub": "user-123",
		"aud": "agentcube-api",
		"exp": time.Now().Add(5 * time.Minute).Unix(),
	})

	_, err = validator.ValidateToken(context.Background(), rawToken)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid issuer")
}

func TestValidateToken_InvalidSignature(t *testing.T) {
	ts, _ := testOIDCServer(t)
	defer ts.Close()

	validator, err := NewOIDCValidator(context.Background(), OIDCConfig{
		IssuerURL: ts.URL,
		Audience:  "agentcube-api",
	})
	require.NoError(t, err)

	// Sign with a different key than what the JWKS endpoint serves
	wrongKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	rawToken := mintTestJWT(t, wrongKey, jwt.MapClaims{
		"iss": ts.URL,
		"sub": "user-123",
		"aud": "agentcube-api",
		"exp": time.Now().Add(5 * time.Minute).Unix(),
	})

	_, err = validator.ValidateToken(context.Background(), rawToken)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "signature verification failed")
}

func TestExtractRolesFromClaims(t *testing.T) {
	tests := []struct {
		name     string
		claims   map[string]interface{}
		path     string
		expected []string
	}{
		{
			name: "Keycloak nested path",
			claims: map[string]interface{}{
				"realm_access": map[string]interface{}{
					"roles": []interface{}{"sandbox:invoke", "sandbox:manage"},
				},
			},
			path:     "realm_access.roles",
			expected: []string{"sandbox:invoke", "sandbox:manage"},
		},
		{
			name:     "missing intermediate key returns nil",
			claims:   map[string]interface{}{"other": "value"},
			path:     "realm_access.roles",
			expected: nil,
		},
		{
			name: "non-array final value returns nil",
			claims: map[string]interface{}{
				"realm_access": map[string]interface{}{
					"roles": "not-an-array",
				},
			},
			path:     "realm_access.roles",
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractRolesFromClaims(tt.claims, tt.path)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestValidateToken_MissingSubject(t *testing.T) {
	ts, privateKey := testOIDCServer(t)
	defer ts.Close()

	validator, err := NewOIDCValidator(context.Background(), OIDCConfig{
		IssuerURL: ts.URL,
		Audience:  "agentcube-api",
	})
	require.NoError(t, err)

	// Mint a token with no "sub" claim
	rawToken := mintTestJWT(t, privateKey, jwt.MapClaims{
		"iss": ts.URL,
		"aud": "agentcube-api",
		"exp": time.Now().Add(5 * time.Minute).Unix(),
	})

	_, err = validator.ValidateToken(context.Background(), rawToken)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "token missing required sub claim")
}
