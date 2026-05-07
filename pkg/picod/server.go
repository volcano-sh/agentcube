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
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"k8s.io/klog/v2"
)

// Config defines server configuration
type Config struct {
	Port      int    `json:"port"`
	Workspace string `json:"workspace"`
}

// Server defines the PicoD HTTP server
type Server struct {
	engine          *gin.Engine
	config          Config
	authManager     *AuthManager
	startTime       time.Time
	workspaceDir    string
	processRegistry *ProcessRegistry
}

// NewServer creates a new PicoD server instance
func NewServer(config Config) *Server {
	s := &Server{
		config:          config,
		startTime:       time.Now(),
		authManager:     NewAuthManager(),
		processRegistry: NewProcessRegistry(),
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

	// Load public key from environment variable (required)
	if err := s.authManager.LoadPublicKeyFromEnv(); err != nil {
		klog.Fatalf("Failed to load public key from environment: %v", err)
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

	// Health check (no authentication required)
	engine.GET("/health", s.HealthCheckHandler)

	// E2B envd API routes
	s.setupEnvdRoutes(engine)

	s.engine = engine
	return s
}

// setupEnvdRoutes registers E2B envd-compatible endpoints.
func (s *Server) setupEnvdRoutes(engine *gin.Engine) {
	// Health check (no authentication)
	engine.GET("/envd/health", s.EnvdHealthHandler)

	envd := engine.Group("/envd")
	envd.Use(s.authManager.AuthMiddleware())
	{
		// Environment
		envd.GET("/env", s.EnvdEnvHandler)

		// Filesystem
		envd.POST("/filesystem/upload", s.EnvdUploadHandler)
		envd.GET("/filesystem/download", s.EnvdDownloadHandler)
		envd.GET("/filesystem/list", s.EnvdListHandler)
		envd.POST("/filesystem/mkdir", s.EnvdMkdirHandler)
		envd.POST("/filesystem/move", s.EnvdMoveHandler)
		envd.DELETE("/filesystem/remove", s.EnvdRemoveHandler)
		envd.GET("/filesystem/stat", s.EnvdStatHandler)

		// Process
		envd.POST("/process/start", s.EnvdProcessStartHandler)
		envd.POST("/process/input", s.EnvdProcessInputHandler)
		envd.POST("/process/close-stdin", s.EnvdProcessCloseStdinHandler)
		envd.POST("/process/signal", s.EnvdProcessSignalHandler)
		envd.GET("/process/list", s.EnvdProcessListHandler)
	}
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
