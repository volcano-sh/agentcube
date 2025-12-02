package picod

import (
	"bytes"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

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
	exec.Command("chattr", "+i", am.keyFile).Run()

	am.publicKey = rsaPub
	am.initialized = true
	return nil
}

// VerifyRSASignature verifies RSA signature for given key and data
func VerifyRSASignature(pubKey *rsa.PublicKey, timestamp, body, signature string) bool {
	if pubKey == nil {
		return false
	}

	// Decode signature
	sigBytes, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		return false
	}

	// Create message hash (timestamp + body)
	message := timestamp + string(body)
	hashed := sha256.Sum256([]byte(message))

	// Verify signature
	err = rsa.VerifyPKCS1v15(pubKey, crypto.SHA256, hashed[:], sigBytes)
	return err == nil
}

// VerifySignature verifies RSA signature using the session public key
func (am *AuthManager) VerifySignature(timestamp, body, signature string) bool {
	am.mutex.RLock()
	defer am.mutex.RUnlock()

	if !am.initialized || am.publicKey == nil {
		return false
	}

	return VerifyRSASignature(am.publicKey, timestamp, body, signature)
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

// Helper function to validate timestamp
func validateTimestamp(c *gin.Context, timestamp string) bool {
	// Validate timestamp (prevent replay attacks, allow 5-minute window)
	ts, err := time.Parse(time.RFC3339, timestamp)
	if err != nil {
		// Try Unix timestamp format
		unixTs := parseInt(timestamp, 0)
		if unixTs > 0 {
			ts = time.Unix(unixTs, 0)
		} else {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error":  "Invalid timestamp format",
				"code":   http.StatusUnauthorized,
				"detail": "Use RFC3339 format or Unix timestamp",
			})
			c.Abort()
			return false
		}
	}

	// Check timestamp window (5 minutes)
	if time.Since(ts) > 5*time.Minute || ts.After(time.Now().Add(5*time.Minute)) {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error":  "Timestamp out of range",
			"code":   http.StatusUnauthorized,
			"detail": "Timestamp must be within 5 minutes of current time",
		})
		c.Abort()
		return false
	}
	return true
}

// AuthMiddleware creates authentication middleware with RSA signature verification
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

		// Get timestamp and signature from headers
		timestamp := c.GetHeader("X-Timestamp")
		signature := c.GetHeader("X-Signature")

		if timestamp == "" || signature == "" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error":  "Missing X-Timestamp or X-Signature headers",
				"code":   http.StatusUnauthorized,
				"detail": "Please provide both X-Timestamp and X-Signature headers",
			})
			c.Abort()
			return
		}

		// Validate timestamp (prevent replay attacks, allow 5-minute window)
		if !validateTimestamp(c, timestamp) {
			return
		}

		// Read request body for signature verification
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

		// Verify signature
		if !am.VerifySignature(timestamp, string(bodyBytes), signature) {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error":  "Invalid signature",
				"code":   http.StatusUnauthorized,
				"detail": "Signature verification failed",
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

// Helper function to parse integer
func parseInt(s string, defaultValue int64) int64 {
	if s == "" {
		return defaultValue
	}

	var result int64
	for _, r := range s {
		if r < '0' || r > '9' {
			return defaultValue
		}
		result = result*10 + int64(r-'0')
	}
	return result
}

// RequestBody wraps bytes.Buffer to implement io.ReadCloser
type RequestBody struct {
	*bytes.Buffer
}

func (rb *RequestBody) Close() error {
	return nil
}
