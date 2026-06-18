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

package picod

import (
	"context"
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"k8s.io/klog/v2"
)

var (
	// ErrAlreadyInitialized is returned when attempting to initialize PicoD session key again
	ErrAlreadyInitialized = errors.New("session has already been initialized")
)

const (

	// BootstrapPublicKeyEnvVar is the environment variable name for the Workload Manager's bootstrap RSA public key
	BootstrapPublicKeyEnvVar = "PICOD_BOOTSTRAP_PUBLIC_KEY"

	// SessionIDEnvVar is the environment variable name containing the unique ID for this sandbox
	SessionIDEnvVar = "PICOD_SESSION_ID"
)

// AuthManager manages RSA public key authentication
// The bootstrap public key is loaded from environment variable at startup
// The session public key is set dynamically via the /init endpoint
type AuthManager struct {
	bootstrapPublicKey *rsa.PublicKey
	sessionPublicKey   *ecdsa.PublicKey
	initialized        bool
	mutex              sync.RWMutex
	seenJTIs           sync.Map
}

// NewAuthManager creates a new AuthManager instance
func NewAuthManager(ctx context.Context) *AuthManager {
	am := &AuthManager{}

	// Start background goroutine to clean up seenJTIs
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				now := time.Now()
				am.seenJTIs.Range(func(key, value interface{}) bool {
					if t, ok := value.(time.Time); ok {
						// Bootstrap tokens are valid for up to 3 minutes, so any JTI older than that can be safely evicted
						if now.Sub(t) > 3*time.Minute {
							am.seenJTIs.Delete(key)
						}
					} else {
						// If the value is somehow not a time.Time, delete it
						am.seenJTIs.Delete(key)
					}
					return true
				})
			}
		}
	}()

	return am
}

// parseRSAPublicKeyFromPEM parses an RSA public key from a PEM string
func parseRSAPublicKeyFromPEM(keyData string) (*rsa.PublicKey, error) {
	block, _ := pem.Decode([]byte(keyData))
	if block == nil {
		return nil, fmt.Errorf("failed to decode PEM block")
	}

	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse public key: %w", err)
	}

	rsaPub, ok := pub.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("key is not an RSA public key")
	}

	return rsaPub, nil
}

// parseECDSAPublicKeyFromPEM parses an ECDSA public key from a PEM string
func parseECDSAPublicKeyFromPEM(keyData string) (*ecdsa.PublicKey, error) {
	block, _ := pem.Decode([]byte(keyData))
	if block == nil {
		return nil, errors.New("failed to parse PEM block")
	}

	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse public key: %w", err)
	}

	ecdsaPub, ok := pub.(*ecdsa.PublicKey)
	if !ok {
		return nil, errors.New("key is not an ECDSA public key")
	}

	return ecdsaPub, nil
}

// LoadBootstrapPublicKey loads the bootstrap public key from environment variable.
// The key should be in PEM format.
func (am *AuthManager) LoadBootstrapPublicKey() error {
	am.mutex.Lock()
	defer am.mutex.Unlock()

	keyData := os.Getenv(BootstrapPublicKeyEnvVar)
	if keyData == "" {
		keyData = os.Getenv("PICOD_AUTH_PUBLIC_KEY")
	}
	if keyData == "" {
		return fmt.Errorf("environment variable %s is not set (and fallback PICOD_AUTH_PUBLIC_KEY is also not set)", BootstrapPublicKeyEnvVar)
	}

	rsaPub, err := parseRSAPublicKeyFromPEM(keyData)
	if err != nil {
		return fmt.Errorf("failed to parse bootstrap public key: %w", err)
	}

	am.bootstrapPublicKey = rsaPub
	klog.Info("Bootstrap public key loaded successfully from environment variable")
	return nil
}

// SetSessionPublicKey parses and stores the ephemeral session public key
func (am *AuthManager) SetSessionPublicKey(keyData string) error {
	am.mutex.Lock()
	defer am.mutex.Unlock()

	if am.initialized {
		klog.Warning("Attempted to re-initialize an already initialized session")
		return ErrAlreadyInitialized
	}

	ecdsaPub, err := parseECDSAPublicKeyFromPEM(keyData)
	if err != nil {
		return fmt.Errorf("failed to parse session public key: %w", err)
	}

	am.sessionPublicKey = ecdsaPub
	am.initialized = true
	klog.Info("Session public key successfully registered via /init")
	return nil
}

// VerifyBootstrapJWT verifies the init token against the bootstrap public key and returns the session_public_key claim
func (am *AuthManager) VerifyBootstrapJWT(tokenStr string) (string, error) {
	token, err := jwt.Parse(tokenStr, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		am.mutex.RLock()
		defer am.mutex.RUnlock()
		if am.bootstrapPublicKey == nil {
			return nil, fmt.Errorf("bootstrap public key is not loaded")
		}
		return am.bootstrapPublicKey, nil
	}, jwt.WithExpirationRequired(), jwt.WithIssuedAt(), jwt.WithLeeway(time.Minute), jwt.WithIssuer("agentcube-workload-manager"))

	if err != nil {
		return "", fmt.Errorf("JWT verification failed: %w", err)
	}
	if token == nil || !token.Valid {
		return "", fmt.Errorf("JWT verification failed: token is invalid")
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return "", fmt.Errorf("invalid token claims")
	}

	jti, _ := claims["jti"].(string)
	if jti == "" {
		return "", fmt.Errorf("missing jti claim")
	}
	if _, loaded := am.seenJTIs.LoadOrStore(jti, time.Now()); loaded {
		return "", fmt.Errorf("token already used (replay detected)")
	}

	sessionID := os.Getenv(SessionIDEnvVar)
	if sessionID != "" {
		sub, err := token.Claims.GetSubject()
		if err != nil || sub != sessionID {
			return "", fmt.Errorf("token subject mismatch: expected %s, got %s", sessionID, sub)
		}
	}
	// If PICOD_SESSION_ID is unset the pod is a warm-pool pre-created instance;
	// the sub check is skipped and isolation is provided by the JTI replay guard above.

	sessionPubKey, ok := claims["session_public_key"].(string)
	if !ok || sessionPubKey == "" {
		return "", fmt.Errorf("missing or invalid session_public_key claim")
	}

	return sessionPubKey, nil
}

// AuthMiddleware creates authentication middleware with JWT verification
// Note: Requires the daemon to be initialized with a session public key.
func (am *AuthManager) AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Check if initialized
		am.mutex.RLock()
		isInit := am.initialized
		am.mutex.RUnlock()
		if !isInit {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"error":  "Daemon not initialized",
				"code":   "DAEMON_NOT_INITIALIZED",
				"detail": "PicoD is waiting for Workload Manager to initialize the session",
			})
			c.Abort()
			return
		}

		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error":  "Missing Authorization header",
				"code":   http.StatusUnauthorized,
				"detail": "Request requires JWT authentication",
			})
			c.Abort()
			return
		}

		parts := strings.Split(authHeader, " ")
		if len(parts) != 2 || parts[0] != "Bearer" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error":  "Invalid Authorization header format",
				"code":   http.StatusUnauthorized,
				"detail": "Use Bearer <token>",
			})
			c.Abort()
			return
		}

		tokenString := parts[1]

		// Parse and validate JWT using the session public key
		token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
			if _, ok := token.Method.(*jwt.SigningMethodECDSA); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
			}
			am.mutex.RLock()
			defer am.mutex.RUnlock()
			return am.sessionPublicKey, nil
		}, jwt.WithExpirationRequired(), jwt.WithIssuedAt(), jwt.WithIssuer("agentcube-router"), jwt.WithLeeway(time.Minute))

		if err != nil || !token.Valid {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error":  "Invalid token",
				"code":   http.StatusUnauthorized,
				"detail": fmt.Sprintf("JWT verification failed: %v", err),
			})
			c.Abort()
			return
		}

		c.Next()
	}
}
