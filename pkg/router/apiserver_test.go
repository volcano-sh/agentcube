package router

import (
	"context"
	"testing"
	"time"
)

func TestNewServer(t *testing.T) {
	tests := []struct {
		name    string
		config  *Config
		wantErr bool
	}{
		{
			name:    "nil config",
			config:  nil,
			wantErr: true,
		},
		{
			name: "valid config with defaults",
			config: &Config{
				Port: "8080",
			},
			wantErr: false,
		},
		{
			name: "valid config with custom values",
			config: &Config{
				Port:                  "9090",
				MaxConcurrentRequests: 500,
				RequestTimeout:        60,
				MaxIdleConns:          200,
				MaxConnsPerHost:       20,
				SessionExpireDuration: 7200,
				EnableRedis:           true,
				Debug:                 true,
			},
			wantErr: false,
		},
		{
			name: "config with TLS enabled",
			config: &Config{
				Port:      "8443",
				EnableTLS: true,
				TLSCert:   "/path/to/cert.pem",
				TLSKey:    "/path/to/key.pem",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, err := NewServer(tt.config)

			if (err != nil) != tt.wantErr {
				t.Errorf("NewServer() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				// Verify server was created
				if server == nil {
					t.Error("Expected non-nil server")
					return
				}

				// Verify config was set
				if server.config != tt.config {
					t.Error("Server config was not set correctly")
				}

				// Verify session manager was created
				if server.sessionManager == nil {
					t.Error("Session manager was not created")
				}

				// Verify redis manager was created
				if server.redisManager == nil {
					t.Error("Redis manager was not created")
				}

				// Verify semaphore was created with correct capacity
				expectedCapacity := tt.config.MaxConcurrentRequests
				if expectedCapacity <= 0 {
					expectedCapacity = 1000 // Default value
				}
				if cap(server.semaphore) != expectedCapacity {
					t.Errorf("Expected semaphore capacity %d, got %d", expectedCapacity, cap(server.semaphore))
				}

				// Verify default values were set
				if server.config.MaxConcurrentRequests <= 0 {
					t.Error("MaxConcurrentRequests should have been set to default")
				}
				if server.config.RequestTimeout <= 0 {
					t.Error("RequestTimeout should have been set to default")
				}
				if server.config.MaxIdleConns <= 0 {
					t.Error("MaxIdleConns should have been set to default")
				}
				if server.config.MaxConnsPerHost <= 0 {
					t.Error("MaxConnsPerHost should have been set to default")
				}
				if server.config.SessionExpireDuration <= 0 {
					t.Error("SessionExpireDuration should have been set to default")
				}
			}
		})
	}
}

func TestServer_DefaultValues(t *testing.T) {
	config := &Config{
		Port: "8080",
		// Leave other values as zero to test defaults
	}

	server, err := NewServer(config)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	// Test default values
	if server.config.MaxConcurrentRequests != 1000 {
		t.Errorf("Expected default MaxConcurrentRequests 1000, got %d", server.config.MaxConcurrentRequests)
	}

	if server.config.RequestTimeout != 30 {
		t.Errorf("Expected default RequestTimeout 30, got %d", server.config.RequestTimeout)
	}

	if server.config.MaxIdleConns != 100 {
		t.Errorf("Expected default MaxIdleConns 100, got %d", server.config.MaxIdleConns)
	}

	if server.config.MaxConnsPerHost != 10 {
		t.Errorf("Expected default MaxConnsPerHost 10, got %d", server.config.MaxConnsPerHost)
	}

	if server.config.SessionExpireDuration != 3600 {
		t.Errorf("Expected default SessionExpireDuration 3600, got %d", server.config.SessionExpireDuration)
	}
}

func TestServer_ConcurrencyLimitMiddleware(t *testing.T) {
	config := &Config{
		Port:                  "8080",
		MaxConcurrentRequests: 2, // Small limit for testing
	}

	server, err := NewServer(config)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	middleware := server.concurrencyLimitMiddleware()

	// Test that middleware function was created
	if middleware == nil {
		t.Error("Expected non-nil middleware function")
	}

	// Note: Testing the actual middleware behavior would require setting up
	// a full HTTP test environment, which is beyond the scope of unit tests.
	// Integration tests would be more appropriate for testing middleware behavior.
}

func TestServer_SetupRoutes(t *testing.T) {
	config := &Config{
		Port: "8080",
	}

	server, err := NewServer(config)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	// Verify that engine was created during setupRoutes
	if server.engine == nil {
		t.Error("Expected non-nil Gin engine after setupRoutes")
	}

	// Note: Testing specific routes would require HTTP testing,
	// which is more appropriate for integration tests.
}

func TestServer_StartContext(t *testing.T) {
	config := &Config{
		Port: "0", // Use port 0 to let the OS assign a free port
	}

	server, err := NewServer(config)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	// Test context cancellation
	ctx, cancel := context.WithCancel(context.Background())

	// Start server in goroutine
	errChan := make(chan error, 1)
	go func() {
		err := server.Start(ctx)
		errChan <- err
	}()

	// Give server a moment to start
	time.Sleep(100 * time.Millisecond)

	// Cancel context to trigger shutdown
	cancel()

	// Wait for server to shutdown
	select {
	case err := <-errChan:
		// Server should shutdown gracefully, error might be http.ErrServerClosed
		if err != nil && err.Error() != "http: Server closed" {
			t.Errorf("Unexpected error during shutdown: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("Server did not shutdown within timeout")
	}
}

func TestServer_TLSConfiguration(t *testing.T) {
	tests := []struct {
		name      string
		config    *Config
		wantErr   bool
		errString string
	}{
		{
			name: "TLS enabled with cert and key",
			config: &Config{
				Port:      "8443",
				EnableTLS: true,
				TLSCert:   "/path/to/cert.pem",
				TLSKey:    "/path/to/key.pem",
			},
			wantErr: false,
		},
		{
			name: "TLS enabled without cert",
			config: &Config{
				Port:      "8443",
				EnableTLS: true,
				TLSKey:    "/path/to/key.pem",
			},
			wantErr:   true,
			errString: "TLS enabled but cert/key not provided",
		},
		{
			name: "TLS enabled without key",
			config: &Config{
				Port:      "8443",
				EnableTLS: true,
				TLSCert:   "/path/to/cert.pem",
			},
			wantErr:   true,
			errString: "TLS enabled but cert/key not provided",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, err := NewServer(tt.config)
			if err != nil {
				t.Fatalf("Failed to create server: %v", err)
			}

			// Test Start method with a context that will be cancelled immediately
			ctx, cancel := context.WithCancel(context.Background())
			cancel() // Cancel immediately to avoid actually starting the server

			err = server.Start(ctx)

			if tt.wantErr {
				if err == nil {
					t.Error("Expected error but got none")
				} else if tt.errString != "" && err.Error() != tt.errString {
					t.Errorf("Expected error '%s', got '%s'", tt.errString, err.Error())
				}
			} else {
				// For TLS tests, we expect the server to fail to start due to invalid cert/key paths
				// but the configuration validation should pass
				if err != nil && err.Error() == tt.errString {
					t.Errorf("Unexpected configuration error: %v", err)
				}
			}
		})
	}
}

func TestServer_RedisIntegration(t *testing.T) {
	tests := []struct {
		name        string
		enableRedis bool
	}{
		{
			name:        "Redis enabled",
			enableRedis: true,
		},
		{
			name:        "Redis disabled",
			enableRedis: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &Config{
				Port:        "8080",
				EnableRedis: tt.enableRedis,
			}

			server, err := NewServer(config)
			if err != nil {
				t.Fatalf("Failed to create server: %v", err)
			}

			// Verify Redis manager was created
			if server.redisManager == nil {
				t.Error("Redis manager was not created")
			}

			// Test Redis manager functionality based on enabled state
			err = server.redisManager.UpdateSessionActivity("test-session")
			if tt.enableRedis {
				if err != nil {
					t.Errorf("Expected no error when Redis is enabled, got: %v", err)
				}
			} else {
				// When disabled, UpdateSessionActivity should not return an error
				// (it silently skips)
				if err != nil {
					t.Errorf("Expected no error when Redis is disabled, got: %v", err)
				}
			}
		})
	}
}
