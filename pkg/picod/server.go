package picod

import (
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"k8s.io/klog/v2"
)

// Config defines server configuration
type Config struct {
	Port         int    `json:"port"`
	BootstrapKey []byte `json:"bootstrap_key"`
	Workspace    string `json:"workspace"`
}

// Server defines the PicoD HTTP server
type Server struct {
	engine       *gin.Engine
	config       Config
	authManager  *AuthManager
	startTime    time.Time
	workspaceDir string
}

// NewServer creates a new PicoD server instance
func NewServer(config Config) *Server {
	s := &Server{
		config:      config,
		startTime:   time.Now(),
		authManager: NewAuthManager(),
	}

	// Initialize workspace directory
	if config.Workspace != "" {
		s.setWorkspace(config.Workspace)
	} else {
		// Default to current working directory if not specified
		cwd, err := os.Getwd()
		if err != nil {
			klog.Fatalf("Failed to get current working directory: %v", err)
		}
		s.setWorkspace(cwd)
	}

	// Disable Gin debug output in production mode
	gin.SetMode(gin.ReleaseMode)

	engine := gin.New()

	// Global middleware
	engine.Use(gin.Logger())   // Request logging
	engine.Use(gin.Recovery()) // Crash recovery

	// Load bootstrap key (Required)
	if len(config.BootstrapKey) == 0 {
		klog.Fatal("Bootstrap key is missing. Please ensure the bootstrap public key file is correctly mounted or provided.")
	}

	if err := s.authManager.LoadBootstrapKey(config.BootstrapKey); err != nil {
		klog.Fatalf("Failed to load bootstrap key: %v", err)
	}
	klog.Info("Bootstrap key loaded successfully")

	// Load existing public key if available
	if err := s.authManager.LoadPublicKey(); err != nil {
		// Log that server is not initialized, but don't fail startup
		klog.Infof("Server not initialized: %v", err)
	}

	// API route group (Authenticated)
	api := engine.Group("/api")
	api.Use(s.authManager.AuthMiddleware())
	{
		api.POST("/execute", s.ExecuteHandler)
		api.POST("/files", s.UploadFileHandler)
		api.GET("/files", s.ListFilesHandler)
		api.GET("/files/*path", s.DownloadFileHandler)
	}

	engine.POST("/init", s.authManager.InitHandler)

	// Health check (no authentication required)
	engine.GET("/health", s.HealthCheckHandler)

	s.engine = engine
	return s
}

// Run starts the server
func (s *Server) Run() error {
	addr := fmt.Sprintf(":%d", s.config.Port)
	klog.Infof("PicoD server starting on %s", addr)

	server := &http.Server{
		Addr:              addr,
		Handler:           s.engine,
		ReadHeaderTimeout: 10 * time.Second, // Prevent Slowloris attacks
	}

	return server.ListenAndServe()
}

// HealthCheckHandler handles health check requests
func (s *Server) HealthCheckHandler(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":  "ok",
		"service": "PicoD",
		"version": "0.0.1",
		"uptime":  time.Since(s.startTime).String(),
	})
}
