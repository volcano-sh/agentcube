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
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"k8s.io/klog/v2"

	"github.com/volcano-sh/agentcube/pkg/router/e2b"
	"github.com/volcano-sh/agentcube/pkg/store"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

// Server is the main structure for Router apiserver
type Server struct {
	config         *Config
	engine         *gin.Engine
	httpServer     *http.Server
	e2bEngine      *gin.Engine
	e2bHTTPServer  *http.Server
	e2bServer      *e2b.Server
	sessionManager SessionManager
	storeClient    store.Store
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
	if config.E2BPort == "" {
		config.E2BPort = "8081"
	}
	if config.InitialConnectRetryCount < 0 {
		config.InitialConnectRetryCount = 0
	}
	if config.InitialConnectRetryInterval <= 0 {
		config.InitialConnectRetryInterval = 200 * time.Millisecond
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
		IdleConnTimeout:    0,
		DisableCompression: false,
	}

	server := &Server{
		config:         config,
		sessionManager: sessionManager,
		storeClient:    store.Storage(),
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
	concurrency := make(chan struct{}, s.config.MaxConcurrentRequests)
	return func(c *gin.Context) {
		// Try to acquire a slot in the semaphore
		select {
		case concurrency <- struct{}{}:
			// Successfully acquired a slot, continue processing
			defer func() {
				// Release the slot when done
				<-concurrency
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

	// Invocation routes accept all HTTP methods used by Envd downstream
	// (Envd exposes filesystem operations that use DELETE/PUT in addition
	// to GET/POST). Without these, the router returns 404 even though
	// PicoD can serve the request.
	invokeMethods := []string{
		http.MethodGet,
		http.MethodPost,
		http.MethodPut,
		http.MethodPatch,
		http.MethodDelete,
	}
	for _, m := range invokeMethods {
		v1.Handle(m, "/namespaces/:namespace/agent-runtimes/:name/invocations/*path", s.handleAgentInvoke)
		v1.Handle(m, "/namespaces/:namespace/code-interpreters/:name/invocations/*path", s.handleCodeInterpreterInvoke)
	}

	// E2B API routes run on a separate listener (:8081 by default)
	s.e2bEngine = gin.New()

	// Health check endpoints (no authentication required, no concurrency limit)
	s.e2bEngine.GET("/health/live", s.handleHealthLive)
	s.e2bEngine.GET("/health/ready", s.handleHealthReady)

	s.e2bEngine.Use(gin.Logger())
	s.e2bEngine.Use(gin.Recovery())
	s.e2bEngine.Use(s.concurrencyLimitMiddleware())

	// Native invocation routes are also exposed on the E2B engine so that
	// callers using the E2B port (8081) can reach Envd via the standard
	// /v1/namespaces/.../invocations/* path. These routes are registered
	// outside the e2bGroup so they bypass the API key middleware applied
	// inside e2b.NewServer (native callers authenticate via JWT/SA tokens).
	v1OnE2B := s.e2bEngine.Group("/v1")
	for _, m := range invokeMethods {
		v1OnE2B.Handle(m, "/namespaces/:namespace/agent-runtimes/:name/invocations/*path", s.handleAgentInvoke)
		v1OnE2B.Handle(m, "/namespaces/:namespace/code-interpreters/:name/invocations/*path", s.handleCodeInterpreterInvoke)
	}

	e2bGroup := s.e2bEngine.Group("")
	klog.Infof("Setting up E2B Platform API routes (storeClient=%v, sessionManager=%v)", s.storeClient != nil, s.sessionManager != nil)
	e2bSrv, err := e2b.NewServer(e2bGroup, s.storeClient, s.sessionManager)
	if err != nil {
		// Log at high severity so the error is visible in kubectl logs even
		// though we keep the router running for native routes.
		klog.Errorf("E2B server initialization FAILED: %v. Platform API routes (/templates, /sandboxes) will NOT be available.", err)
	} else {
		s.e2bServer = e2bSrv
		klog.Info("E2B server initialized successfully")
	}

	// Always set NoRoute so unmatched requests on the E2B port get a clear
	// 503/400 response instead of gin's default "404 page not found".
	s.e2bEngine.NoRoute(s.handleE2BSandboxProxy)

	// Log all registered E2B routes for diagnostics — this makes it trivial to
	// verify in kubectl logs whether Platform API routes were actually wired up.
	for _, route := range s.e2bEngine.Routes() {
		klog.Infof("E2B route registered: %s %s", route.Method, route.Path)
	}
}

// Start starts the Router API server (both Native and E2B listeners)
func (s *Server) Start(ctx context.Context) error {
	if err := s.initServers(); err != nil {
		return err
	}
	s.runShutdownWatcher(ctx)
	return s.runListeners()
}

func (s *Server) initServers() error {
	nativeAddr := ":" + s.config.Port

	h2s := &http2.Server{}
	h2cHandler := h2c.NewHandler(s.engine, h2s)

	s.httpServer = &http.Server{
		Addr:        nativeAddr,
		Handler:     h2cHandler,
		ReadTimeout: 30 * time.Second,
		IdleTimeout: 90 * time.Second,
	}

	if s.e2bEngine != nil {
		e2bAddr := ":" + s.config.E2BPort
		s.e2bHTTPServer = &http.Server{
			Addr:        e2bAddr,
			Handler:     s.e2bEngine,
			ReadTimeout: 30 * time.Second,
			IdleTimeout: 90 * time.Second,
		}
	}

	if s.config.EnableTLS {
		if s.config.TLSCert == "" || s.config.TLSKey == "" {
			return fmt.Errorf("TLS enabled but cert/key not provided")
		}
	}
	return nil
}

func (s *Server) runShutdownWatcher(ctx context.Context) {
	go func() {
		<-ctx.Done()
		klog.Info("Shutting down Router servers...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if s.httpServer != nil {
			if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
				klog.Errorf("Native server shutdown error: %v", err)
			}
		}
		if s.e2bHTTPServer != nil {
			if err := s.e2bHTTPServer.Shutdown(shutdownCtx); err != nil {
				klog.Errorf("E2B server shutdown error: %v", err)
			}
		}
	}()
}

func (s *Server) runListeners() error {
	var wg sync.WaitGroup
	errCh := make(chan error, 2)

	startServer := func(name string, srv *http.Server, tls bool) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			klog.Infof("%s Router server listening on %s", name, srv.Addr)
			var err error
			if tls {
				err = srv.ListenAndServeTLS(s.config.TLSCert, s.config.TLSKey)
			} else {
				err = srv.ListenAndServe()
			}
			if err != nil && err != http.ErrServerClosed {
				errCh <- fmt.Errorf("%s server: %w", name, err)
			}
		}()
	}

	startServer("Native", s.httpServer, s.config.EnableTLS)
	if s.e2bHTTPServer != nil {
		startServer("E2B", s.e2bHTTPServer, false)
	}

	go func() {
		wg.Wait()
		close(errCh)
	}()

	err, ok := <-errCh
	if !ok {
		return http.ErrServerClosed
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if s.httpServer != nil {
		_ = s.httpServer.Shutdown(shutdownCtx)
	}
	if s.e2bHTTPServer != nil {
		_ = s.e2bHTTPServer.Shutdown(shutdownCtx)
	}
	<-errCh
	return err
}
