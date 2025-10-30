package picoapiserver

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/agent-box/pico-apiserver/pkg/controller"
	"github.com/gorilla/mux"
)

// Server is the main structure for pico-apiserver
type Server struct {
	config            *Config
	router            *mux.Router
	httpServer        *http.Server
	k8sClient         *K8sClient
	sandboxController *controller.SandboxReconciler
	sandboxStore      *SandboxStore
}

// NewServer creates a new API server instance
func NewServer(config *Config, sandboxController *controller.SandboxReconciler) (*Server, error) {
	if config == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}

	// Create Kubernetes client
	k8sClient, err := NewK8sClient(config.Namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	// Create sandbox store
	sandboxStore := NewSandboxStore()

	server := &Server{
		config:            config,
		k8sClient:         k8sClient,
		sandboxStore:      sandboxStore,
		sandboxController: sandboxController,
	}

	// Setup routes
	server.setupRoutes()

	return server, nil
}

// setupRoutes configures HTTP routes
func (s *Server) setupRoutes() {
	s.router = mux.NewRouter()

	// Health check (no authentication required)
	s.router.HandleFunc("/health", s.handleHealth).Methods("GET")

	// API v1 routes
	v1 := s.router.PathPrefix("/v1").Subrouter()

	// Sandbox management endpoints
	v1.HandleFunc("/sandboxes", s.handleCreateSandbox).Methods("POST")
	v1.HandleFunc("/sandboxes", s.handleListSandboxes).Methods("GET")
	v1.HandleFunc("/sandboxes/{sandboxId}", s.handleGetSandbox).Methods("GET")
	v1.HandleFunc("/sandboxes/{sandboxId}", s.handleDeleteSandbox).Methods("DELETE")

	// HTTP CONNECT tunnel endpoint - for SSH/SFTP proxy
	// Path: /v1/sandboxes/{sandboxId} with CONNECT method
	v1.HandleFunc("/sandboxes/{sandboxId}", s.handleTunnel)

	// Apply middleware (auth first, then logging)
	// Auth middleware excludes /health, logging middleware also excludes /health
	s.router.Use(s.authMiddleware)
	s.router.Use(s.loggingMiddleware)
}

// Start starts the API server
func (s *Server) Start(ctx context.Context) error {
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
func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip logging for health check endpoint
		if r.URL.Path == "/health" {
			next.ServeHTTP(w, r)
			return
		}

		start := time.Now()
		log.Printf("%s %s %s", r.Method, r.RequestURI, r.RemoteAddr)
		next.ServeHTTP(w, r)
		log.Printf("%s %s - completed in %v", r.Method, r.RequestURI, time.Since(start))
	})
}
