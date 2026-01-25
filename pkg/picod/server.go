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
	engine             *gin.Engine
	config             Config
	authManager        *AuthManager
	startTime          time.Time
	workspaceDir       string
	originalWorkingDir string
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
	if config.Workspace != "" {
		s.setWorkspace(config.Workspace)
		klog.Infof("Set workspace to configured value: %q", config.Workspace)
	} else {
		// Default to current working directory if not specified
		cwd, err := os.Getwd()
		if err != nil {
			klog.Fatalf("Failed to get current working directory: %v", err)
		}
		s.setWorkspace(cwd)
		klog.Infof("Set workspace to current working directory: %q", cwd)
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

// RestoreWorkingDirectory restores the process working directory to its original state
func (s *Server) RestoreWorkingDirectory() {
	if s.originalWorkingDir != "" {
		if err := os.Chdir(s.originalWorkingDir); err != nil {
			klog.Warningf("failed to restore working directory to %q: %v", s.originalWorkingDir, err)
		} else {
			klog.Infof("restored working directory to: %q", s.originalWorkingDir)
		}
	}
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
