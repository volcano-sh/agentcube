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

package e2b

import (
	"context"
	"fmt"
	"strconv"

	"github.com/gin-gonic/gin"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/volcano-sh/agentcube/pkg/common/types"
	"github.com/volcano-sh/agentcube/pkg/store"
)

// SessionManager defines the interface for session management
type SessionManager interface {
	GetSandboxBySession(ctx context.Context, sessionID, namespace, name, kind string, envVars map[string]string) (*types.SandboxInfo, error)
}

// Server is the E2B API server
type Server struct {
	router         *gin.RouterGroup
	storeClient    store.Store
	sessionManager SessionManager
	k8sClient      client.Client
	mapper         *Mapper
	authenticator  *Authenticator
	config         *Config
	idGenerator    *IDGenerator
}

// Config holds the E2B server configuration
type Config struct {
	// EnvdVersion is the version of envd running in sandboxes
	EnvdVersion string
	// EnableAuth enables API key authentication
	EnableAuth bool
	// AuthConfig is the authentication configuration
	AuthConfig *AuthConfig
	// E2BPort is the E2B listener port (Platform API + Sandbox API Proxy)
	E2BPort string
	// E2BAPIKeySecret is the K8s Secret name for API key status
	E2BAPIKeySecret string
	// E2BAPIKeyConfigMap is the K8s ConfigMap name for API key namespace mapping
	E2BAPIKeyConfigMap string
	// E2BDefaultTTL is the default sandbox TTL in seconds
	E2BDefaultTTL int
	// E2BDefaultNamespace is the fallback namespace for API Keys without explicit mapping
	E2BDefaultNamespace string
	// E2BSandboxDomain is the domain suffix for Sandbox API subdomains
	E2BSandboxDomain string
}

// DefaultConfig returns default E2B server configuration
func DefaultConfig() *Config {
	cfg := &Config{
		EnvdVersion:         "v1.0.0",
		EnableAuth:          true,
		E2BPort:             getEnvOrDefault("E2B_PORT", "8081"),
		E2BAPIKeySecret:     getEnvOrDefault("E2B_API_KEY_SECRET", "e2b-api-keys"),
		E2BAPIKeyConfigMap:  getEnvOrDefault("E2B_API_KEY_CONFIGMAP", "e2b-api-key-config"),
		E2BDefaultTTL:       defaultTTL(),
		E2BDefaultNamespace: getEnvOrDefault("E2B_DEFAULT_NAMESPACE", "e2b-default"),
		E2BSandboxDomain:    getEnvOrDefault("E2B_SANDBOX_DOMAIN", "sb.e2b.app"),
	}
	cfg.AuthConfig = &AuthConfig{
		APIKeySecret:          cfg.E2BAPIKeySecret,
		APIKeySecretNamespace: getEnvOrDefault("E2B_API_KEY_SECRET_NAMESPACE", "agentcube-system"),
		APIKeyConfigMap:       cfg.E2BAPIKeyConfigMap,
	}
	return cfg
}

// defaultTTL reads E2B_DEFAULT_TTL from environment, falling back to 900.
func defaultTTL() int {
	if v := getEnvOrDefault("E2B_DEFAULT_TTL", ""); v != "" {
		if ttl, err := strconv.Atoi(v); err == nil && ttl > 0 {
			return ttl
		}
	}
	return 900
}

// NewServer creates a new E2B API server instance
func NewServer(router *gin.RouterGroup, storeClient store.Store, sessionManager SessionManager) (*Server, error) {
	return NewServerWithConfig(router, storeClient, sessionManager, DefaultConfig())
}

// NewServerWithConfig creates a new E2B API server with custom configuration
func NewServerWithConfig(router *gin.RouterGroup, storeClient store.Store, sessionManager SessionManager, config *Config) (*Server, error) {
	if router == nil {
		return nil, fmt.Errorf("router cannot be nil")
	}
	if storeClient == nil {
		return nil, fmt.Errorf("store client cannot be nil")
	}
	if sessionManager == nil {
		return nil, fmt.Errorf("session manager cannot be nil")
	}
	if config == nil {
		config = DefaultConfig()
	}

	server := &Server{
		router:         router,
		storeClient:    storeClient,
		sessionManager: sessionManager,
		mapper:         NewMapper(config.EnvdVersion, config.E2BSandboxDomain),
		config:         config,
		idGenerator:    NewIDGenerator(storeClient),
	}

	// Initialize authenticator if auth is enabled
	if config.EnableAuth {
		server.authenticator = NewAuthenticator(config.AuthConfig)
		if err := server.authenticator.LoadAPIKeys(); err != nil {
			klog.Warningf("failed to load API keys: %v", err)
		}
	}

	// Setup routes
	server.setupRoutes()

	return server, nil
}

// NewServerWithAuthenticator creates a new E2B API server with a custom authenticator (for testing)
func NewServerWithAuthenticator(router *gin.RouterGroup, storeClient store.Store, sessionManager SessionManager, authenticator *Authenticator) (*Server, error) {
	if router == nil {
		return nil, fmt.Errorf("router cannot be nil")
	}
	if storeClient == nil {
		return nil, fmt.Errorf("store client cannot be nil")
	}
	if sessionManager == nil {
		return nil, fmt.Errorf("session manager cannot be nil")
	}

	config := DefaultConfig()
	server := &Server{
		router:         router,
		storeClient:    storeClient,
		sessionManager: sessionManager,
		mapper:         NewMapper(config.EnvdVersion, config.E2BSandboxDomain),
		authenticator:  authenticator,
		config:         config,
		idGenerator:    NewIDGenerator(storeClient),
	}

	// Setup routes
	server.setupRoutes()

	return server, nil
}

// setupRoutes configures HTTP routes using Gin
func (s *Server) setupRoutes() {
	// Apply authentication middleware if enabled
	if s.config.EnableAuth && s.authenticator != nil {
		s.router.Use(s.authenticator.APIKeyMiddleware())
	}

	// Sandbox routes
	s.router.POST("/sandboxes", s.handleCreateSandbox)
	s.router.GET("/sandboxes", s.handleListSandboxes)
	s.router.GET("/v2/sandboxes", s.handleListSandboxes)
	s.router.GET("/sandboxes/:id", s.handleGetSandbox)
	s.router.DELETE("/sandboxes/:id", s.handleDeleteSandbox)
	s.router.POST("/sandboxes/:id/timeout", s.handleSetTimeout)
	s.router.POST("/sandboxes/:id/refreshes", s.handleRefreshSandbox)

	// Template routes
	// Using wildcard routes to support template IDs with slashes (e.g., "namespace/name")
	s.router.GET("/templates", s.handleListTemplates)
	s.router.POST("/templates", s.handleCreateTemplate)
	s.router.POST("/v3/templates", s.handleCreateTemplate)
	s.router.GET("/templates/*path", s.handleTemplateWildcard)
	s.router.PATCH("/templates/*path", s.handleTemplateWildcard)
	s.router.PATCH("/v2/templates/*path", s.handleTemplateWildcard)
	s.router.DELETE("/templates/*path", s.handleTemplateWildcard)
}

// GetStore returns the store client (used for testing)
func (s *Server) GetStore() store.Store {
	return s.storeClient
}

// GetSessionManager returns the session manager (used for testing)
func (s *Server) GetSessionManager() SessionManager {
	return s.sessionManager
}

// GetMapper returns the mapper (used for testing)
func (s *Server) GetMapper() *Mapper {
	return s.mapper
}

// SetK8sClient sets the Kubernetes client for template operations
func (s *Server) SetK8sClient(k8sClient client.Client) {
	s.k8sClient = k8sClient
}

// GetK8sClient returns the Kubernetes client (used for testing)
func (s *Server) GetK8sClient() client.Client {
	return s.k8sClient
}
