package workloadmanager

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	authv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Authorization Model for API Server Endpoints
//
// The API server implements a role-based access control (RBAC) model:
// - All users are service accounts that can only create and manage their own sandboxes
// - Each service account can only access sandboxes it created
// - No service account has administrative privileges
//
// Authentication:
// - All endpoints (except /health) require a valid Kubernetes service account token
//   in the Authorization header as a Bearer token
// - Tokens are validated using the Kubernetes TokenReview API
// - User identity is extracted from the service account username format:
//   system:serviceaccount:<namespace>:<serviceaccount-name>
//
// Authorization by Endpoint:
//
// POST /v1/sandboxes (CreateSandbox):
//   - Any authenticated user can create sandboxes
//   - The creator's service account name is stored in the sandbox metadata
//
// GET /v1/sandboxes (ListSandboxes):
//   - Users can only list sandboxes they created
//   - Results are filtered based on CreatorServiceAccount field
//
// GET /v1/sandboxes/{sandboxId} (GetSandbox):
//   - Users can only access sandboxes they created
//   - Access is checked via checkSandboxAccess() function
//
// DELETE /v1/sandboxes/{sandboxId} (DeleteSandbox):
//   - Users can only delete sandboxes they created
//   - Access is checked via checkSandboxAccess() function
//
// CONNECT /v1/sandboxes/{sandboxId} (Tunnel):
//   - Users can only establish tunnels to sandboxes they created
//   - Access is checked via checkSandboxAccess() function
//
// GET /health (HealthCheck):
//   - No authentication required (public endpoint)

type contextKey string

const (
	contextKeyUserToken          contextKey = "userToken"
	contextKeyServiceAccount     contextKey = "serviceAccount"
	contextKeyServiceAccountName contextKey = "serviceAccountName"
	contextKeyNamespace          contextKey = "namespace"
)

// authMiddleware provides service account token authentication middleware
func (s *Server) authMiddleware(c *gin.Context) {
	if !s.enableAuth {
		c.Next()
		return
	}
	// Skip authentication for health check endpoint
	if c.Request.URL.Path == "/health" {
		c.Next()
		return
	}
	// Extract token from Authorization header
	authHeader := c.GetHeader("Authorization")
	if authHeader == "" {
		respondError(c, http.StatusUnauthorized, "UNAUTHORIZED", "Missing authorization header")
		c.Abort()
		return
	}

	// Check if it's a Bearer token
	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || parts[0] != "Bearer" {
		respondError(c, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid authorization header format")
		c.Abort()
		return
	}

	token := parts[1]

	// Validate token using Kubernetes TokenReview API
	authenticated, serviceAccount, err := s.validateServiceAccountToken(c.Request.Context(), token)
	if err != nil {
		log.Printf("Token validation error: %v", err)
		respondError(c, http.StatusUnauthorized, "UNAUTHORIZED", "Token validation failed")
		c.Abort()
		return
	}

	if !authenticated {
		respondError(c, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid or expired token")
		c.Abort()
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
	} else {
		respondError(c, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid service account format")
		c.Abort()
		return
	}

	// Store user information in request context
	ctx := context.WithValue(c.Request.Context(), contextKeyUserToken, token)
	ctx = context.WithValue(ctx, contextKeyServiceAccount, serviceAccount)
	ctx = context.WithValue(ctx, contextKeyServiceAccountName, serviceAccountName)
	ctx = context.WithValue(ctx, contextKeyNamespace, namespace)

	// Update request context
	c.Request = c.Request.WithContext(ctx)

	// Token validation passed, continue processing request
	c.Next()
}

// validateServiceAccountToken validates a service account token using Kubernetes TokenReview API
// Uses LRU cache to avoid repeated API calls for the same token
func (s *Server) validateServiceAccountToken(ctx context.Context, token string) (bool, string, error) {
	// Check cache first
	if found, authenticated, username := s.tokenCache.Get(token); found {
		if authenticated {
			return true, username, nil
		}
		// Token found in cache but not authenticated
		return false, "", nil
	}

	// Cache miss, call Kubernetes API
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

	// Extract service account information from username
	// Kubernetes service account username format: system:serviceaccount:<namespace>:<serviceaccount-name>
	username := ""
	if result.Status.Authenticated {
		username = result.Status.User.Username
	}

	// Cache the result (both authenticated and non-authenticated tokens)
	s.tokenCache.Set(token, result.Status.Authenticated, username)

	if !result.Status.Authenticated {
		return false, "", nil
	}

	return true, username, nil
}

// extractUserInfo extracts user information from request context
// Returns userToken, userNamespace, serviceAccount, serviceAccountName
// If extraction fails, returns empty strings
func extractUserInfo(c *gin.Context) (userToken, userNamespace, serviceAccount, serviceAccountName string) {
	userToken, _ = c.Request.Context().Value(contextKeyUserToken).(string)
	userNamespace, _ = c.Request.Context().Value(contextKeyNamespace).(string)
	serviceAccount, _ = c.Request.Context().Value(contextKeyServiceAccount).(string)
	serviceAccountName, _ = c.Request.Context().Value(contextKeyServiceAccountName).(string)
	return
}
