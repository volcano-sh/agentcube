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
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
)

// OIDCConfig holds provider-agnostic OIDC configuration.
type OIDCConfig struct {
	// IssuerURL is the OIDC provider issuer URL.
	IssuerURL string

	// Audience is the expected aud claim in the JWT.
	Audience string

	// RolesClaim is the JSON path to the roles array.
	RolesClaim string
}

// Claims represents validated identity extracted from an OIDC access token.
type Claims struct {
	// Subject is the standard "sub" claim identifying the user.
	Subject string

	// Email is the standard "email" claim (may be empty).
	Email string

	// Roles extracted from the configured RolesClaim path.
	Roles []string
}

// OIDCValidator validates OIDC access tokens using cached JWKS keys.
type OIDCValidator struct {
	keySet     gooidc.KeySet // JWKS key set with automatic caching/rotation
	issuer     string        // expected issuer
	audience   string        // expected audience
	rolesClaim string        // dot-notation path to roles array
}

// NewOIDCValidator creates a new OIDCValidator instance.
func NewOIDCValidator(ctx context.Context, cfg OIDCConfig) (*OIDCValidator, error) {
	provider, err := gooidc.NewProvider(ctx, cfg.IssuerURL)
	if err != nil {
		return nil, fmt.Errorf("failed to discover OIDC provider at %s: %w", cfg.IssuerURL, err)
	}

	// Extract the JWKS URL from the provider's discovery document.
	var providerClaims struct {
		JWKSURL string `json:"jwks_uri"`
	}
	if err := provider.Claims(&providerClaims); err != nil {
		return nil, fmt.Errorf("failed to extract jwks_uri from OIDC discovery: %w", err)
	}

	// Create a RemoteKeySet for JWKS caching and automatic key rotation.
	keySet := gooidc.NewRemoteKeySet(context.Background(), providerClaims.JWKSURL)

	return &OIDCValidator{
		keySet:     keySet,
		issuer:     cfg.IssuerURL,
		audience:   cfg.Audience,
		rolesClaim: cfg.RolesClaim,
	}, nil
}

// accessTokenClaims represents the standard and custom claims in an access token.
type accessTokenClaims struct {
	Issuer    string       `json:"iss"`
	Subject   string       `json:"sub"`
	Audience  claimStrings `json:"aud"`
	Expiry    float64      `json:"exp"`
	NotBefore float64      `json:"nbf"`
	Email     string       `json:"email"`
}

// claimStrings handles the JWT "aud" claim which can be either a string or []string.
type claimStrings []string

func (cs *claimStrings) UnmarshalJSON(data []byte) error {
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		*cs = []string{single}
		return nil
	}
	var multi []string
	if err := json.Unmarshal(data, &multi); err != nil {
		return err
	}
	*cs = multi
	return nil
}

// ValidateToken verifies the JWT signature, standard claims, and extracts roles.
func (v *OIDCValidator) ValidateToken(ctx context.Context, rawToken string) (*Claims, error) {
	if rawToken == "" {
		return nil, fmt.Errorf("empty token")
	}

	// Verify signing algorithm before signature check
	if err := v.checkTokenAlgorithm(rawToken); err != nil {
		return nil, err
	}

	// Verify the JWT signature using the cached JWKS keys.
	payload, err := v.keySet.VerifySignature(ctx, rawToken)
	if err != nil {
		return nil, fmt.Errorf("token signature verification failed: %w", err)
	}

	// Parse the standard claims from the verified payload.
	var tokenClaims accessTokenClaims
	if err := json.Unmarshal(payload, &tokenClaims); err != nil {
		return nil, fmt.Errorf("failed to parse token claims: %w", err)
	}

	// Validate issuer
	if tokenClaims.Issuer != v.issuer {
		return nil, fmt.Errorf("invalid issuer: got %q, expected %q", tokenClaims.Issuer, v.issuer)
	}

	// Validate expiration
	if tokenClaims.Expiry == 0 {
		return nil, fmt.Errorf("token missing required exp claim")
	}
	if time.Now().After(time.Unix(int64(tokenClaims.Expiry), 0)) {
		return nil, fmt.Errorf("token has expired")
	}

	// Validate not-before (if present)
	if tokenClaims.NotBefore != 0 && time.Now().Before(time.Unix(int64(tokenClaims.NotBefore), 0)) {
		return nil, fmt.Errorf("token is not yet valid")
	}

	// Validate audience
	if v.audience != "" {
		if !slices.Contains(tokenClaims.Audience, v.audience) {
			return nil, fmt.Errorf("invalid audience: token audiences %v do not include %q", []string(tokenClaims.Audience), v.audience)
		}
	}

	// Extract roles from the full claims map using the configured path.
	var allClaims map[string]interface{}
	if err := json.Unmarshal(payload, &allClaims); err != nil {
		return nil, fmt.Errorf("failed to parse token claims for role extraction: %w", err)
	}
	roles := extractRolesFromClaims(allClaims, v.rolesClaim)

	return &Claims{
		Subject: tokenClaims.Subject,
		Email:   tokenClaims.Email,
		Roles:   roles,
	}, nil
}

// extractRolesFromClaims navigates a nested claims map using a dot-separated path.
func extractRolesFromClaims(claims map[string]interface{}, path string) []string {
	if path == "" {
		return nil
	}

	parts := strings.Split(path, ".")
	var current interface{} = claims

	for _, part := range parts {
		m, ok := current.(map[string]interface{})
		if !ok {
			return nil
		}
		current, ok = m[part]
		if !ok {
			return nil
		}
	}

	// The final value should be an array of strings.
	arr, ok := current.([]interface{})
	if !ok {
		return nil
	}

	var roles []string
	for _, v := range arr {
		if s, ok := v.(string); ok {
			roles = append(roles, s)
		}
	}
	return roles
}

// allowedSigningAlgs contains supported asymmetric algorithms for JWT signatures.
var allowedSigningAlgs = map[string]bool{
	"RS256": true,
	"RS384": true,
	"RS512": true,
	"ES256": true,
	"ES384": true,
	"ES512": true,
	"PS256": true,
	"PS384": true,
	"PS512": true,
	"EdDSA": true,
}

// checkTokenAlgorithm verifies the JWT signing algorithm is supported.
func (v *OIDCValidator) checkTokenAlgorithm(rawToken string) error {
	parts := strings.SplitN(rawToken, ".", 3)
	if len(parts) < 2 {
		return fmt.Errorf("malformed JWT: expected 3 parts, got %d", len(parts))
	}

	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return fmt.Errorf("malformed JWT header: %w", err)
	}

	var header struct {
		Alg string `json:"alg"`
	}
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return fmt.Errorf("malformed JWT header JSON: %w", err)
	}

	if !allowedSigningAlgs[header.Alg] {
		return fmt.Errorf("signing algorithm not supported: %q", header.Alg)
	}
	return nil
}
