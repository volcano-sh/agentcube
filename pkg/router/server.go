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
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"k8s.io/klog/v2"

	"github.com/volcano-sh/agentcube/pkg/store"
)

// Server is the main structure for Router apiserver
type Server struct {
	config         *Config
	engine         *gin.Engine
	httpServer     *http.Server
	sessionManager SessionManager
	storeClient    store.Store
	semaphore      chan struct{}   // For limiting concurrent requests
	httpTransport  *http.Transport // Reusable HTTP transport for connection pooling
	jwtManager     *JWTManager     // JWT manager for signing requests to sandboxes
}

// NewServer creates a new Router API server instance
func NewServer(config *Config) (*Server, error) {
	if config == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}

	// Set default values for concurrency settings
	if config.MaxConcurrentRequests <= 0 {
		config.MaxConcurrentRequests = 1000 // Default limit
	}
	if config.RequestTimeout <= 0 {
		config.RequestTimeout = 30 // Default 30 seconds
	}
	if config.MaxIdleConns <= 0 {
		config.MaxIdleConns = 100 // Default 100 idle connections
	}
	if config.MaxConnsPerHost <= 0 {
		config.MaxConnsPerHost = 10 // Default 10 connections per host
	}

	// Create session manager with store client
	sessionManager, err := NewSessionManager(store.Storage())
	if err != nil {
		return nil, fmt.Errorf("failed to create session manager: %w", err)
	}

	// Set Gin mode based on environment
	if config.Debug {
		gin.SetMode(gin.DebugMode)
	} else {
		gin.SetMode(gin.ReleaseMode)
	}

	// Create a reusable HTTP transport for connection pooling
	httpTransport := &http.Transport{
		MaxIdleConns:        config.MaxIdleConns,
		MaxIdleConnsPerHost: config.MaxConnsPerHost,
		IdleConnTimeout:     0,
		DisableCompression:  false,
		ForceAttemptHTTP2:   true,
	}

	server := &Server{
		config:         config,
		sessionManager: sessionManager,
		storeClient:    store.Storage(),
		semaphore:      make(chan struct{}, config.MaxConcurrentRequests),
		httpTransport:  httpTransport,
	}

	// Initialize JWT manager for signing requests to sandboxes
	jwtManager, err := NewJWTManager()
	if err != nil {
		return nil, fmt.Errorf("failed to create JWT manager: %w", err)
	}

	// Try to load existing keys from secret or store new ones
	if err := jwtManager.TryStoreOrLoadJWTKeySecret(context.Background()); err != nil {
		return nil, fmt.Errorf("failed to store/load JWT key secret: %w", err)
	}

	server.jwtManager = jwtManager
	klog.Info("JWT manager initialized successfully")

	// Setup routes
	server.setupRoutes()

	return server, nil
}

// concurrencyLimitMiddleware limits the number of concurrent requests
func (s *Server) concurrencyLimitMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Try to acquire a slot in the semaphore
		select {
		case s.semaphore <- struct{}{}:
			// Successfully acquired a slot, continue processing
			defer func() {
				// Release the slot when done
				<-s.semaphore
			}()
			c.Next()
		default:
			// No slots available, return 503 Service Unavailable
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error": "server overloaded, please try again later",
				"code":  "SERVER_OVERLOADED",
			})
			c.Abort()
		}
	}
}

// setupRoutes configures HTTP routes using Gin
func (s *Server) setupRoutes() {
	s.engine = gin.New()

	// Health check endpoints (no authentication required, no concurrency limit)
	s.engine.GET("/health/live", s.handleHealthLive)
	s.engine.GET("/health/ready", s.handleHealthReady)

	// API v1 routes with concurrency limiting
	v1 := s.engine.Group("/v1")
	// Add middleware
	v1.Use(gin.Logger())
	v1.Use(gin.Recovery())

	v1.Use(s.concurrencyLimitMiddleware()) // Apply concurrency limit to API routes

	// Agent invoke requests (support GET/POST, since downstream uses these methods)
	v1.GET("/namespaces/:namespace/agent-runtimes/:name/invocations/*path", s.handleAgentInvoke)
	v1.POST("/namespaces/:namespace/agent-runtimes/:name/invocations/*path", s.handleAgentInvoke)

	// Code interpreter invoke requests (support GET/POST, since downstream uses GET for file download)
	v1.GET("/namespaces/:namespace/code-interpreters/:name/invocations/*path", s.handleCodeInterpreterInvoke)
	v1.POST("/namespaces/:namespace/code-interpreters/:name/invocations/*path", s.handleCodeInterpreterInvoke)
}

// Start starts the Router API server
func (s *Server) Start(ctx context.Context) error {
	addr := ":" + s.config.Port

	s.httpServer = &http.Server{
		Addr:         addr,
		Handler:      s.engine,
		ReadTimeout:  30 * time.Second, // Longer timeout for potential long-running requests
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Listen for shutdown signal in goroutine
	go func() {
		<-ctx.Done()
		klog.Info("Shutting down Router server...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
			klog.Errorf("Server shutdown error: %v", err)
		}
	}()

	klog.Infof("Router server listening on %s", addr)

	// Start HTTP or HTTPS server
	if s.config.EnableTLS {
		if s.config.TLSCert == "" || s.config.TLSKey == "" {
			return fmt.Errorf("TLS enabled but cert/key not provided")
		}
		return s.httpServer.ListenAndServeTLS(s.config.TLSCert, s.config.TLSKey)
	}

	return s.httpServer.ListenAndServe()
}
