package workloadmanager

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/volcano-sh/agentcube/pkg/store"
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
	storeClient       store.Store
	jwtManager        *JWTManager
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
	// EnableAuth enable auth by service account
	EnableAuth bool
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
		storeClient:       store.Storage(),
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

	// Health check (no authentication required)
	s.router.GET("/health", s.handleHealth)

	// API v1 routes
	v1Group := s.router.Group("/v1")
	// Apply middleware (logging first, then auth)
	v1Group.Use(s.loggingMiddleware)
	v1Group.Use(s.authMiddleware)

	// agent runtime management endpoints
	v1Group.POST("/agent-runtime", s.handleCreateSandbox)
	v1Group.DELETE("/agent-runtime/sessions/:sessionId", s.handleDeleteSandbox)
	// code interpreter management endpoints
	v1Group.POST("/code-interpreter", s.handleCreateSandbox)
	v1Group.DELETE("/code-interpreter/sessions/:sessionId", s.handleDeleteSandbox)
}

// Start starts the API server
func (s *Server) Start(ctx context.Context) error {
	if err := s.TryStoreOrLoadJWTKeySecret(ctx); err != nil {
		return fmt.Errorf("failed to store or load JWT key: %w", err)
	}

	// Initialize store with informer before starting server
	if err := s.InitializeStore(ctx); err != nil {
		return fmt.Errorf("failed to initialize sandbox store: %w", err)
	}

	if err := s.informers.RunAndWaitForCacheSync(ctx); err != nil {
		return fmt.Errorf("failed to wait for caches to sync: %w", err)
	}

	if err := s.storeClient.Ping(ctx); err != nil {
		return fmt.Errorf("failed to ping store: %w", err)
	}
	log.Println("kv store Ping check successfully")

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

	gc := newGarbageCollector(s.k8sClient, s.storeClient, 15*time.Second)
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
	start := time.Now()
	log.Printf("%s %s %s", c.Request.Method, c.Request.RequestURI, c.ClientIP())
	c.Next()
	log.Printf("%s %s - completed in %v", c.Request.Method, c.Request.RequestURI, time.Since(start))
}
