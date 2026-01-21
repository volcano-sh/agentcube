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

// handleInvoke handles invocation requests for agents and code interpreters
func (s *Server) handleInvoke(c *gin.Context, namespace, name, path, kind string) {
	klog.V(4).Infof("%s invoke request: namespace=%s, name=%s, path=%s", kind, namespace, name, path)

	clientSessionID := c.GetHeader("x-agentcube-session-id")

	sandbox, err := s.sessionManager.GetSandboxBySession(
		c.Request.Context(),
		clientSessionID,
		namespace,
		name,
		kind,
	)
	if err != nil {
		klog.Errorf("Failed to get sandbox info: %v, session id %s", err, clientSessionID)
		s.handleSandboxLookupError(c, err, clientSessionID, namespace, name, kind)
		return
	}

	if sandbox == nil || sandbox.SessionID == "" {
		klog.Error("Invalid sandbox returned")
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "internal error",
			"code":  "INTERNAL_ERROR",
		})
		return
	}

	actualSessionID := sandbox.SessionID

	endpoint, err := selectSandboxEndpoint(sandbox, path)
	if err != nil {
		klog.Warningf("Failed to select endpoint: %v", err)
		c.JSON(http.StatusNotFound, gin.H{
			"error": err.Error(),
			"code":  "SERVICE_NOT_FOUND",
		})
		return
	}

	if err := s.storeClient.UpdateSessionLastActivity(
		c.Request.Context(),
		actualSessionID,
		time.Now(),
	); err != nil {
		klog.Warningf("Failed to update session activity: %v", err)
	}

	klog.Infof(
		"Forwarding to sandbox: sessionID=%s namespace=%s name=%s path=%s endpoint=%s",
		actualSessionID, namespace, name, path, endpoint,
	)

	s.forwardToSandbox(c, endpoint, path, actualSessionID)
}

func (s *Server) handleGetSandboxError(c *gin.Context, err error) {
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

	c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
}

// handleAgentInvoke handles agent invocation requests
func (s *Server) handleAgentInvoke(c *gin.Context) {
	s.handleInvoke(
		c,
		c.Param("namespace"),
		c.Param("name"),
		c.Param("path"),
		types.AgentRuntimeKind,
	)
}

// handleCodeInterpreterInvoke handles code interpreter invocation requests
func (s *Server) handleCodeInterpreterInvoke(c *gin.Context) {
	s.handleInvoke(
		c,
		c.Param("namespace"),
		c.Param("name"),
		c.Param("path"),
		types.CodeInterpreterKind,
	)
}

// forwardToSandbox forwards the request to the sandbox endpoint
func (s *Server) forwardToSandbox(c *gin.Context, endpoint, path, sessionID string) {
	targetURL, err := url.Parse(endpoint)
	if err != nil {
		klog.Errorf("Failed to parse sandbox endpoint %s: %v", endpoint, err)
		c.JSON(http.StatusNotFound, gin.H{
			"error": "invalid sandbox endpoint",
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

		if s.jwtManager != nil && req.Header.Get("Authorization") == "" {
			if token, err := s.jwtManager.GenerateToken(
				map[string]interface{}{"session_id": sessionID},
			); err == nil {
				req.Header.Set("Authorization", "Bearer "+token)
			}
		}

		klog.Infof(
			"Forwarding request to %s%s (session: %s)",
			targetURL.String(), path, sessionID,
		)
	}

	proxy.ErrorHandler = func(_ http.ResponseWriter, _ *http.Request, err error) {
		klog.Errorf("Proxy error (session: %s): %v", sessionID, err)
		c.JSON(http.StatusBadGateway, gin.H{
			"error": "sandbox unreachable",
		})
		c.Abort()
	}

	proxy.ModifyResponse = func(resp *http.Response) error {
		resp.Header.Set("x-agentcube-session-id", sessionID)
		return nil
	}

	proxy.ServeHTTP(c.Writer, c.Request)
}