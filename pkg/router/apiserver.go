package router

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// Server is the main structure for Router apiserver
type Server struct {
	config         *Config
	engine         *gin.Engine
	httpServer     *http.Server
	sessionManager SessionManager
	redisManager   RedisManager
	semaphore      chan struct{} // For limiting concurrent requests
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
	if config.SessionExpireDuration <= 0 {
		config.SessionExpireDuration = 3600 // Default 1 hour
	}

	// Create session manager (using mock implementation)
	sessionManager := NewMockSessionManager(config.SandboxEndpoints)

	// Create Redis manager (using mock implementation)
	redisManager := NewMockRedisManager(config.EnableRedis)

	// Set Gin mode based on environment
	if config.Debug {
		gin.SetMode(gin.DebugMode)
	} else {
		gin.SetMode(gin.ReleaseMode)
	}

	server := &Server{
		config:         config,
		sessionManager: sessionManager,
		redisManager:   redisManager,
		semaphore:      make(chan struct{}, config.MaxConcurrentRequests),
	}

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
			c.JSON(http.StatusServiceUnavailable, gin.H{
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

	// Add middleware
	s.engine.Use(gin.Logger())
	s.engine.Use(gin.Recovery())

	// Health check endpoints (no authentication required, no concurrency limit)
	s.engine.GET("/health", s.handleHealth)
	s.engine.GET("/health/live", s.handleHealthLive)
	s.engine.GET("/health/ready", s.handleHealthReady)

	// API v1 routes with concurrency limiting
	v1 := s.engine.Group("/v1")
	v1.Use(s.concurrencyLimitMiddleware()) // Apply concurrency limit to API routes

	// Agent invoke requests
	v1.Any("/namespaces/:agentNamespace/agent-runtimes/:agentName/invocations/*path", s.handleAgentInvoke)

	// Code interpreter invoke requests - use different base path to avoid conflicts
	v1.Any("/code-namespaces/:namespace/code-interpreters/:name/invocations/*path", s.handleCodeInterpreterInvoke)
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
		log.Println("Shutting down Router server...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
			log.Printf("Server shutdown error: %v", err)
		}
	}()

	log.Printf("Router server listening on %s", addr)

	// Start HTTP or HTTPS server
	if s.config.EnableTLS {
		if s.config.TLSCert == "" || s.config.TLSKey == "" {
			return fmt.Errorf("TLS enabled but cert/key not provided")
		}
		return s.httpServer.ListenAndServeTLS(s.config.TLSCert, s.config.TLSKey)
	}

	return s.httpServer.ListenAndServe()
}
