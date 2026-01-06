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

	// Extract session ID from header
	sessionID := c.GetHeader("x-agentcube-session-id")

	// Get sandbox info from session manager
	sandbox, err := s.sessionManager.GetSandboxBySession(c.Request.Context(), sessionID, namespace, name, kind)
	if err != nil {
		klog.Errorf("Failed to get sandbox info: %v, session id %s", err, sessionID)
		c.JSON(http.StatusBadRequest, gin.H{
			"error": fmt.Sprintf("Invalid session id %s", sessionID),
			"code":  "BadRequest",
		})
		return
	}

	// Extract endpoint from sandbox - find matching entry point by path
	var endpoint string
	for _, ep := range sandbox.EntryPoints {
		if strings.HasPrefix(path, ep.Path) {
			// Only add protocol if not already present
			if ep.Protocol != "" && !strings.Contains(ep.Endpoint, "://") {
				endpoint = strings.ToLower(ep.Protocol) + "://" + ep.Endpoint
			} else {
				endpoint = ep.Endpoint
			}
			break
		}
	}

	// If no matching endpoint found, use the first one as fallback
	if endpoint == "" {
		if len(sandbox.EntryPoints) == 0 {
			klog.Warningf("No entry points found for sandbox: %s", sandbox.SandboxID)
			c.JSON(http.StatusNotFound, gin.H{
				"error": "no entry points found for sandbox",
				"code":  "Service not found",
			})
			return
		}
		// Only add protocol if not already present
		if sandbox.EntryPoints[0].Protocol != "" && !strings.Contains(sandbox.EntryPoints[0].Endpoint, "://") {
			endpoint = strings.ToLower(sandbox.EntryPoints[0].Protocol) + "://" + sandbox.EntryPoints[0].Endpoint
		} else {
			endpoint = sandbox.EntryPoints[0].Endpoint
		}
	}

	klog.Infof("The selected entrypoint for session-id %s to sandbox is %s", sandbox.SessionID, endpoint)

	// Update session activity in store when receiving request
	if sandbox.SessionID != "" && sandbox.SandboxID != "" {
		if err := s.storeClient.UpdateSessionLastActivity(c.Request.Context(), sandbox.SessionID, time.Now()); err != nil {
			klog.Warningf("Failed to update sandbox with session-id %s last activity for request: %v", sandbox.SessionID, err)
		}
	}

	// Forward request to sandbox with session ID
	s.forwardToSandbox(c, endpoint, path, sandbox.SessionID)

	if err := s.storeClient.UpdateSessionLastActivity(c.Request.Context(), sandbox.SessionID, time.Now()); err != nil {
		klog.Warningf("Failed to update sandbox with session-id %s last activity for request: %v", sandbox.SessionID, err)
	}
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
	// Parse the target URL
	targetURL, err := url.Parse(endpoint)
	if err != nil {
		klog.Errorf("Invalid sandbox endpoint: %s (session: %s), error: %v", endpoint, sessionID, err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "internal server error",
			"code":  "INTERNAL_ERROR",
		})
		return
	}

	// Create reverse proxy with reusable transport
	proxy := httputil.NewSingleHostReverseProxy(targetURL)

	// Use the shared HTTP transport for connection pooling
	proxy.Transport = s.httpTransport

	// Generate JWT token before setting up Director
	// Include session ID in claims for debugging and request tracking
	var jwtToken string
	if s.jwtManager != nil {
		claims := map[string]interface{}{
			"session_id": sessionID,
		}
		token, err := s.jwtManager.GenerateToken(claims)
		if err != nil {
			klog.Errorf("Failed to generate JWT token (session: %s): %v", sessionID, err)
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": "failed to sign request",
				"code":  "JWT_SIGNING_FAILED",
			})
			return
		}
		jwtToken = token
	}

	// Customize the director to modify the request
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)

		// Set the target path
		if path != "" && !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		req.URL.Path = path
		req.URL.RawPath = ""

		// Set the host header
		req.Host = targetURL.Host

		// Add forwarding headers
		req.Header.Set("X-Forwarded-Host", c.Request.Host)
		req.Header.Set("X-Forwarded-Proto", "http")
		if c.Request.TLS != nil {
			req.Header.Set("X-Forwarded-Proto", "https")
		}

		// Set X-Forwarded-For to preserve original client IP
		clientIP := c.ClientIP()
		if prior, ok := req.Header["X-Forwarded-For"]; ok {
			clientIP = strings.Join(prior, ", ") + ", " + clientIP
		}
		req.Header.Set("X-Forwarded-For", clientIP)

		// Add JWT authorization header using pre-generated token
		if jwtToken != "" {
			req.Header.Set("Authorization", "Bearer "+jwtToken)
		}

		klog.Infof("Forwarding request to: %s%s (session: %s)", targetURL.String(), path, sessionID)
	}

	// Customize error handler
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
	}

	// Modify response
	proxy.ModifyResponse = func(resp *http.Response) error {
		// Always set session ID in response header
		if sessionID != "" {
			resp.Header.Set("x-agentcube-session-id", sessionID)
		}
		return nil
	}

	// No timeout for invoke requests to allow long-running operations
	// ctx, cancel := context.WithTimeout(c.Request.Context(), time.Duration(s.config.RequestTimeout)*time.Second)
	// defer cancel()
	// c.Request = c.Request.WithContext(ctx)

	// Use the proxy to serve the request
	proxy.ServeHTTP(c.Writer, c.Request)
}
