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
	"net/http"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"
	"k8s.io/klog/v2"
)

// contextKeyType is a private type for context keys to avoid collisions.
type contextKeyType string

// contextKeyOIDCClaims is the context key for storing validated OIDC claims.
const contextKeyOIDCClaims = contextKeyType("oidcClaims")

// oidcAuthMiddleware validates incoming OIDC JWTs.
func (s *Server) oidcAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if s.oidcValidator == nil {
			c.Next()
			return
		}

		// Extract the Bearer token from the Authorization header.
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "missing authorization header",
				"code":  "UNAUTHORIZED",
			})
			c.Abort()
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "invalid authorization header format",
				"code":  "UNAUTHORIZED",
			})
			c.Abort()
			return
		}

		rawToken := strings.TrimSpace(parts[1])
		if rawToken == "" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "empty bearer token",
				"code":  "UNAUTHORIZED",
			})
			c.Abort()
			return
		}

		// Validate the token against the OIDC provider.
		claims, err := s.oidcValidator.ValidateToken(c.Request.Context(), rawToken)
		if err != nil {
			klog.V(2).Infof("OIDC token validation failed: %v", err)
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "invalid or expired token",
				"code":  "UNAUTHORIZED",
			})
			c.Abort()
			return
		}

		c.Set(string(contextKeyOIDCClaims), claims)

		// Store in request context for downstream code
		ctx := context.WithValue(c.Request.Context(), contextKeyOIDCClaims, claims)
		c.Request = c.Request.WithContext(ctx)

		c.Next()
	}
}

// requireRole enforces a specific OIDC role for the endpoint.
func requireRole(role string) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims := extractClaims(c)
		if claims == nil {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "authentication required",
				"code":  "UNAUTHORIZED",
			})
			c.Abort()
			return
		}

		if !slices.Contains(claims.Roles, role) {
			klog.V(2).Infof("RBAC check failed: user %s missing required role %q (has: %v)",
				claims.Subject, role, claims.Roles)
			c.JSON(http.StatusForbidden, gin.H{
				"error": "insufficient permissions: missing role " + role,
				"code":  "FORBIDDEN",
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

// extractClaims extracts validated OIDC claims from the Gin context.
func extractClaims(c *gin.Context) *Claims {
	val, exists := c.Get(string(contextKeyOIDCClaims))
	if !exists {
		return nil
	}
	claims, ok := val.(*Claims)
	if !ok {
		return nil
	}
	return claims
}
