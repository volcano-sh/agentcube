package router

import (
	"context"
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

// handleAgentInvoke handles agent invocation requests
func (s *Server) handleAgentInvoke(c *gin.Context) {
	agentNamespace := c.Param("agentNamespace")
	agentName := c.Param("agentName")
	path := c.Param("path")

	log.Printf("Agent invoke request: namespace=%s, agent=%s, path=%s", agentNamespace, agentName, path)

	// Extract session ID from header
	sessionID := c.GetHeader("x-agentcube-session-id")

	// Get sandbox info from session manager
	endpoint, newSessionID, err := s.sessionManager.GetSandboxInfoBySessionId(sessionID, agentNamespace, agentName, KindAgent)
	if err != nil {
		log.Printf("Failed to get sandbox info: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "internal server error",
			"code":  "INTERNAL_ERROR",
		})
		return
	}

	// Update session activity in Redis when receiving request
	if newSessionID != "" {
		if err := s.redisManager.UpdateSessionActivity(newSessionID); err != nil {
			log.Printf("Failed to update session activity for request: %v", err)
		}
	}

	// Forward request to sandbox with session ID
	s.forwardToSandbox(c, endpoint, path, newSessionID)
}

// handleCodeInterpreterInvoke handles code interpreter invocation requests
func (s *Server) handleCodeInterpreterInvoke(c *gin.Context) {
	namespace := c.Param("namespace")
	name := c.Param("name")
	path := c.Param("path")

	log.Printf("Code interpreter invoke request: namespace=%s, name=%s, path=%s", namespace, name, path)

	// Extract session ID from header
	sessionID := c.GetHeader("x-agentcube-session-id")

	// Get sandbox info from session manager
	endpoint, newSessionID, err := s.sessionManager.GetSandboxInfoBySessionId(sessionID, namespace, name, KindCodeInterpreter)
	if err != nil {
		log.Printf("Failed to get sandbox info: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "internal server error",
			"code":  "INTERNAL_ERROR",
		})
		return
	}

	// Update session activity in Redis when receiving request
	if newSessionID != "" {
		if err := s.redisManager.UpdateSessionActivity(newSessionID); err != nil {
			log.Printf("Failed to update session activity for request: %v", err)
		}
	}

	// Forward request to sandbox with session ID
	s.forwardToSandbox(c, endpoint, path, newSessionID)
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

	// Create reverse proxy with optimized transport
	proxy := httputil.NewSingleHostReverseProxy(targetURL)

	// Configure HTTP transport for better concurrency
	proxy.Transport = &http.Transport{
		MaxIdleConns:        s.config.MaxIdleConns,
		MaxIdleConnsPerHost: s.config.MaxConnsPerHost,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  false,
		ForceAttemptHTTP2:   true,
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

			// Update session activity in Redis when returning response
			if err := s.redisManager.UpdateSessionActivity(sessionID); err != nil {
				log.Printf("Failed to update session activity for response: %v", err)
			}
		}
		return nil
	}

	// Set timeout for the proxy request using configured timeout
	ctx, cancel := context.WithTimeout(c.Request.Context(), time.Duration(s.config.RequestTimeout)*time.Second)
	defer cancel()
	c.Request = c.Request.WithContext(ctx)

	// Use the proxy to serve the request
	proxy.ServeHTTP(c.Writer, c.Request)
}
