package picoapiserver

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"

	authv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// authMiddleware provides service account token authentication middleware
func (s *Server) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// If authentication is disabled, skip validation (development only)
		if s.config.DisableAuth {
			log.Printf("WARNING: Authentication is disabled - allowing unauthenticated request")
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

		// Validate token using Kubernetes TokenReview API
		authenticated, serviceAccount, err := s.validateServiceAccountToken(r.Context(), token)
		if err != nil {
			log.Printf("Token validation error: %v", err)
			respondError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Token validation failed")
			return
		}

		if !authenticated {
			respondError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid or expired token")
			return
		}

		// Verify the token is from the expected service account
		if !s.isAuthorizedServiceAccount(serviceAccount) {
			log.Printf("Unauthorized service account: %s", serviceAccount)
			respondError(w, http.StatusForbidden, "FORBIDDEN", "Service account not authorized")
			return
		}

		log.Printf("Authenticated request from service account: %s", serviceAccount)

		// Token validation passed, continue processing request
		next(w, r)
	}
}

// validateServiceAccountToken validates a service account token using Kubernetes TokenReview API
func (s *Server) validateServiceAccountToken(ctx context.Context, token string) (bool, string, error) {
	// Create TokenReview request
	tokenReview := &authv1.TokenReview{
		Spec: authv1.TokenReviewSpec{
			Token: token,
		},
	}

	// Call Kubernetes API to review the token
	result, err := s.k8sClient.clientset.AuthenticationV1().TokenReviews().Create(
		ctx,
		tokenReview,
		metav1.CreateOptions{},
	)
	if err != nil {
		return false, "", fmt.Errorf("failed to review token: %w", err)
	}

	// Check if token is authenticated
	if !result.Status.Authenticated {
		return false, "", nil
	}

	// Extract service account information from username
	// Kubernetes service account username format: system:serviceaccount:<namespace>:<serviceaccount-name>
	username := result.Status.User.Username
	return true, username, nil
}

// isAuthorizedServiceAccount checks if the service account is authorized
func (s *Server) isAuthorizedServiceAccount(username string) bool {
	// Allow tokens from pico-apiserver service account in the same namespace
	expectedPrefix := fmt.Sprintf("system:serviceaccount:%s:pico-apiserver", s.config.Namespace)

	// Also allow tokens from pico-apiserver service account in pico namespace (for backward compatibility)
	picoNamespacePrefix := "system:serviceaccount:pico:pico-apiserver"

	return strings.HasPrefix(username, expectedPrefix) || strings.HasPrefix(username, picoNamespacePrefix)
}
