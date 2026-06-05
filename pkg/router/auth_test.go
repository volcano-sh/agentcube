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
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func TestOIDCAuthMiddleware_Disabled(t *testing.T) {
	// When oidcValidator is nil, middleware should be a passthrough.
	server := &Server{oidcValidator: nil}

	w := httptest.NewRecorder()
	c, router := gin.CreateTestContext(w)
	router.Use(server.oidcAuthMiddleware())
	router.GET("/test", func(c *gin.Context) {
		claims := extractClaims(c)
		assert.Nil(t, claims, "no claims should be set when auth is disabled")
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	c.Request = httptest.NewRequest(http.MethodGet, "/test", nil)
	router.ServeHTTP(w, c.Request)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestOIDCAuthMiddleware_MissingHeader(t *testing.T) {
	server := &Server{oidcValidator: &OIDCValidator{}}

	w := httptest.NewRecorder()
	_, router := gin.CreateTestContext(w)
	router.Use(server.oidcAuthMiddleware())
	router.GET("/test", func(_ *gin.Context) {
		t.Error("handler should not be called")
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), "missing authorization header")
}

func TestRequireRole_HasRole(t *testing.T) {
	w := httptest.NewRecorder()
	_, router := gin.CreateTestContext(w)

	// Pre-set claims in context before the role check
	router.Use(func(c *gin.Context) {
		c.Set(string(contextKeyOIDCClaims), &Claims{
			Subject: "user-1",
			Roles:   []string{"sandbox:invoke", "sandbox:manage"},
		})
		c.Next()
	})
	router.Use(requireRole("sandbox:invoke"))
	router.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRequireRole_MissingRole(t *testing.T) {
	w := httptest.NewRecorder()
	_, router := gin.CreateTestContext(w)

	router.Use(func(c *gin.Context) {
		c.Set(string(contextKeyOIDCClaims), &Claims{
			Subject: "user-1",
			Roles:   []string{"sandbox:invoke"},
		})
		c.Next()
	})
	router.Use(requireRole("admin"))
	router.GET("/test", func(_ *gin.Context) {
		t.Error("handler should not be called")
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "insufficient permissions: missing role admin")
}
