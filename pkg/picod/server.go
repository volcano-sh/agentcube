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

package picod

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
	"k8s.io/klog/v2"
)

const (
	// MaxBodySize limits request body size to prevent memory exhaustion
	MaxBodySize = 32 << 20 // 32 MB
)

// Config defines server configuration
type Config struct {
	Port      int    `json:"port"`
	Workspace string `json:"workspace"`
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
	klog.Infof("Initializing workspace with config.Workspace: %q", config.Workspace)
	workspaceDir := config.Workspace
	if workspaceDir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			klog.Fatalf("Failed to get current working directory: %v", err)
		}
		workspaceDir = cwd
	}
	if err := s.setWorkspace(workspaceDir); err != nil {
		klog.Fatalf("Failed to initialize workspace: %v", err)
	}
	klog.Infof("Final workspace directory: %q", s.workspaceDir)

	// Disable Gin debug output in production mode
	gin.SetMode(gin.ReleaseMode)

	engine := gin.New()

	// Global middleware
	engine.Use(gin.Logger())   // Request logging
	engine.Use(gin.Recovery()) // Crash recovery
	engine.Use(maxBodySizeMiddleware())
	engine.MaxMultipartMemory = MaxBodySize
	engine.Use(gzip.Gzip(gzip.BestSpeed, gzip.WithExcludedPaths([]string{"/health"}))) // Response compression

	// Load public key from environment variable (required for JWT auth)
	if err := s.authManager.LoadPublicKeyFromEnv(); err != nil {
		klog.Fatalf("Failed to load public key from environment: %v", err)
	}

	// API route group with JWT authentication
	api := engine.Group("/api")
	api.Use(s.authManager.AuthMiddleware())
	{
		api.POST("/execute", s.ExecuteHandler)
		api.POST("/files", s.UploadFileHandler)
		api.GET("/files", s.ListFilesHandler)
		api.GET("/files/*path", s.DownloadFileHandler)
	}

	// Health check (no authentication required)
	engine.GET("/health", s.HealthCheckHandler)

	s.engine = engine
	return s
}

func maxBodySizeMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.ContentLength > MaxBodySize {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{
				"error":  "request body too large",
				"detail": fmt.Sprintf("maximum allowed size is %d bytes", MaxBodySize),
			})
			c.Abort()
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, MaxBodySize)
		c.Next()
	}
}

// Start starts the server and blocks until ctx is canceled or a fatal error occurs.
func (s *Server) Start(ctx context.Context) error {
	addr := fmt.Sprintf(":%d", s.config.Port)
	klog.Infof("PicoD server starting on %s", addr)

	httpServer := &http.Server{
		Addr:              addr,
		Handler:           s.engine,
		ReadHeaderTimeout: 10 * time.Second, // Prevent Slowloris attacks
	}

	// Listen for shutdown signal and gracefully stop the HTTP server.
	go func() {
		<-ctx.Done()
		klog.Info("Shutting down PicoD server...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			klog.Errorf("PicoD server shutdown error: %v", err)
		}
	}()

	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
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
