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
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func generateTestKeyPair(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate RSA key: %v", err)
	}

	pubBytes, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		t.Fatalf("failed to marshal public key: %v", err)
	}

	pubPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: pubBytes,
	})

	return privateKey, string(pubPEM)
}

func signTestToken(t *testing.T, key *rsa.PrivateKey, claims jwt.MapClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := token.SignedString(key)
	if err != nil {
		t.Fatalf("failed to sign token: %v", err)
	}
	return signed
}

func TestVerifyIdentityJWT_ValidToken(t *testing.T) {
	privKey, pubPEM := generateTestKeyPair(t)

	token := signTestToken(t, privKey, jwt.MapClaims{
		"sub": "user-123",
		"aud": "workloadmanager",
		"exp": time.Now().Add(5 * time.Minute).Unix(),
		"iat": time.Now().Unix(),
	})

	sub, err := verifyIdentityJWT(pubPEM, token)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if sub != "user-123" {
		t.Errorf("expected sub 'user-123', got %q", sub)
	}
}

func TestVerifyIdentityJWT_ExpiredToken(t *testing.T) {
	privKey, pubPEM := generateTestKeyPair(t)

	token := signTestToken(t, privKey, jwt.MapClaims{
		"sub": "user-123",
		"aud": "workloadmanager",
		"exp": time.Now().Add(-5 * time.Minute).Unix(),
	})

	_, err := verifyIdentityJWT(pubPEM, token)
	if err == nil {
		t.Fatal("expected error for expired token")
	}
}

func TestVerifyIdentityJWT_WrongAudience(t *testing.T) {
	privKey, pubPEM := generateTestKeyPair(t)

	token := signTestToken(t, privKey, jwt.MapClaims{
		"sub": "user-123",
		"aud": "wrong-audience",
		"exp": time.Now().Add(5 * time.Minute).Unix(),
	})

	_, err := verifyIdentityJWT(pubPEM, token)
	if err == nil {
		t.Fatal("expected error for wrong audience")
	}
}

func TestVerifyIdentityJWT_TamperedSignature(t *testing.T) {
	privKey, pubPEM := generateTestKeyPair(t)

	token := signTestToken(t, privKey, jwt.MapClaims{
		"sub": "user-123",
		"aud": "workloadmanager",
		"exp": time.Now().Add(5 * time.Minute).Unix(),
	})

	// Use a completely different key to sign the same claims
	otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate second RSA key: %v", err)
	}
	tampered := signTestToken(t, otherKey, jwt.MapClaims{
		"sub": "user-123",
		"aud": "workloadmanager",
		"exp": time.Now().Add(5 * time.Minute).Unix(),
	})

	// Verify the original token works
	_, err = verifyIdentityJWT(pubPEM, token)
	if err != nil {
		t.Fatalf("valid token should pass: %v", err)
	}

	// Verify the tampered token (signed with different key) fails
	_, err = verifyIdentityJWT(pubPEM, tampered)
	if err == nil {
		t.Fatal("expected error for token signed with different key")
	}
}

func TestVerifyIdentityJWT_MissingSub(t *testing.T) {
	privKey, pubPEM := generateTestKeyPair(t)

	token := signTestToken(t, privKey, jwt.MapClaims{
		"aud": "workloadmanager",
		"exp": time.Now().Add(5 * time.Minute).Unix(),
	})

	_, err := verifyIdentityJWT(pubPEM, token)
	if err == nil {
		t.Fatal("expected error for missing sub")
	}
}

func TestSha256Short(t *testing.T) {
	result := sha256Short("test-user-id")
	if len(result) != 63 {
		t.Errorf("expected length 63, got %d", len(result))
	}

	// Verify deterministic
	if sha256Short("test-user-id") != result {
		t.Error("sha256Short should be deterministic")
	}

	// Verify different inputs produce different outputs
	if sha256Short("other-user-id") == result {
		t.Error("different inputs should produce different hashes")
	}
}

func TestExtractOwnerID(t *testing.T) {
	// Backup original cached public key and restore after test
	publicKeyCacheMutex.Lock()
	origKey := cachedPublicKey
	publicKeyCacheMutex.Unlock()
	defer func() {
		publicKeyCacheMutex.Lock()
		cachedPublicKey = origKey
		publicKeyCacheMutex.Unlock()
	}()

	req := httptest.NewRequest(http.MethodPost, "/sandbox", nil)
	owner, err := extractOwnerID(req)
	if owner != "" || !errors.Is(err, ErrNoIdentityHeader) {
		t.Errorf("expected ErrNoIdentityHeader, got owner=%q, err=%v", owner, err)
	}

	req = httptest.NewRequest(http.MethodPost, "/sandbox", nil)
	req.Header.Set(identityJWTHeader, "some-token")
	publicKeyCacheMutex.Lock()
	cachedPublicKey = ""
	publicKeyCacheMutex.Unlock()
	owner, err = extractOwnerID(req)
	if owner != "" || !errors.Is(err, ErrPublicKeyNotCached) {
		t.Errorf("expected ErrPublicKeyNotCached, got owner=%q, err=%v", owner, err)
	}

	// Setup valid key for remaining cases
	privKey, pubPEM := generateTestKeyPair(t)
	publicKeyCacheMutex.Lock()
	cachedPublicKey = pubPEM
	publicKeyCacheMutex.Unlock()

	expiredToken := signTestToken(t, privKey, jwt.MapClaims{
		"sub": "user-123",
		"aud": "workloadmanager",
		"exp": time.Now().Add(-5 * time.Minute).Unix(),
	})
	req = httptest.NewRequest(http.MethodPost, "/sandbox", nil)
	req.Header.Set(identityJWTHeader, expiredToken)
	owner, err = extractOwnerID(req)
	if owner != "" || !errors.Is(err, ErrVerificationFailed) {
		t.Errorf("expected ErrVerificationFailed, got owner=%q, err=%v", owner, err)
	}

	validToken := signTestToken(t, privKey, jwt.MapClaims{
		"sub": "user-123",
		"aud": "workloadmanager",
		"exp": time.Now().Add(5 * time.Minute).Unix(),
	})
	req = httptest.NewRequest(http.MethodPost, "/sandbox", nil)
	req.Header.Set(identityJWTHeader, validToken)
	owner, err = extractOwnerID(req)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if owner != "user-123" {
		t.Errorf("expected owner 'user-123', got %q", owner)
	}
}
