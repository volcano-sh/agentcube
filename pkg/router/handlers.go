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
	"fmt"
	"net"
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

func determineUpstreamURL(sandbox *types.SandboxInfo, path string) (*url.URL, error) {
	// prefer matched entrypoint by path
	for _, ep := range sandbox.EntryPoints {
		if strings.HasPrefix(path, ep.Path) {
			return buildURL(ep.Protocol, ep.Endpoint)
		}
	}
	// fallback to first entrypoint
	if len(sandbox.EntryPoints) == 0 {
		return nil, fmt.Errorf("no entry point found for sandbox")
	}
	ep := sandbox.EntryPoints[0]
	return buildURL(ep.Protocol, ep.Endpoint)
}

func buildURL(protocol, endpoint string) (*url.URL, error) {
	if protocol != "" && !strings.Contains(endpoint, "://") {
		endpoint = (strings.ToLower(protocol) + "://" + endpoint)
	}
	parsedURL, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid upstream endpoint %q: %w", endpoint, err)
	}
	if parsedURL.Host == "" {
		return nil, fmt.Errorf("invalid upstream endpoint %q: missing host", endpoint)
	}
	return parsedURL, nil
}

func connectionRefusedRetryable(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "connection refused")
}

func preflightTargetAddress(targetURL *url.URL) string {
	if targetURL == nil {
		return ""
	}
	if targetURL.Host == "" {
		return ""
	}
	if _, _, err := net.SplitHostPort(targetURL.Host); err == nil {
		return targetURL.Host
	}
	switch strings.ToLower(targetURL.Scheme) {
	case "https":
		return net.JoinHostPort(targetURL.Host, "443")
	default:
		return net.JoinHostPort(targetURL.Host, "80")
	}
}

func (s *Server) waitForUpstreamReachable(ctx context.Context, targetURL *url.URL) error {
	address := preflightTargetAddress(targetURL)
	if address == "" {
		return nil
	}

	retryCount := 0
	retryInterval := 200 * time.Millisecond
	if s != nil && s.config != nil {
		retryCount = s.config.InitialConnectRetryCount
		if s.config.InitialConnectRetryInterval > 0 {
			retryInterval = s.config.InitialConnectRetryInterval
		}
	}

	var lastErr error
	for attempt := 0; attempt <= retryCount; attempt++ {
		dialer := &net.Dialer{Timeout: retryInterval}
		conn, err := dialer.DialContext(ctx, "tcp", address)
		if err == nil {
			if closeErr := conn.Close(); closeErr != nil {
				klog.V(4).Infof("closing preflight connection to %s failed: %v", address, closeErr)
			}
			return nil
		}
		lastErr = err
		if !connectionRefusedRetryable(err) || attempt == retryCount {
			break
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(retryInterval):
		}
	}

	if lastErr != nil {
		return fmt.Errorf("sandbox preflight connect failed: %w", lastErr)
	}
	return nil
}

func upstreamUnavailableResponse(err error) (int, gin.H) {
	errText := strings.ToLower(err.Error())
	switch {
	case connectionRefusedRetryable(err):
		return http.StatusBadGateway, gin.H{"error": "sandbox unreachable"}
	case strings.Contains(errText, "deadline exceeded") || strings.Contains(errText, "timeout"):
		return http.StatusGatewayTimeout, gin.H{"error": "sandbox timeout"}
	default:
		return http.StatusServiceUnavailable, gin.H{"error": "sandbox unreachable"}
	}
}

func (s *Server) resolveSandboxTarget(c *gin.Context, sandbox *types.SandboxInfo, path string) (*url.URL, bool) {
	targetURL, err := determineUpstreamURL(sandbox, path)
	if err != nil {
		klog.Errorf("Failed to get sandbox access address %s: %v", sandbox.SandboxID, err)
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return nil, false
	}

	if err := s.waitForUpstreamReachable(c.Request.Context(), targetURL); err != nil {
		klog.Errorf("Sandbox preflight failed (session: %s): %v", sandbox.SessionID, err)
		statusCode, response := upstreamUnavailableResponse(err)
		c.JSON(statusCode, response)
		return nil, false
	}

	return targetURL, true
}

func (s *Server) generateSandboxJWT(c *gin.Context, sandbox *types.SandboxInfo) (string, bool) {
	if sandbox.Kind != types.SandboxClaimsKind && sandbox.Kind != types.SandboxKind {
		return "", true
	}
	if s.jwtManager == nil {
		return "", true
	}

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
		return "", false
	}

	return token, true
}

func normalizedProxyPath(path string) string {
	if path != "" && !strings.HasPrefix(path, "/") {
		return "/" + path
	}
	return path
}

func forwardedClientIP(req *http.Request, clientIP string) string {
	if prior, ok := req.Header["X-Forwarded-For"]; ok {
		return strings.Join(prior, ", ") + ", " + clientIP
	}
	return clientIP
}

func configureProxyDirector(proxy *httputil.ReverseProxy, c *gin.Context, targetURL *url.URL, path, jwtToken, sessionID string) {
	proxyPath := normalizedProxyPath(path)
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)

		req.URL.Path = proxyPath
		req.URL.RawPath = ""
		req.Host = targetURL.Host
		req.Header.Set("X-Forwarded-Host", c.Request.Host)
		req.Header.Set("X-Forwarded-Proto", "http")
		if c.Request.TLS != nil {
			req.Header.Set("X-Forwarded-Proto", "https")
		}
		req.Header.Set("X-Forwarded-For", forwardedClientIP(req, c.ClientIP()))
		if jwtToken != "" {
			req.Header.Set("Authorization", "Bearer "+jwtToken)
		}

		klog.Infof("Forwarding request to: %s%s (session: %s)", targetURL.String(), proxyPath, sessionID)
	}
}

func configureProxyErrorHandler(proxy *httputil.ReverseProxy, c *gin.Context, sessionID string) {
	proxy.ErrorHandler = func(_ http.ResponseWriter, _ *http.Request, err error) {
		klog.Errorf("Proxy error (session: %s): %v", sessionID, err)
		statusCode, response := upstreamUnavailableResponse(err)
		c.JSON(statusCode, response)
	}
}

func configureProxyResponse(proxy *httputil.ReverseProxy, sessionID string) {
	proxy.ModifyResponse = func(resp *http.Response) error {
		resp.Header.Set("x-agentcube-session-id", sessionID)
		return nil
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
func (s *Server) forwardToSandbox(c *gin.Context, sandbox *types.SandboxInfo, path string) {
	targetURL, ok := s.resolveSandboxTarget(c, sandbox, path)
	if !ok {
		return
	}

	// Create reverse proxy with reusable transport
	proxy := httputil.NewSingleHostReverseProxy(targetURL)

	// Choose transport and auth based on picod-auth-mode
	var useMTLS bool
	if s.config.PicodAuthMode == PicodAuthModeMTLS {
		if s.mtlsPicodTransport == nil {
			klog.Error("CRITICAL: picod-auth-mode is 'mtls' but mTLS transport is nil. Refusing to downgrade to JWT.")
			c.JSON(http.StatusInternalServerError, gin.H{"error": "mTLS authentication is requested but mTLS transport is not configured"})
			return
		}
		proxy.Transport = s.mtlsPicodTransport
		targetURL.Scheme = "https"
		useMTLS = true
	} else {
		proxy.Transport = s.httpTransport
		useMTLS = false
	}

	// In mTLS mode, the TLS handshake authenticates the Router — no JWT needed.
	// In JWT mode (or when mTLS is not available), sign the request with a JWT.
	var jwtToken string
	if !useMTLS {
		var ok bool
		jwtToken, ok = s.generateSandboxJWT(c, sandbox)
		if !ok {
			return
		}
	}

	configureProxyDirector(proxy, c, targetURL, path, jwtToken, sandbox.SessionID)
	configureProxyErrorHandler(proxy, c, sandbox.SessionID)
	configureProxyResponse(proxy, sandbox.SessionID)

	// Use the proxy to serve the request
	proxy.ServeHTTP(c.Writer, c.Request)
}
