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
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"k8s.io/klog/v2"

	"github.com/volcano-sh/agentcube/pkg/mtls"
)

// Config defines server configuration
type Config struct {
	Port      int    `json:"port"`
	Workspace string `json:"workspace"`

	// EnableMTLS enables mutual TLS on the PicoD listener.
	// When true, JWT-based authentication is bypassed (transport-layer auth).
	EnableMTLS bool `json:"enableMTLS"`

	// mTLS configuration (certificate source abstraction)

	// MTLSCertFile is the path to the mTLS certificate (--mtls-cert-file)
	MTLSCertFile string `json:"mtlsCertFile"`
	// MTLSKeyFile is the path to the mTLS private key (--mtls-key-file)
	MTLSKeyFile string `json:"mtlsKeyFile"`
	// MTLSCAFile is the path to the mTLS CA bundle (--mtls-ca-file)
	MTLSCAFile string `json:"mtlsCAFile"`
}

// Server defines the PicoD HTTP server
type Server struct {
	engine       *gin.Engine
	config       Config
	authManager  *AuthManager
	startTime    time.Time
	workspaceDir string
	certWatcher  *mtls.CertWatcher // mTLS cert watcher for graceful cleanup
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

	// When mTLS is enabled, transport-layer auth replaces JWT-based auth.
	// The TLS handshake itself authenticates the caller (Router) via SPIFFE ID.
	if config.EnableMTLS {
		klog.Info("mTLS mode: skipping JWT public key loading (transport-layer auth)")
	} else {
		// Load public key from environment variable (required for JWT mode)
		if err := s.authManager.LoadPublicKeyFromEnv(); err != nil {
			klog.Fatalf("Failed to load public key from environment: %v", err)
		}
	}

	// API route group
	api := engine.Group("/api")
	if !config.EnableMTLS {
		// Only apply JWT auth middleware when NOT using mTLS
		api.Use(s.authManager.AuthMiddleware())
	}
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

	if s.config.EnableMTLS {
		return s.serveMTLS(server, addr)
	}

	return server.ListenAndServe()
}

// serveMTLS configures and starts the server with mutual TLS.
func (s *Server) serveMTLS(server *http.Server, addr string) error {
	mtlsCfg := &mtls.Config{
		CertFile: s.config.MTLSCertFile,
		KeyFile:  s.config.MTLSKeyFile,
		CAFile:   s.config.MTLSCAFile,
	}
	if !mtlsCfg.Enabled() {
		return fmt.Errorf("--enable-mtls requires --mtls-cert-file, --mtls-key-file, and --mtls-ca-file to be set")
	}

	// The spiffe-helper sidecar writes the certificates asynchronously on startup.
	// We must retry loading them here to avoid crashing before the sidecar has provisioned them.
	var serverTLS *tls.Config
	var watcher *mtls.CertWatcher
	var err error
	backoff := 10 * time.Millisecond
	for i := 0; i < 50; i++ {
		serverTLS, watcher, err = mtls.LoadServerConfig(mtlsCfg, []string{mtls.RouterSPIFFEID})
		if err == nil {
			break
		}
		time.Sleep(backoff)
		backoff *= 2
		if backoff > 1*time.Second {
			backoff = 1 * time.Second
		}
	}
	if err != nil {
		return fmt.Errorf("failed to load mTLS server config after retries: %w", err)
	}
	s.certWatcher = watcher

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		watcher.Stop()
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}
	tlsListener := tls.NewListener(ln, serverTLS)

	klog.Infof("PicoD mTLS enabled: accepting only clients with SPIFFE ID %s", mtls.RouterSPIFFEID)
	return server.Serve(tlsListener)
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
