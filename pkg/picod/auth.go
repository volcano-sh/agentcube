package picod

import (
	"bytes"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

const (
	KeyFile = "picod_public_key.pem"
)

// AuthManager manages RSA public key authentication
type AuthManager struct {
	publicKey    *rsa.PublicKey
	bootstrapKey *rsa.PublicKey // Key injected at startup for init authentication
	mutex        sync.RWMutex
	keyFile      string
	initialized  bool
}

// InitRequest represents initialization request with public key
type InitRequest struct {
	PublicKey string `json:"public_key" binding:"required"`
}

// InitResponse represents initialization response
type InitResponse struct {
	Message string `json:"message"`
	Success bool   `json:"success"`
}

// NewAuthManager creates a new auth manager
func NewAuthManager() *AuthManager {
	return &AuthManager{
		keyFile:     KeyFile,
		initialized: false,
	}
}

// LoadBootstrapKey loads the bootstrap public key from string
func (am *AuthManager) LoadBootstrapKey(keyStr string) error {
	if keyStr == "" {
		return nil
	}

	block, _ := pem.Decode([]byte(keyStr))
	if block == nil {
		return fmt.Errorf("failed to decode bootstrap key PEM block")
	}

	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return fmt.Errorf("failed to parse bootstrap public key: %v", err)
	}

	rsaPub, ok := pub.(*rsa.PublicKey)
	if !ok {
		return fmt.Errorf("bootstrap key is not an RSA public key")
	}

	am.bootstrapKey = rsaPub
	return nil
}

// LoadPublicKey loads public key from file
func (am *AuthManager) LoadPublicKey() error {
	am.mutex.Lock()
	defer am.mutex.Unlock()

	if _, err := os.Stat(am.keyFile); os.IsNotExist(err) {
		return fmt.Errorf("no public key file found, server not initialized")
	}

	data, err := os.ReadFile(am.keyFile)
	if err != nil {
		return fmt.Errorf("failed to read public key file: %v", err)
	}

	block, _ := pem.Decode(data)
	if block == nil {
		return fmt.Errorf("failed to decode PEM block")
	}

	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return fmt.Errorf("failed to parse public key: %v", err)
	}

	rsaPub, ok := pub.(*rsa.PublicKey)
	if !ok {
		return fmt.Errorf("not an RSA public key")
	}

	am.publicKey = rsaPub
	am.initialized = true
	return nil
}

// SavePublicKey saves public key to file
func (am *AuthManager) SavePublicKey(publicKeyStr string) error {
	am.mutex.Lock()
	defer am.mutex.Unlock()

	// Parse the public key
	block, _ := pem.Decode([]byte(publicKeyStr))
	if block == nil {
		return fmt.Errorf("failed to decode PEM block")
	}

	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return fmt.Errorf("failed to parse public key: %v", err)
	}

	rsaPub, ok := pub.(*rsa.PublicKey)
	if !ok {
		return fmt.Errorf("not an RSA public key")
	}

	// Check if already initialized
	if am.initialized {
		return fmt.Errorf("server already initialized with a public key")
	}

	// Save to file with read-only permissions
	if err := os.WriteFile(am.keyFile, []byte(publicKeyStr), 0400); err != nil {
		return fmt.Errorf("failed to save public key file: %v", err)
	}

	// Try to make the file immutable (Linux only)
	if runtime.GOOS == "linux" {
		cmd := exec.Command("chattr", "+i", am.keyFile)
		if err := cmd.Run(); err != nil {
			log.Printf("Warning: failed to make key file immutable: %v. File permissions still set to read-only.", err)
		} else {
			log.Printf("Key file successfully set to immutable (chattr +i)")
		}
	} else {
		log.Printf("Note: chattr command is Linux-specific. Current OS: %s. File permissions set to read-only.", runtime.GOOS)
	}

	am.publicKey = rsaPub
	am.initialized = true
	return nil
}

// IsInitialized checks if server is initialized
func (am *AuthManager) IsInitialized() bool {
	am.mutex.RLock()
	defer am.mutex.RUnlock()
	return am.initialized
}

// InitHandler handles initialization requests
func (am *AuthManager) InitHandler(c *gin.Context) {
	// Check if already initialized
	if am.IsInitialized() {
		c.JSON(http.StatusForbidden, gin.H{
			"error":  "Server already initialized",
			"code":   http.StatusForbidden,
			"detail": "This PicoD instance is already owned by another client",
		})
		return
	}

	// Always require bootstrap key authentication
	if am.bootstrapKey == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error":  "PicoD is not configured for secure initialization",
			"code":   http.StatusServiceUnavailable,
			"detail": "Bootstrap key is missing. Please configure PICOD_BOOTSTRAP_KEY environment variable.",
		})
		return
	}

	authHeader := c.GetHeader("Authorization")
	if authHeader == "" {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error":  "Missing Authorization header",
			"code":   http.StatusUnauthorized,
			"detail": "Init requires JWT authentication",
		})
		return
	}

	parts := strings.Split(authHeader, " ")
	if len(parts) != 2 || parts[0] != "Bearer" {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error":  "Invalid Authorization header format",
			"code":   http.StatusUnauthorized,
			"detail": "Use Bearer <token>",
		})
		return
	}

	tokenString := parts[1]

	// Parse and validate JWT
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return am.bootstrapKey, nil
	})

	if err != nil || !token.Valid {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error":  "Invalid token",
			"code":   http.StatusUnauthorized,
			"detail": fmt.Sprintf("JWT verification failed: %v", err),
		})
		return
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": "Invalid claims",
			"code":  http.StatusUnauthorized,
		})
		return
	}

	// Extract session_public_key
	sessionPublicKey, ok := claims["session_public_key"].(string)
	if !ok || sessionPublicKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Missing session_public_key in token",
			"code":  http.StatusBadRequest,
		})
		return
	}

	// Save the public key
	if err := am.SavePublicKey(sessionPublicKey); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Failed to save public key: %v", err),
			"code":  http.StatusInternalServerError,
		})
		return
	}

	c.JSON(http.StatusOK, InitResponse{
		Message: "Server initialized successfully. This Picod instance is now locked to your public key.",
		Success: true,
	})
}

// AuthMiddleware creates authentication middleware with JWT verification
func (am *AuthManager) AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Skip authentication for health check and init endpoint
		if c.Request.URL.Path == "/health" || c.Request.URL.Path == "/api/init" {
			c.Next()
			return
		}

		// Check if server is initialized
		if !am.IsInitialized() {
			c.JSON(http.StatusForbidden, gin.H{
				"error":  "Server not initialized",
				"code":   http.StatusForbidden,
				"detail": "Please initialize this Picod instance first via /api/init",
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

		// Parse and validate JWT using Session Public Key
		token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
			if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
			}
			// Use the session public key for verification
			// Lock is handled by IsInitialized check above, but safe to read pointer here
			// strictly speaking we should lock to read am.publicKey if it can change,
			// but it's set once at init.
			am.mutex.RLock()
			defer am.mutex.RUnlock()
			return am.publicKey, nil
		})

		if err != nil || !token.Valid {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error":  "Invalid token",
				"code":   http.StatusUnauthorized,
				"detail": fmt.Sprintf("JWT verification failed: %v", err),
			})
			c.Abort()
			return
		}

		claims, ok := token.Claims.(jwt.MapClaims)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "Invalid claims",
				"code":  http.StatusUnauthorized,
			})
			c.Abort()
			return
		}

		// Read request body for hash verification
		bodyBytes, err := c.GetRawData()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":  "Failed to read request body",
				"code":   http.StatusInternalServerError,
				"detail": err.Error(),
			})
			c.Abort()
			return
		}

		// Restore request body for subsequent handlers
		c.Request.Body = &RequestBody{Buffer: bytes.NewBuffer(bodyBytes)}

		// Verify body hash if present in claims
		// We mandate body_sha256 for requests with body to ensure integrity
		if claimHash, ok := claims["body_sha256"].(string); ok {
			// Calculate SHA256 of actual body
			hash := sha256.Sum256(bodyBytes)
			computedHash := fmt.Sprintf("%x", hash)

			if claimHash != computedHash {
				c.JSON(http.StatusUnauthorized, gin.H{
					"error":  "Body integrity check failed",
					"code":   http.StatusUnauthorized,
					"detail": "The request body does not match the body_sha256 claim",
				})
				c.Abort()
				return
			}
		} else {
			// Ideally we should enforce this, but for GET requests without body it might be empty.
			// For now, if body is not empty, enforce hash presence?
			// Let's enforce it if the body is not empty.
			// Exception: multipart/form-data requests (file uploads) where client cannot easily compute hash
			isMultipart := strings.HasPrefix(c.ContentType(), "multipart/form-data")
			if len(bodyBytes) > 0 && !isMultipart {
				c.JSON(http.StatusUnauthorized, gin.H{
					"error":  "Missing body_sha256 claim",
					"code":   http.StatusUnauthorized,
					"detail": "Token must contain body_sha256 claim for integrity",
				})
				c.Abort()
				return
			}
		}

		c.Next()
	}
}

// RequestBody wraps bytes.Buffer to implement io.ReadCloser
type RequestBody struct {
	*bytes.Buffer
}

func (rb *RequestBody) Close() error {
	return nil
}
