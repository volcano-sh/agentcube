package router

import (
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// handleHealth handles health check requests
func (s *Server) handleHealth(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status": "healthy",
	})
}

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
	log.Printf("%s invoke request: namespace=%s, name=%s, path=%s", kind, namespace, name, path)

	// Extract session ID from header
	sessionID := c.GetHeader("x-agentcube-session-id")

	// Get sandbox info from session manager
	sandbox, err := s.sessionManager.GetSandboxBySession(c.Request.Context(), sessionID, namespace, name, kind)
	if err != nil {
		log.Printf("Failed to get sandbox info: %v, session id %s", err, sessionID)
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
			log.Printf("No entry points found for sandbox: %s", sandbox.SandboxID)
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": "internal server error",
				"code":  "INTERNAL_ERROR",
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

	log.Printf("The selected entrypoint for session-id %s to sandbox is %s", sandbox.SessionID, endpoint)

	// Update session activity in Redis when receiving request
	if sandbox.SessionID != "" && sandbox.SandboxID != "" {
		if err := s.redisClient.UpdateSessionLastActivity(c.Request.Context(), sandbox.SessionID, time.Now()); err != nil {
			log.Printf("Failed to update sandbox last activity for request: %v", err)
		}
	}

	// Forward request to sandbox with session ID
	s.forwardToSandbox(c, endpoint, path, sandbox.SessionID)

	if err := s.redisClient.UpdateSessionLastActivity(c.Request.Context(), sandbox.SessionID, time.Now()); err != nil {
		log.Printf("Failed to update sandbox last activity for request: %v", err)
	}
}

// handleAgentInvoke handles agent invocation requests
func (s *Server) handleAgentInvoke(c *gin.Context) {
	namespace := c.Param("namespace")
	name := c.Param("name")
	path := c.Param("path")
	s.handleInvoke(c, namespace, name, path, "AgentRuntime")
}

// handleCodeInterpreterInvoke handles code interpreter invocation requests
func (s *Server) handleCodeInterpreterInvoke(c *gin.Context) {
	namespace := c.Param("namespace")
	name := c.Param("name")
	path := c.Param("path")
	s.handleInvoke(c, namespace, name, path, "CodeInterpreter")
}

// forwardToSandbox forwards the request to the specified sandbox endpoint
func (s *Server) forwardToSandbox(c *gin.Context, endpoint, path, sessionID string) {
	// Parse the target URL
	targetURL, err := url.Parse(endpoint)
	if err != nil {
		log.Printf("Invalid sandbox endpoint: %s, error: %v", endpoint, err)
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

		log.Printf("Forwarding request to: %s%s", targetURL.String(), path)
	}

	// Customize error handler
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Printf("Proxy error: %v", err)

		// Determine error type and return appropriate response
		if strings.Contains(err.Error(), "connection refused") {
			c.JSON(http.StatusBadGateway, gin.H{
				"error": "sandbox unreachable",
				"code":  "SANDBOX_UNREACHABLE",
			})
		} else if strings.Contains(err.Error(), "timeout") {
			c.JSON(http.StatusGatewayTimeout, gin.H{
				"error": "sandbox timeout",
				"code":  "SANDBOX_TIMEOUT",
			})
		} else {
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
