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
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"net/http"

	"github.com/golang-jwt/jwt/v5"
	"k8s.io/klog/v2"
)

// identityJWTHeader is the header carrying the Router-signed user identity.
const identityJWTHeader = "X-AgentCube-User-Identity"

// verifyIdentityJWT parses the Router-signed identity JWT, verifies the RSA signature, validates aud=="workloadmanager", and returns the sub claim.
func verifyIdentityJWT(publicKeyPEM string, rawToken string) (string, error) {
	block, _ := pem.Decode([]byte(publicKeyPEM))
	if block == nil {
		return "", fmt.Errorf("failed to decode public key PEM")
	}

	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("failed to parse public key: %w", err)
	}

	rsaPub, ok := pub.(*rsa.PublicKey)
	if !ok {
		return "", fmt.Errorf("public key is not RSA")
	}

	token, err := jwt.Parse(rawToken, func(_ *jwt.Token) (interface{}, error) {
		return rsaPub, nil
	}, jwt.WithValidMethods([]string{"RS256"}))
	if err != nil {
		return "", fmt.Errorf("token verification failed: %w", err)
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return "", fmt.Errorf("unexpected claims type")
	}

	// Validate audience
	aud, _ := claims["aud"].(string)
	if aud != "workloadmanager" {
		return "", fmt.Errorf("invalid audience: %q", aud)
	}

	sub, _ := claims["sub"].(string)
	if sub == "" {
		return "", fmt.Errorf("missing sub claim")
	}

	return sub, nil
}

// extractOwnerID reads the identity JWT from the request header and returns the verified subject, or empty string if the header is absent or invalid.
func extractOwnerID(r *http.Request) string {
	rawToken := r.Header.Get(identityJWTHeader)
	if rawToken == "" {
		return ""
	}

	publicKey := GetCachedPublicKey()
	if publicKey == "" {
		klog.V(2).Info("Identity JWT present but public key not cached, skipping owner extraction")
		return ""
	}

	sub, err := verifyIdentityJWT(publicKey, rawToken)
	if err != nil {
		klog.V(2).Infof("Identity JWT verification failed: %v", err)
		return ""
	}

	return sub
}

// sha256Short returns the first 63 characters of the hex-encoded SHA-256 hash.
func sha256Short(s string) string {
	h := sha256.Sum256([]byte(s))
	full := hex.EncodeToString(h[:])
	return full[:63]
}
