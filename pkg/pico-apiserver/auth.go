package picoapiserver

import (
	"net/http"
	"strings"
)

// authMiddleware provides simple authentication middleware
func (s *Server) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// If no JWT secret is configured, skip validation (development only)
		if s.config.JWTSecret == "" {
			next(w, r)
			return
		}

		// Extract token from Authorization header
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			respondError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Missing authorization header")
			return
		}

		// Check if it's a Bearer token
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			respondError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid authorization header format")
			return
		}

		token := parts[1]

		// TODO: Implement actual JWT validation
		// This should use jwt-go or similar library to validate the token
		// Verify signature, expiration, claims, etc.
		if !s.validateToken(token) {
			respondError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid or expired token")
			return
		}

		// Token validation passed, continue processing request
		next(w, r)
	}
}

// validateToken validates JWT token (placeholder implementation)
func (s *Server) validateToken(token string) bool {
	// TODO: Implement real JWT validation
	// 1. Parse token
	// 2. Verify signature
	// 3. Check expiration
	// 4. Validate other claims

	// Temporary implementation: if no secret is configured, accept all tokens
	if s.config.JWTSecret == "" {
		return true
	}

	// Simple check: token is not empty
	return token != ""
}
