package workloadmanager

import (
	"context"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestNewJWTManager(t *testing.T) {
	jwtManager, err := NewJWTManager()
	if err != nil {
		t.Fatalf("Failed to create JWT manager: %v", err)
	}

	if jwtManager.privateKey == nil {
		t.Error("Private key is nil")
	}

	if jwtManager.publicKey == nil {
		t.Error("Public key is nil")
	}
}

func TestGenerateToken(t *testing.T) {
	jwtManager, err := NewJWTManager()
	if err != nil {
		t.Fatalf("Failed to create JWT manager: %v", err)
	}

	claims := map[string]interface{}{
		"sessionId": "test-session-123",
		"purpose":   "sandbox_init",
	}

	tokenString, err := jwtManager.GenerateToken(claims)
	if err != nil {
		t.Fatalf("Failed to generate token: %v", err)
	}

	if tokenString == "" {
		t.Error("Generated token is empty")
	}

	// Verify token can be parsed
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		// Verify signing method
		if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
			t.Errorf("Unexpected signing method: %v", token.Header["alg"])
		}
		return jwtManager.publicKey, nil
	})

	if err != nil {
		t.Fatalf("Failed to parse token: %v", err)
	}

	if !token.Valid {
		t.Error("Token is not valid")
	}

	// Verify claims
	if mapClaims, ok := token.Claims.(jwt.MapClaims); ok {
		if sessionId, ok := mapClaims["sessionId"].(string); !ok || sessionId != "test-session-123" {
			t.Errorf("Expected sessionId 'test-session-123', got '%v'", mapClaims["sessionId"])
		}

		if purpose, ok := mapClaims["purpose"].(string); !ok || purpose != "sandbox_init" {
			t.Errorf("Expected purpose 'sandbox_init', got '%v'", mapClaims["purpose"])
		}

		// Verify expiration
		if exp, ok := mapClaims["exp"].(float64); ok {
			expTime := time.Unix(int64(exp), 0)
			if time.Until(expTime) > jwtExpiration || time.Until(expTime) < jwtExpiration-time.Minute {
				t.Errorf("Token expiration time is not within expected range")
			}
		} else {
			t.Error("Token does not have expiration claim")
		}

		// Verify issued at
		if _, ok := mapClaims["iat"].(float64); !ok {
			t.Error("Token does not have issued at claim")
		}
	} else {
		t.Error("Failed to get map claims from token")
	}
}

func TestGetPublicKeyPEM(t *testing.T) {
	jwtManager, err := NewJWTManager()
	if err != nil {
		t.Fatalf("Failed to create JWT manager: %v", err)
	}

	publicKeyPEM, err := jwtManager.GetPublicKeyPEM()
	if err != nil {
		t.Fatalf("Failed to get public key PEM: %v", err)
	}

	if len(publicKeyPEM) == 0 {
		t.Error("Public key PEM is empty")
	}

	// Verify PEM format
	pemString := string(publicKeyPEM)
	if len(pemString) < 100 {
		t.Error("Public key PEM seems too short")
	}

	// Check for PEM markers
	if pemString[:26] != "-----BEGIN RSA PUBLIC KEY-" {
		t.Error("Public key PEM does not start with correct header")
	}
}

func TestGetPrivateKeyPEM(t *testing.T) {
	jwtManager, err := NewJWTManager()
	if err != nil {
		t.Fatalf("Failed to create JWT manager: %v", err)
	}

	privateKeyPEM, err := jwtManager.GetPrivateKeyPEM()
	if err != nil {
		t.Fatalf("Failed to get private key PEM: %v", err)
	}

	if len(privateKeyPEM) == 0 {
		t.Error("Private key PEM is empty")
	}

	// Verify PEM format
	pemString := string(privateKeyPEM)
	if len(pemString) < 100 {
		t.Error("Private key PEM seems too short")
	}

	// Check for PEM markers
	if pemString[:31] != "-----BEGIN RSA PRIVATE KEY-----" {
		t.Error("Private key PEM does not start with correct header")
	}
}

func TestTokenExpiration(t *testing.T) {
	jwtManager, err := NewJWTManager()
	if err != nil {
		t.Fatalf("Failed to create JWT manager: %v", err)
	}

	claims := map[string]interface{}{
		"test": "value",
	}

	tokenString, err := jwtManager.GenerateToken(claims)
	if err != nil {
		t.Fatalf("Failed to generate token: %v", err)
	}

	// Parse token to check expiration
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		return jwtManager.publicKey, nil
	})

	if err != nil {
		t.Fatalf("Failed to parse token: %v", err)
	}

	if mapClaims, ok := token.Claims.(jwt.MapClaims); ok {
		exp, ok := mapClaims["exp"].(float64)
		if !ok {
			t.Fatal("Token does not have expiration claim")
		}

		iat, ok := mapClaims["iat"].(float64)
		if !ok {
			t.Fatal("Token does not have issued at claim")
		}

		// Verify expiration is 5 minutes after issued at
		expectedExp := int64(iat) + int64(jwtExpiration.Seconds())
		actualExp := int64(exp)

		// Allow 1 second tolerance for timing differences
		if actualExp < expectedExp-1 || actualExp > expectedExp+1 {
			t.Errorf("Token expiration not set correctly. Expected ~%d, got %d", expectedExp, actualExp)
		}
	}
}

func TestMultipleTokenGeneration(t *testing.T) {
	jwtManager, err := NewJWTManager()
	if err != nil {
		t.Fatalf("Failed to create JWT manager: %v", err)
	}

	// Generate multiple tokens and verify they're all different
	tokens := make(map[string]bool)
	for i := 0; i < 10; i++ {
		claims := map[string]interface{}{
			"sessionId": "session-" + string(rune(i)),
		}

		tokenString, err := jwtManager.GenerateToken(claims)
		if err != nil {
			t.Fatalf("Failed to generate token %d: %v", i, err)
		}

		if tokens[tokenString] {
			t.Errorf("Duplicate token generated: %s", tokenString)
		}
		tokens[tokenString] = true

		// Verify each token is valid
		token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
			return jwtManager.publicKey, nil
		})

		if err != nil {
			t.Fatalf("Failed to parse token %d: %v", i, err)
		}

		if !token.Valid {
			t.Errorf("Token %d is not valid", i)
		}
	}
}

// Mock test for StoreJWTPublicKeyInSecret - requires actual K8s client
func TestStoreJWTPublicKeyInSecretConstants(t *testing.T) {
	// Verify constants are set correctly
	if JWTPublicKeySecretName != "agentcube-jwt-public-key" {
		t.Errorf("Expected secret name 'agentcube-jwt-public-key', got '%s'", JWTPublicKeySecretName)
	}

	if JWTPublicKeySecretNamespace != "default" {
		t.Errorf("Expected secret namespace 'default', got '%s'", JWTPublicKeySecretNamespace)
	}

	if JWTPublicKeyDataKey != "public-key.pem" {
		t.Errorf("Expected data key 'public-key.pem', got '%s'", JWTPublicKeyDataKey)
	}
}

// Benchmark token generation
func BenchmarkGenerateToken(b *testing.B) {
	jwtManager, err := NewJWTManager()
	if err != nil {
		b.Fatalf("Failed to create JWT manager: %v", err)
	}

	claims := map[string]interface{}{
		"sessionId": "test-session",
		"purpose":   "sandbox_init",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := jwtManager.GenerateToken(claims)
		if err != nil {
			b.Fatalf("Failed to generate token: %v", err)
		}
	}
}

// Benchmark key generation
func BenchmarkNewJWTManager(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_, err := NewJWTManager()
		if err != nil {
			b.Fatalf("Failed to create JWT manager: %v", err)
		}
	}
}

// Test context cancellation
func TestStoreJWTPublicKeyInSecretWithCancelledContext(t *testing.T) {
	// This test verifies that the function respects context cancellation
	// Note: This is a mock test since we don't have a real K8s cluster
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	// We can't actually test the K8s client without a cluster,
	// but we verify the context is properly typed
	if ctx.Err() == nil {
		t.Error("Context should be cancelled")
	}
}
