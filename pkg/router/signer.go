package router

import (
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// RequestSigner handles signing of requests with a static private key
type RequestSigner struct {
	privateKey *rsa.PrivateKey
}

// NewRequestSigner creates a new RequestSigner
func NewRequestSigner(keyFile string) (*RequestSigner, error) {
	data, err := os.ReadFile(keyFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read private key file: %v", err)
	}

	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("failed to decode PEM block")
	}

	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		// Try PKCS8 if PKCS1 fails
		pkcs8Key, err2 := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err2 != nil {
			return nil, fmt.Errorf("failed to parse private key: %v (also tried PKCS8: %v)", err, err2)
		}
		var ok bool
		key, ok = pkcs8Key.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("key is not RSA private key")
		}
	}

	return &RequestSigner{
		privateKey: key,
	}, nil
}

// SignRequest adds a signed JWT Authorization header to the request
func (rs *RequestSigner) SignRequest(req *http.Request, body []byte) error {
	claims := jwt.MapClaims{
		"iss": "router",
		"iat": time.Now().Unix(),
		"exp": time.Now().Add(5 * time.Minute).Unix(),
	}

	// Calculate body hash if body is present
	if len(body) > 0 {
		hash := sha256.Sum256(body)
		claims["body_sha256"] = fmt.Sprintf("%x", hash)
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tokenString, err := token.SignedString(rs.privateKey)
	if err != nil {
		return fmt.Errorf("failed to sign token: %v", err)
	}

	req.Header.Set("Authorization", "Bearer "+tokenString)
	return nil
}
