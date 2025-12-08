package router

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	redisv9 "github.com/redis/go-redis/v9"

	"github.com/volcano-sh/agentcube/pkg/redis"
)

// Server is the main structure for Router apiserver
type Server struct {
	config         *Config
	engine         *gin.Engine
	httpServer     *http.Server
	sessionManager SessionManager
	redisClient    redis.Client
	semaphore      chan struct{}   // For limiting concurrent requests
	httpTransport  *http.Transport // Reusable HTTP transport for connection pooling
}

// makeRedisOptions creates redis options from environment variables
func makeRedisOptions() (*redisv9.Options, error) {
	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		return nil, fmt.Errorf("missing env var REDIS_ADDR")
	}
	redisPassword := os.Getenv("REDIS_PASSWORD")
	if redisPassword == "" {
		return nil, fmt.Errorf("missing env var REDIS_PASSWORD")
	}
	redisOptions := &redisv9.Options{
		Addr:     redisAddr,
		Password: redisPassword,
	}
	return redisOptions, nil
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

	// Initialize Redis client
	redisOptions, err := makeRedisOptions()
	if err != nil {
		return nil, fmt.Errorf("make redis options failed: %w", err)
	}
	redisClient := redis.NewClient(redisOptions)

	// Create session manager with redis client
	sessionManager, err := NewSessionManager(redisClient)
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
		redisClient:    redisClient,
		semaphore:      make(chan struct{}, config.MaxConcurrentRequests),
		httpTransport:  httpTransport,
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
	v1.POST("/namespaces/:namespace/agent-runtimes/:name/invocations/*path", s.handleAgentInvoke)

	// Code interpreter invoke requests
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
