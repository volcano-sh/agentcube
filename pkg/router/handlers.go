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

	// Extract session ID from header (may be empty or incorrect)
	clientSessionID := c.GetHeader("x-agentcube-session-id")

	// Get sandbox info from session manager
	sandbox, err := s.sessionManager.GetSandboxBySession(c.Request.Context(), clientSessionID, namespace, name, kind)
	if err != nil {
		klog.Errorf("Failed to get sandbox info: %v, session id %s", err, clientSessionID)
		c.JSON(http.StatusBadRequest, gin.H{
			"error": fmt.Sprintf("Invalid session id %s", clientSessionID),
			"code":  "BadRequest",
		})
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
	var endpoint string
	for _, ep := range sandbox.EntryPoints {
		if strings.HasPrefix(path, ep.Path) {
			if ep.Protocol != "" && !strings.Contains(ep.Endpoint, "://") {
				endpoint = strings.ToLower(ep.Protocol) + "://" + ep.Endpoint
			} else {
				endpoint = ep.Endpoint
			}
			break
		}
	}

	// Fallback to first entry point
	if endpoint == "" {
		if len(sandbox.EntryPoints) == 0 {
			klog.Warningf("No entry points found for sandbox: %s", sandbox.SandboxID)
			c.JSON(http.StatusNotFound, gin.H{
				"error": "no entry points found for sandbox",
				"code":  "ServiceNotFound",
			})
			return
		}
		ep := sandbox.EntryPoints[0]
		if ep.Protocol != "" && !strings.Contains(ep.Endpoint, "://") {
			endpoint = strings.ToLower(ep.Protocol) + "://" + ep.Endpoint
		} else {
			endpoint = ep.Endpoint
		}
	}

	klog.Infof("The selected entrypoint for session-id %s to sandbox is %s", actualSessionID, endpoint)

	if err := s.storeClient.UpdateSessionLastActivity(c.Request.Context(), actualSessionID, time.Now()); err != nil {
		klog.Warningf("Failed to update session activity for %s: %v", actualSessionID, err)
		// Best-effort — don't fail request
	}

	// Forward request using actualSessionID
	s.forwardToSandbox(c, endpoint, path, actualSessionID)

	if err := s.storeClient.UpdateSessionLastActivity(c.Request.Context(), actualSessionID, time.Now()); err != nil {
		klog.Warningf("Failed to update session activity (post-forward) for %s: %v", actualSessionID, err)
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
	targetURL, err := url.Parse(endpoint)
	if err != nil {
		klog.Errorf("Invalid sandbox endpoint: %s, error: %v", endpoint, err)
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

		req.Header.Set("x-agentcube-session-id", sessionID)

		klog.Infof("Forwarding request to: %s%s", targetURL.String(), path)
	}

	proxy.ErrorHandler = func(_ http.ResponseWriter, _ *http.Request, err error) {
		klog.Errorf("Proxy error: %v", err)
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