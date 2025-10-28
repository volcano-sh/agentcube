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

type contextKey string

const (
	contextKeyUserToken          contextKey = "userToken"
	contextKeyServiceAccount     contextKey = "serviceAccount"
	contextKeyServiceAccountName contextKey = "serviceAccountName"
	contextKeyNamespace          contextKey = "namespace"
)

// authMiddleware provides service account token authentication middleware
func (s *Server) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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

		log.Printf("Authenticated request from service account: %s", serviceAccount)

		// Extract namespace from service account username
		// Format: system:serviceaccount:<namespace>:<serviceaccount-name>
		saParts := strings.Split(serviceAccount, ":")
		var namespace, serviceAccountName string
		if len(saParts) == 4 && saParts[0] == "system" && saParts[1] == "serviceaccount" {
			namespace = saParts[2]
			serviceAccountName = saParts[3]
		}

		// Store user information in request context
		ctx := context.WithValue(r.Context(), contextKeyUserToken, token)
		ctx = context.WithValue(ctx, contextKeyServiceAccount, serviceAccount)
		ctx = context.WithValue(ctx, contextKeyServiceAccountName, serviceAccountName)
		ctx = context.WithValue(ctx, contextKeyNamespace, namespace)

		// Token validation passed, continue processing request with updated context
		next(w, r.WithContext(ctx))
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
