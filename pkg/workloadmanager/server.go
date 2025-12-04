package workloadmanager

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

// Server is the main structure for workload manager
type Server struct {
	config            *Config
	router            *gin.Engine
	httpServer        *http.Server
	k8sClient         *K8sClient
	sandboxController *SandboxReconciler
	sandboxStore      *SandboxStore
	tokenCache        *TokenCache
	informers         *Informers
	redisClient       redis.Client
	jwtManager        *JWTManager
	enableAuth        bool
}

type Config struct {
	// Port is the port the API server listens on
	Port string
	// RuntimeClassName is the RuntimeClassName for sandbox pods
	RuntimeClassName string
	// EnableTLS enables HTTPS
	EnableTLS bool
	// TLSCert is the path to the TLS certificate file
	TLSCert string
	// TLSKey is the path to the TLS private key file
	TLSKey string
}

// makeRedisOptions make redis options by environment
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

	redisOptions, err := makeRedisOptions()
	if err != nil {
		return nil, fmt.Errorf("make redis options failed: %w", err)
	}

	// Create sandbox store
	sandboxStore := NewSandboxStore()

	// Create token cache (cache up to 1000 tokens, 5min TTL)
	tokenCache := NewTokenCache(1000, 5*time.Minute)

	// Create JWT manager for signing sandbox init requests
	jwtManager, err := NewJWTManager()
	if err != nil {
		return nil, fmt.Errorf("failed to create JWT manager: %w", err)
	}

	server := &Server{
		config:            config,
		k8sClient:         k8sClient,
		sandboxStore:      sandboxStore,
		sandboxController: sandboxController,
		tokenCache:        tokenCache,
		informers:         NewInformers(k8sClient),
		redisClient:       redis.NewClient(redisOptions),
		jwtManager:        jwtManager,
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

	// agent runtime management endpoints
	v1.POST("/agent-runtime", s.handleCreateSandbox)
	v1.DELETE("/agent-runtime/sessions/:sessionId", s.handleDeleteSandbox)
	// code interpreter management endpoints
	v1.POST("/code-interpreter", s.handleCreateSandbox)
	v1.DELETE("/code-interpreter/sessions/:sessionId", s.handleDeleteSandbox)
}

// Start starts the API server
func (s *Server) Start(ctx context.Context) error {
	// Store JWT public key in Kubernetes secret
	publicKeyPEM, err := s.jwtManager.GetPublicKeyPEM()
	if err != nil {
		return fmt.Errorf("failed to get JWT public key: %w", err)
	}

	if err := s.k8sClient.StoreJWTPublicKeyInSecret(ctx, publicKeyPEM); err != nil {
		log.Printf("Warning: failed to store JWT public key in secret: %v", err)
		// Don't fail startup if secret storage fails, just log warning
	} else {
		log.Println("JWT public key stored in Kubernetes secret successfully")
	}

	// Initialize store with informer before starting server
	if err := s.InitializeStore(ctx); err != nil {
		return fmt.Errorf("failed to initialize sandbox store: %w", err)
	}

	if err := s.informers.RunAndWaitForCacheSync(ctx); err != nil {
		return fmt.Errorf("failed to wait for caches to sync: %w", err)
	}

	if err := s.redisClient.Ping(ctx); err != nil {
		return fmt.Errorf("failed to ping redis: %w", err)
	}
	log.Println("redis Ping check successfully")

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

	gc := newGarbageCollector(s.k8sClient, s.redisClient, 15*time.Second)
	go gc.run(ctx.Done())

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
