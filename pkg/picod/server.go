package picod

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"k8s.io/klog/v2"
)

const (
	AuthModeDynamic = "dynamic"
	AuthModeStatic  = "static"
	// DefaultTTL is the default TTL in seconds (15 minutes)
	DefaultTTL = 900
)

// Config defines server configuration
type Config struct {
	Port         int    `json:"port"`
	BootstrapKey []byte `json:"bootstrap_key"`
	Workspace    string `json:"workspace"`
	AuthMode     string `json:"auth_mode"`
	// Static mode uses PICOD_PUBLIC_KEY env var (base64 encoded PEM)
}

// Server defines the PicoD HTTP server
type Server struct {
	engine         *gin.Engine
	config         Config
	authManager    *AuthManager
	jupyterManager *JupyterManager
	startTime      time.Time
	workspaceDir   string
	mu             sync.RWMutex // Protects lastActivityAt and ttl
	lastActivityAt time.Time    // Last activity timestamp
	ttl            time.Duration
}

// NewServer creates a new PicoD server instance
func NewServer(config Config) *Server {
	// Determine TTL from environment variable or use default
	ttl := time.Duration(DefaultTTL) * time.Second
	if envTTL := os.Getenv("PICOD_DEFAULT_TTL"); envTTL != "" {
		if seconds, err := strconv.Atoi(envTTL); err == nil && seconds > 0 {
			ttl = time.Duration(seconds) * time.Second
		}
	}

	now := time.Now()
	s := &Server{
		config:         config,
		startTime:      now,
		lastActivityAt: now, // Initialize to startup time
		ttl:            ttl,
	}
	// Create auth manager with activity callback
	s.authManager = NewAuthManager(s.UpdateLastActivity)
	klog.Infof("PicoD TTL configured: %v", ttl)

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

	// Initialize Jupyter Manager (Requirement 1: startup initialization)
	jupyterMgr, err := NewJupyterManager(s.workspaceDir)
	if err != nil {
		klog.Fatalf("Failed to initialize Jupyter Manager: %v", err)
	}
	s.jupyterManager = jupyterMgr

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

	// Static Key Mode initialization
	if config.AuthMode == AuthModeStatic {
		klog.Info("Static Key Mode is enabled")
		s.authManager.SetAuthMode(AuthModeStatic)

		if err := s.authManager.LoadStaticPublicKey(); err != nil {
			klog.Fatalf("Failed to load static public key from PICOD_PUBLIC_KEY: %v", err)
		}
		klog.Info("Static public key loaded successfully from PICOD_PUBLIC_KEY")
	}

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
		api.POST("/run_python", s.RunPythonHandler)
		api.PUT("/ttl", s.SetTTLHandler)
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

	// Setup graceful shutdown
	go func() {
		sigint := make(chan os.Signal, 1)
		signal.Notify(sigint, os.Interrupt, syscall.SIGTERM)
		<-sigint

		klog.Info("Shutting down Jupyter Manager...")
		if err := s.jupyterManager.Shutdown(); err != nil {
			klog.Errorf("Error shutting down Jupyter: %v", err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			klog.Errorf("Server shutdown error: %v", err)
		}
	}()

	return server.ListenAndServe()
}

// UpdateLastActivity updates the last activity timestamp
func (s *Server) UpdateLastActivity() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastActivityAt = time.Now()
}

// getLastActivityAt returns the last activity timestamp (thread-safe)
func (s *Server) getLastActivityAt() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastActivityAt
}

// SetTTL sets the TTL value (thread-safe)
func (s *Server) SetTTL(ttl time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ttl = ttl
}

// getTTL returns the current TTL value (thread-safe)
func (s *Server) getTTL() time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ttl
}

// SetTTLRequest represents the request body for setting TTL
type SetTTLRequest struct {
	TTL int64 `json:"ttl" binding:"required,min=1"` // TTL in seconds
}

// SetTTLHandler handles TTL configuration requests
func (s *Server) SetTTLHandler(c *gin.Context) {
	var req SetTTLRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Invalid request: ttl must be a positive integer",
			"code":  http.StatusBadRequest,
		})
		return
	}

	newTTL := time.Duration(req.TTL) * time.Second
	s.SetTTL(newTTL)
	klog.Infof("TTL updated to %v", newTTL)

	c.JSON(http.StatusOK, gin.H{
		"message": "TTL updated successfully",
		"ttl":     req.TTL,
	})
}

// HealthResponse represents the health check response
type HealthResponse struct {
	Status           string `json:"status"` // "ok", "idle", "expiring", "expired"
	Service          string `json:"service"`
	Uptime           string `json:"uptime"`
	Initialized      bool   `json:"initialized"`
	LastActivityAt   string `json:"last_activity_at"`  // RFC3339 format
	IdleSeconds      int64  `json:"idle_seconds"`      // Seconds since last activity
	TTL              int64  `json:"ttl"`               // Configured TTL in seconds
	RemainingSeconds int64  `json:"remaining_seconds"` // Seconds until expiry (can be negative)
}

// HealthCheckHandler handles health check requests
func (s *Server) HealthCheckHandler(c *gin.Context) {
	now := time.Now()
	lastActivity := s.getLastActivityAt()
	ttl := s.getTTL()
	idleDuration := now.Sub(lastActivity)
	remainingTTL := ttl - idleDuration

	// Determine status
	status := "ok"
	if remainingTTL <= 0 {
		status = "expired"
	} else if remainingTTL < 2*time.Minute {
		status = "expiring"
	} else if idleDuration > 5*time.Minute {
		status = "idle"
	}

	response := HealthResponse{
		Status:           status,
		Service:          "PicoD",
		Uptime:           now.Sub(s.startTime).Truncate(time.Second).String(),
		Initialized:      s.authManager.IsInitialized(),
		LastActivityAt:   lastActivity.Format(time.RFC3339),
		IdleSeconds:      int64(idleDuration.Seconds()),
		TTL:              int64(ttl.Seconds()),
		RemainingSeconds: int64(remainingTTL.Seconds()),
	}

	// Return 503 if expired
	if status == "expired" {
		c.JSON(http.StatusServiceUnavailable, response)
		return
	}

	c.JSON(http.StatusOK, response)
}
