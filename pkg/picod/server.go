package picod

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

var startTime = time.Now() // Server start time

// Config defines server configuration
type Config struct {
	Port         int    `json:"port"`
	BootstrapKey string `json:"bootstrap_key"`
}

// Server defines the PicoD HTTP server
type Server struct {
	engine      *gin.Engine
	config      Config
	authManager *AuthManager
}

// NewServer creates a new PicoD server instance
func NewServer(config Config) *Server {
	// Disable Gin debug output in production mode
	gin.SetMode(gin.ReleaseMode)

	engine := gin.New()

	// Global middleware
	engine.Use(gin.Logger())   // Request logging
	engine.Use(gin.Recovery()) // Crash recovery

	// Create auth manager
	authManager := NewAuthManager()

	// Load bootstrap key if provided
	if config.BootstrapKey != "" {
		if err := authManager.LoadBootstrapKey(config.BootstrapKey); err != nil {
			fmt.Printf("Failed to load bootstrap key: %v\n", err)
		} else {
			fmt.Println("Bootstrap key loaded successfully")
		}
	}

	// Load existing public key if available
	if err := authManager.LoadPublicKey(); err != nil {
		// Log that server is not initialized, but don't fail startup
		fmt.Printf("Server not initialized: %v\n", err)
	}

	// Apply authentication middleware
	engine.Use(authManager.AuthMiddleware())

	// API route group
	api := engine.Group("/api")
	{
		api.POST("/init", authManager.InitHandler)
		api.POST("/execute", ExecuteHandler)
		api.POST("/files", UploadFileHandler)
		api.POST("/files/list", ListFilesHandler)
		api.GET("/files/*path", DownloadFileHandler)
	}

	// Health check (no authentication required)
	engine.GET("/health", HealthCheckHandler)

	return &Server{
		engine:      engine,
		config:      config,
		authManager: authManager,
	}
}

// Run starts the server
func (s *Server) Run() error {
	addr := fmt.Sprintf(":%d", s.config.Port)
	log.Printf("PicoD server starting on %s", addr)
	return http.ListenAndServe(addr, s.engine)
}

// HealthCheckHandler handles health check requests
func HealthCheckHandler(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":  "ok",
		"service": "PicoD",
		"version": "1.0.0",
		"uptime":  time.Since(startTime).String(),
	})
}
