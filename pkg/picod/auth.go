package picod

import (
	"bytes"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"k8s.io/klog/v2"
)

const (
	keyFile     = "picod_public_key.pem"
	MaxBodySize = 32 << 20 // 32 MB limit to prevent memory exhaustion
)

// AuthManager manages RSA public key authentication
type AuthManager struct {
	publicKey    *rsa.PublicKey
	bootstrapKey *rsa.PublicKey // Key injected at startup for init authentication
	mutex        sync.RWMutex
	keyFile      string
	initialized  bool
	authMode     string
}

// InitRequest represents initialization request with public key
type InitRequest struct {
	PublicKey string `json:"public_key" binding:"required"`
}

// InitResponse represents initialization response
type InitResponse struct {
	Message string `json:"message"`
}

// NewAuthManager creates a new auth manager
func NewAuthManager() *AuthManager {
	return &AuthManager{
		keyFile:     keyFile,
		initialized: false,
		authMode:    AuthModeDynamic,
	}
}

// SetAuthMode sets the authentication mode
func (am *AuthManager) SetAuthMode(mode string) {
	am.authMode = mode
}

// SetInitialized sets the initialization state
func (am *AuthManager) SetInitialized(initialized bool) {
	am.mutex.Lock()
	defer am.mutex.Unlock()
	am.initialized = initialized
}

// GetAuthMode returns the current authentication mode
func (am *AuthManager) GetAuthMode() string {
	return am.authMode
}

// LoadStaticPublicKey loads the static public key from PICOD_PUBLIC_KEY environment variable
// The key must be base64 encoded PEM format
func (am *AuthManager) LoadStaticPublicKey() error {
	am.mutex.Lock()
	defer am.mutex.Unlock()

	keyB64 := os.Getenv("PICOD_PUBLIC_KEY")
	if keyB64 == "" {
		return fmt.Errorf("PICOD_PUBLIC_KEY environment variable is not set")
	}

	// Decode base64
	data, err := base64.StdEncoding.DecodeString(keyB64)
	if err != nil {
		return fmt.Errorf("failed to decode base64 PICOD_PUBLIC_KEY: %v", err)
	}

	block, _ := pem.Decode(data)
	if block == nil {
		return fmt.Errorf("failed to decode PEM block from PICOD_PUBLIC_KEY")
	}

	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return fmt.Errorf("failed to parse static public key: %v", err)
	}

	rsaPub, ok := pub.(*rsa.PublicKey)
	if !ok {
		return fmt.Errorf("static key is not an RSA public key")
	}

	am.publicKey = rsaPub
	am.initialized = true
	return nil
}

// LoadBootstrapKey loads the bootstrap public key from bytes
func (am *AuthManager) LoadBootstrapKey(keyData []byte) error {
	if len(keyData) == 0 {
		return fmt.Errorf("bootstrap key string is empty")
	}

	block, _ := pem.Decode(keyData)
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

func (am *AuthManager) savePublicKeyLocked(publicKeyStr string) error {
	publicKeyByte, err := base64.RawStdEncoding.DecodeString(publicKeyStr)
	if err != nil {
		return fmt.Errorf("failed to decode base64")
	}

	// Parse the public key
	block, _ := pem.Decode(publicKeyByte)
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
	if err := os.WriteFile(am.keyFile, publicKeyByte, 0400); err != nil {
		return fmt.Errorf("failed to save public key file: %v", err)
	}

	// Try to make the file immutable (Linux only)
	if runtime.GOOS == "linux" {
		cmd := exec.Command("chattr", "+i", am.keyFile) //nolint:gosec // keyFile is internally managed
		if err := cmd.Run(); err != nil {
			klog.Warningf("failed to make key file immutable: %v. File permissions still set to read-only.", err)
		} else {
			klog.Info("Key file successfully set to immutable (chattr +i)")
		}
	} else {
		klog.Infof("Note: chattr command is Linux-specific. Current OS: %s. File permissions set to read-only.", runtime.GOOS)
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
	am.mutex.Lock()
	defer am.mutex.Unlock()

	// Block init if in static key mode
	if am.authMode == AuthModeStatic {
		c.JSON(http.StatusForbidden, gin.H{
			"error":  "Static key mode enabled",
			"code":   http.StatusForbidden,
			"detail": "Dynamic initialization is disabled in static key mode",
		})
		return
	}

	// Check if already initialized
	if am.initialized {
		c.JSON(http.StatusForbidden, gin.H{
			"error":  "Server already initialized",
			"code":   http.StatusForbidden,
			"detail": "This PicoD instance is already owned by another client",
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
		if _, ok := token.Method.(*jwt.SigningMethodRSAPSS); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v, expected PS256", token.Header["alg"])
		}
		return am.bootstrapKey, nil
	}, jwt.WithExpirationRequired(), jwt.WithIssuedAt(), jwt.WithLeeway(time.Minute))

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
	if err := am.savePublicKeyLocked(sessionPublicKey); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Failed to save public key: %v", err),
			"code":  http.StatusInternalServerError,
		})
		return
	}

	c.JSON(http.StatusOK, InitResponse{
		Message: "Server initialized successfully. This PicoD instance is now locked to your public key.",
	})
}

// AuthMiddleware creates authentication middleware with JWT verification
func (am *AuthManager) AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {

		// Check if server is initialized
		if !am.IsInitialized() {
			c.JSON(http.StatusForbidden, gin.H{
				"error":  "Server not initialized",
				"code":   http.StatusForbidden,
				"detail": fmt.Sprintf("Please initialize this Picod instance first via /init. Request path is %s", c.Request.URL.Path),
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
			if _, ok := token.Method.(*jwt.SigningMethodRSAPSS); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v, expected PS256", token.Header["alg"])
			}
			// Use the session public key for verification
			// Lock is handled by IsInitialized check above, but safe to read pointer here
			// strictly speaking we should lock to read am.publicKey if it can change,
			// but it's set once at init.
			am.mutex.RLock()
			defer am.mutex.RUnlock()
			return am.publicKey, nil
		}, jwt.WithExpirationRequired(), jwt.WithIssuedAt(), jwt.WithLeeway(time.Minute))

		if err != nil || !token.Valid {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error":  "Invalid token",
				"code":   http.StatusUnauthorized,
				"detail": fmt.Sprintf("JWT verification failed: %v", err),
			})
			c.Abort()
			return
		}

		// Read body for canonical request verification
		var bodyBytes []byte
		if c.Request.Body != nil {
			bodyBytes, _ = io.ReadAll(c.Request.Body)
			// Restore body for downstream handlers
			c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
		}

		// Verify canonical_request_sha256 claim to prevent request tampering
		claims, ok := token.Claims.(jwt.MapClaims)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error":  "Invalid token claims",
				"code":   http.StatusUnauthorized,
				"detail": "Token claims format is invalid",
			})
			c.Abort()
			return
		}

		claimedHash, hasHash := claims["canonical_request_sha256"].(string)
		if hasHash && claimedHash != "" {
			// Build canonical request and verify
			actualHash := buildCanonicalRequestHash(c.Request, bodyBytes)
			if claimedHash != actualHash {
				c.JSON(http.StatusUnauthorized, gin.H{
					"error":  "Request integrity check failed",
					"code":   http.StatusUnauthorized,
					"detail": "canonical_request_sha256 mismatch - request may have been tampered",
				})
				c.Abort()
				return
			}
		}

		// Enforce maximum body size to prevent memory exhaustion
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, MaxBodySize)

		c.Next()
	}
}

// buildCanonicalRequestHash builds a canonical request string and returns its SHA256 hash
// Format: HTTPMethod + \n + URI + \n + QueryString + \n + CanonicalHeaders + \n + SignedHeaders + \n + BodyHash
func buildCanonicalRequestHash(r *http.Request, body []byte) string {
	// 1. HTTP Method
	method := strings.ToUpper(r.Method)

	// 2. Canonical URI (path only)
	uri := r.URL.Path
	if uri == "" {
		uri = "/"
	}

	// 3. Canonical Query String (sorted)
	queryString := buildCanonicalQueryString(r)

	// 4. Canonical Headers (sorted, lowercase)
	canonicalHeaders, signedHeaders := buildCanonicalHeaders(r)

	// 5. Body hash
	bodyHash := fmt.Sprintf("%x", sha256.Sum256(body))

	// Build canonical request
	canonicalRequest := strings.Join([]string{
		method,
		uri,
		queryString,
		canonicalHeaders,
		signedHeaders,
		bodyHash,
	}, "\n")

	// Return SHA256 of canonical request
	hash := sha256.Sum256([]byte(canonicalRequest))
	return fmt.Sprintf("%x", hash)
}

// buildCanonicalQueryString builds a sorted query string
func buildCanonicalQueryString(r *http.Request) string {
	query := r.URL.Query()
	if len(query) == 0 {
		return ""
	}

	keys := make([]string, 0, len(query))
	for k := range query {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var pairs []string
	for _, k := range keys {
		values := query[k]
		sort.Strings(values)
		for _, v := range values {
			pairs = append(pairs, k+"="+v)
		}
	}

	return strings.Join(pairs, "&")
}

// buildCanonicalHeaders builds canonical headers string and returns signedHeaders list
func buildCanonicalHeaders(r *http.Request) (canonicalHeaders string, signedHeaders string) {
	// Only include content-type for request integrity
	headerMap := make(map[string]string)

	if v := r.Header.Get("Content-Type"); v != "" {
		headerMap["content-type"] = strings.TrimSpace(v)
	}

	// Sort header names
	keys := make([]string, 0, len(headerMap))
	for k := range headerMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Build canonical headers and signed headers
	var headerLines []string
	for _, k := range keys {
		headerLines = append(headerLines, k+":"+headerMap[k])
	}

	if len(headerLines) > 0 {
		canonicalHeaders = strings.Join(headerLines, "\n") + "\n"
	} else {
		canonicalHeaders = "\n"
	}
	signedHeaders = strings.Join(keys, ";")

	return canonicalHeaders, signedHeaders
}
