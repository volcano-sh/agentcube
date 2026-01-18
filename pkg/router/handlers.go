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
	"errors"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"k8s.io/klog/v2"

	"github.com/volcano-sh/agentcube/pkg/common/types"
)

// handleHealthLive handles liveness probe
func (s *Server) handleHealthLive(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status": "alive",
	})
}

// handleHealthReady handles readiness probe
func (s *Server) handleHealthReady(c *gin.Context) {
	// Check if SessionManager is available
	if s.sessionManager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "not ready",
			"error":  "session manager not available",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "ready",
	})
}

// handleInvoke is a private helper function that handles invocation requests for both agents and code interpreters
func (s *Server) handleInvoke(c *gin.Context, namespace, name, path, kind string) {
	klog.Infof("%s invoke request: namespace=%s, name=%s, path=%s", kind, namespace, name, path)

	// Extract session ID from header (may be empty or incorrect)
	clientSessionID := c.GetHeader("x-agentcube-session-id")

	// Get sandbox info from session manager
	sandbox, err := s.sessionManager.GetSandboxBySession(c.Request.Context(), clientSessionID, namespace, name, kind)
	if err != nil {
		klog.Errorf("Failed to get sandbox info: %v, session id %s", err, clientSessionID)
		s.handleSandboxLookupError(c, err, clientSessionID, namespace, name, kind)
		return
	}

	if sandbox == nil {
		klog.Error("Session manager returned nil sandbox")
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "internal error",
			"code":  "INTERNAL_ERROR",
		})
		return
	}

	actualSessionID := sandbox.SessionID
	if actualSessionID == "" {
		klog.Errorf("Sandbox returned empty SessionID for %s/%s", namespace, name)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "internal error: empty session ID",
			"code":  "INTERNAL_ERROR",
		})
		return
	}

	// Extract endpoint from sandbox - find matching entry point by path
	endpoint, err := selectSandboxEndpoint(sandbox, path)
	if err != nil {
		klog.Warningf("Failed to select endpoint for sandbox %s: %v", sandbox.SandboxID, err)
		c.JSON(http.StatusNotFound, gin.H{
			"error": err.Error(),
			"code":  "Service not found",
		})
		return
	}

	klog.Infof("The selected entrypoint for session-id %s to sandbox is %s", actualSessionID, endpoint)

	if err := s.storeClient.UpdateSessionLastActivity(c.Request.Context(), actualSessionID, time.Now()); err != nil {
		klog.Warningf("Failed to update session activity for %s: %v", actualSessionID, err)
		// Best-effort — don't fail request
	}

	// Generate JWT token before setting up Director
	// Include session ID in claims for debugging and request tracking
	var jwtToken string
	if s.jwtManager != nil {
		claims := map[string]interface{}{
			"session_id": actualSessionID,
		}
		token, err := s.jwtManager.GenerateToken(claims)
		if err != nil {
			klog.Errorf("Failed to generate JWT token (session: %s): %v", actualSessionID, err)
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": "failed to sign request",
				"code":  "JWT_SIGNING_FAILED",
			})
			return
		}
		jwtToken = token
	}

	// Forward request to sandbox with session ID
	klog.Infof("Forwarding to sandbox: sessionID=%s namespace=%s name=%s path=%s endpoint=%s", sandbox.SessionID, namespace, name, path, endpoint)
	s.forwardToSandbox(c, endpoint, path, actualSessionID)

	if err := s.storeClient.UpdateSessionLastActivity(c.Request.Context(), actualSessionID, time.Now()); err != nil {
		klog.Warningf("Failed to update session activity (post-forward) for %s: %v", actualSessionID, err)
	}
}

func (s *Server) handleSandboxLookupError(c *gin.Context, err error, sessionID, namespace, name, kind string) {
	switch {
	case errors.Is(err, ErrSessionNotFound):
		c.JSON(http.StatusBadRequest, gin.H{
			"error": fmt.Sprintf("Invalid session id %s", sessionID),
			"code":  "BadRequest",
		})
	case errors.Is(err, ErrAgentRuntimeNotFound):
		c.JSON(http.StatusNotFound, gin.H{
			"error": fmt.Sprintf("%s '%s' not found in namespace '%s'", kind, name, namespace),
			"code":  "NotFound",
		})
	default:
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Invalid session or request",
			"code":  "BadRequest",
		})
	}
}

func selectSandboxEndpoint(sandbox *types.SandboxInfo, path string) (string, error) {
	// prefer matched entrypoint by path
	for _, ep := range sandbox.EntryPoints {
		if strings.HasPrefix(path, ep.Path) {
			return prependProtocol(ep.Protocol, ep.Endpoint), nil
		}
	}
	// fallback to first entrypoint
	if len(sandbox.EntryPoints) == 0 {
		return "", fmt.Errorf("no entry points found for sandbox")
	}
	ep := sandbox.EntryPoints[0]
	return prependProtocol(ep.Protocol, ep.Endpoint), nil
}

func prependProtocol(protocol, endpoint string) string {
	if protocol != "" && !strings.Contains(endpoint, "://") {
		return strings.ToLower(protocol) + "://" + endpoint
	}
	return endpoint
}

// handleAgentInvoke handles agent invocation requests
func (s *Server) handleAgentInvoke(c *gin.Context) {
	namespace := c.Param("namespace")
	name := c.Param("name")
	path := c.Param("path")
	s.handleInvoke(c, namespace, name, path, types.AgentRuntimeKind)
}

// handleCodeInterpreterInvoke handles code interpreter invocation requests
func (s *Server) handleCodeInterpreterInvoke(c *gin.Context) {
	namespace := c.Param("namespace")
	name := c.Param("name")
	path := c.Param("path")
	s.handleInvoke(c, namespace, name, path, types.CodeInterpreterKind)
}

// forwardToSandbox forwards the request to the specified sandbox endpoint
func (s *Server) forwardToSandbox(c *gin.Context, endpoint, path, sessionID string) {
	targetURL, err := url.Parse(endpoint)
	if err != nil {
		klog.Errorf("Invalid sandbox endpoint: %s (session: %s), error: %v", endpoint, sessionID, err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "internal server error",
			"code":  "INTERNAL_ERROR",
		})
		return
	}

	proxy := httputil.NewSingleHostReverseProxy(targetURL)
	proxy.Transport = s.httpTransport

	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)

		if path != "" && !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		req.URL.Path = path
		req.URL.RawPath = ""

		req.Host = targetURL.Host

		req.Header.Set("X-Forwarded-Host", c.Request.Host)
		req.Header.Set("X-Forwarded-Proto", "http")
		if c.Request.TLS != nil {
			req.Header.Set("X-Forwarded-Proto", "https")
		}

		clientIP := c.ClientIP()
		if prior, ok := req.Header["X-Forwarded-For"]; ok {
			clientIP = strings.Join(prior, ", ") + ", " + clientIP
		}
		req.Header.Set("X-Forwarded-For", clientIP)

		// Add JWT authorization header using pre-generated token
		if jwtToken := req.Header.Get("Authorization"); jwtToken == "" {
			if s.jwtManager != nil {
				claims := map[string]interface{}{"session_id": sessionID}
				token, err := s.jwtManager.GenerateToken(claims)
				if err == nil {
					req.Header.Set("Authorization", "Bearer "+token)
				}
			}
		}

		klog.Infof("Forwarding request to: %s%s (session: %s)", targetURL.String(), path, sessionID)
	}

	proxy.ErrorHandler = func(_ http.ResponseWriter, _ *http.Request, err error) {
		klog.Errorf("Proxy error (session: %s): %v", sessionID, err)

		// Determine error type and return appropriate response
		switch {
		case strings.Contains(err.Error(), "connection refused"):
			c.JSON(http.StatusBadGateway, gin.H{
				"error": "sandbox unreachable",
				"code":  "SANDBOX_UNREACHABLE",
			})
		case strings.Contains(err.Error(), "timeout"):
			c.JSON(http.StatusGatewayTimeout, gin.H{
				"error": "sandbox timeout",
				"code":  "SANDBOX_TIMEOUT",
			})
		default:
			c.JSON(http.StatusBadGateway, gin.H{
				"error": "sandbox unreachable",
				"code":  "SANDBOX_UNREACHABLE",
			})
		}
		c.Abort()
	}

	proxy.ModifyResponse = func(resp *http.Response) error {
		if sessionID != "" {
			resp.Header.Set("x-agentcube-session-id", sessionID)
		}
		return nil
	}

	proxy.ServeHTTP(c.Writer, c.Request)
}