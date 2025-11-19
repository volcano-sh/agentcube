package apiserver

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/volcano-sh/agentcube/pkg/controller"
)

// Server is the main structure for apiserver
type Server struct {
	config            *Config
	router            *gin.Engine
	httpServer        *http.Server
	k8sClient         *K8sClient
	sandboxController *SandboxReconciler
	sandboxStore      *SandboxStore
	tokenCache        *TokenCache
}

// NewServer creates a new API server instance
func NewServer(config *Config, sandboxController *SandboxReconciler) (*Server, error) {
	if config == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}

	// Create Kubernetes client
	k8sClient, err := NewK8sClient()
	if err != nil {
		return nil, fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	// Create sandbox store
	sandboxStore := NewSandboxStore()

	// Create token cache (cache up to 1000 tokens, 5min TTL)
	tokenCache := NewTokenCache(1000, 5*time.Minute)

	server := &Server{
		config:            config,
		k8sClient:         k8sClient,
		sandboxStore:      sandboxStore,
		sandboxController: sandboxController,
		tokenCache:        tokenCache,
	}

	// Setup routes
	server.setupRoutes()

	return server, nil
}

// InitializeStore initializes the sandbox store with Kubernetes informer
func (s *Server) InitializeStore(ctx context.Context) error {
	informer := s.k8sClient.GetSandboxInformer()
	return s.sandboxStore.InitializeWithInformer(ctx, informer, s.k8sClient)
}

// setupRoutes configures HTTP routes
func (s *Server) setupRoutes() {
	s.router = gin.New()

	// Apply middleware (logging first, then auth)
	// Auth middleware excludes /health, logging middleware also excludes /health
	s.router.Use(s.loggingMiddleware)
	s.router.Use(s.authMiddleware)

	// Health check (no authentication required)
	s.router.GET("/health", s.handleHealth)

	// API v1 routes
	v1 := s.router.Group("/v1")

	// Sandbox management endpoints
	v1.POST("/sandboxes", s.handleCreateSandbox)
	v1.GET("/sandboxes", s.handleListSandboxes)
	v1.GET("/sandboxes/:sandboxId", s.handleGetSandbox)
	v1.DELETE("/sandboxes/:sandboxId", s.handleDeleteSandbox)

	// HTTP CONNECT tunnel endpoint - for SSH/SFTP proxy
	// Path: /v1/sandboxes/{sandboxId} with CONNECT method
	v1.Handle("CONNECT", "/sandboxes/:sandboxId", s.handleTunnel)
}

// Start starts the API server
func (s *Server) Start(ctx context.Context) error {
	// Initialize store with informer before starting server
	if err := s.InitializeStore(ctx); err != nil {
		return fmt.Errorf("failed to initialize sandbox store: %w", err)
	}

	addr := ":" + s.config.Port

	s.httpServer = &http.Server{
		Addr:         addr,
		Handler:      s.router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Listen for shutdown signal in goroutine
	go func() {
		<-ctx.Done()
		log.Println("Shutting down server...")
		// Stop the sandbox store informer
		s.sandboxStore.Stop()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
			log.Printf("Server shutdown error: %v", err)
		}
	}()

	log.Printf("Server listening on %s", addr)

	// Start HTTP or HTTPS server
	if s.config.EnableTLS {
		if s.config.TLSCert == "" || s.config.TLSKey == "" {
			return fmt.Errorf("TLS enabled but cert/key not provided")
		}
		return s.httpServer.ListenAndServeTLS(s.config.TLSCert, s.config.TLSKey)
	}

	return s.httpServer.ListenAndServe()
}

// loggingMiddleware logs each request (except /health)
func (s *Server) loggingMiddleware(c *gin.Context) {
	// Skip logging for health check endpoint
	if c.Request.URL.Path == "/health" {
		c.Next()
		return
	}

	start := time.Now()
	log.Printf("%s %s %s", c.Request.Method, c.Request.RequestURI, c.ClientIP())
	c.Next()
	log.Printf("%s %s - completed in %v", c.Request.Method, c.Request.RequestURI, time.Since(start))
}
