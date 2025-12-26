package router

import (
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
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
	// Build canonical request hash for anti-tampering
	canonicalRequestHash := buildCanonicalRequestHash(req, body)

	claims := jwt.MapClaims{
		"iss":                      "router",
		"iat":                      time.Now().Unix(),
		"exp":                      time.Now().Add(5 * time.Minute).Unix(),
		"canonical_request_sha256": canonicalRequestHash,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodPS256, claims)
	tokenString, err := token.SignedString(rs.privateKey)
	if err != nil {
		return fmt.Errorf("failed to sign token: %v", err)
	}

	req.Header.Set("Authorization", "Bearer "+tokenString)
	return nil
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
