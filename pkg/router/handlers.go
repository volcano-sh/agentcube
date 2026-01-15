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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
	klog.V(4).Infof("%s invoke request: namespace=%s, name=%s, path=%s", kind, namespace, name, path)

	// Extract session ID from header
	sessionID := c.GetHeader("x-agentcube-session-id")

	// Get sandbox info from session manager
	sandbox, err := s.sessionManager.GetSandboxBySession(c.Request.Context(), sessionID, namespace, name, kind)
	if err != nil {
		klog.Errorf("Failed to get or create sandbox info: %v, session id %s", err, sessionID)
		s.handleGetSandboxError(c, err)
		return
	}

	// Update session activity in store when receiving request
	if err := s.storeClient.UpdateSessionLastActivity(c.Request.Context(), sandbox.SessionID, time.Now()); err != nil {
		klog.Warningf("Failed to update sandbox with session-id %s last activity for request: %v", sandbox.SessionID, err)
	}

	// Forward request to sandbox with session ID
	klog.V(2).Infof("Forwarding to sandbox: sessionID=%s namespace=%s name=%s path=%s", sandbox.SessionID, namespace, name, path)
	s.forwardToSandbox(c, sandbox, path)

	if err := s.storeClient.UpdateSessionLastActivity(c.Request.Context(), sandbox.SessionID, time.Now()); err != nil {
		klog.Warningf("Failed to update sandbox with session-id %s last activity for request: %v", sandbox.SessionID, err)
	}
}

func (s *Server) handleGetSandboxError(c *gin.Context, err error) {
	// Fallback for other APIStatus errors
	if statusErr, ok := err.(apierrors.APIStatus); ok {
		code := http.StatusInternalServerError
		if statusErr.Status().Code != 0 {
			code = int(statusErr.Status().Code)
		}
		message := statusErr.Status().Message
		if message == "" {
			message = err.Error()
		}
		if code == http.StatusInternalServerError {
			message = "internal server error"
		}
		c.JSON(code, gin.H{"error": message})
		return
	}

	// Default internal error
	c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
}

func selectSandboxUrl(sandbox *types.SandboxInfo, path string) (*url.URL, error) {
	// prefer matched entrypoint by path
	for _, ep := range sandbox.EntryPoints {
		if strings.HasPrefix(path, ep.Path) {
			return buildURL(ep.Protocol, ep.Endpoint), nil
		}
	}
	// fallback to first entrypoint
	if len(sandbox.EntryPoints) == 0 {
		return nil, fmt.Errorf("no entry point found for sandbox")
	}
	ep := sandbox.EntryPoints[0]
	return buildURL(ep.Protocol, ep.Endpoint), nil
}

func buildURL(protocol, endpoint string) *url.URL {
	if protocol != "" && !strings.Contains(endpoint, "://") {
		endpoint = (strings.ToLower(protocol) + "://" + endpoint)
	}
	url, _ := url.Parse(endpoint)
	return url
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
func (s *Server) forwardToSandbox(c *gin.Context, sandbox *types.SandboxInfo, path string) {
	// Extract url from sandbox - find matching entry point by path
	targetURL, err := selectSandboxUrl(sandbox, path)
	if err != nil {
		klog.Errorf("Failed to get sandbox access address %s: %v", sandbox.SandboxID, err)
		c.JSON(http.StatusNotFound, gin.H{
			"error": err.Error(),
		})
		return
	}

	// Create reverse proxy with reusable transport
	proxy := httputil.NewSingleHostReverseProxy(targetURL)

	// Use the shared HTTP transport for connection pooling
	proxy.Transport = s.httpTransport

	var jwtToken string
	if sandbox.Kind == types.CodeInterpreterKind {
		// Generate JWT token before setting up Director
		// Include session ID in claims for debugging and request tracking
		if s.jwtManager != nil {
			claims := map[string]interface{}{
				"session_id": sandbox.SessionID,
			}
			token, err := s.jwtManager.GenerateToken(claims)
			if err != nil {
				klog.Errorf("Failed to generate JWT token (session: %s): %v", sandbox.SessionID, err)
				c.JSON(http.StatusInternalServerError, gin.H{
					"error": "failed to sign request",
					"code":  "JWT_SIGNING_FAILED",
				})
				return
			}
			jwtToken = token
		}
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

		klog.Infof("Forwarding request to: %s%s (session: %s)", targetURL.String(), path, sandbox.SessionID)
	}

	// Customize error handler
	proxy.ErrorHandler = func(_ http.ResponseWriter, _ *http.Request, err error) {
		klog.Errorf("Proxy error (session: %s): %v", sandbox.SessionID, err)

		// Determine error type and return appropriate response
		switch {
		case strings.Contains(err.Error(), "connection refused"):
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"error": "sandbox unreachable",
			})
		case strings.Contains(err.Error(), "timeout"):
			c.JSON(http.StatusGatewayTimeout, gin.H{
				"error": "sandbox timeout",
			})
		default:
			c.JSON(http.StatusBadGateway, gin.H{
				"error": "sandbox unreachable",
			})
		}
	}

	// Modify response
	proxy.ModifyResponse = func(resp *http.Response) error {
		// Always set session ID in response header
		resp.Header.Set("x-agentcube-session-id", sandbox.SessionID)
		return nil
	}

	// No timeout for invoke requests to allow long-running operations
	// ctx, cancel := context.WithTimeout(c.Request.Context(), time.Duration(s.config.RequestTimeout)*time.Second)
	// defer cancel()
	// c.Request = c.Request.WithContext(ctx)

	// Use the proxy to serve the request
	proxy.ServeHTTP(c.Writer, c.Request)
}
