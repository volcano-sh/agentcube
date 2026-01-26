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

package router

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

func TestLastActivityAnnotationKey(t *testing.T) {
	tests := []struct {
		name      string
		key       string
		wantValid bool
	}{
		{
			name:      "follows kubernetes annotation naming convention",
			key:       LastActivityAnnotationKey,
			wantValid: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Validate format: must contain '/' separating domain and key
			parts := strings.Split(tt.key, "/")
			if len(parts) != 2 {
				t.Errorf("annotation key must be in format 'domain/key', got: %s", tt.key)
				return
			}
			domain, key := parts[0], parts[1]

			// Validate domain has dot (kubernetes requirement for domain annotations)
			if !strings.Contains(domain, ".") {
				t.Errorf("annotation domain must contain '.', got: %s", domain)
			}

			// Validate key is not empty
			if key == "" {
				t.Errorf("annotation key part cannot be empty")
			}

			// Validate key matches expected pattern (lowercase alphanumeric, hyphens, dots)
			// Kubernetes annotation keys can contain: lowercase alphanumeric, hyphens, dots
			if !regexp.MustCompile(`^[a-z0-9.-]+$`).MatchString(key) {
				t.Errorf("annotation key must contain only lowercase alphanumeric, hyphens, and dots, got: %s", key)
			}

			// Validate domain matches expected pattern
			if !regexp.MustCompile(`^[a-z0-9.-]+$`).MatchString(domain) {
				t.Errorf("annotation domain must contain only lowercase alphanumeric, hyphens, and dots, got: %s", domain)
			}
		})
	}
}

func TestConfig(t *testing.T) {
	tests := []struct {
		name        string
		config      Config
		description string
	}{
		{
			name:        "default configuration",
			config:      Config{},
			description: "zero values should be valid (represents defaults handled in server)",
		},
		{
			name: "http configuration",
			config: Config{
				Port:                  "8080",
				Debug:                 false,
				EnableTLS:             false,
				MaxConcurrentRequests: 100,
			},
			description: "standard HTTP server configuration",
		},
		{
			name: "https configuration with TLS",
			config: Config{
				Port:                  "8443",
				Debug:                 false,
				EnableTLS:             true,
				TLSCert:               "/path/to/cert.pem",
				TLSKey:                "/path/to/key.pem",
				MaxConcurrentRequests: 50,
			},
			description: "HTTPS server with TLS enabled",
		},
		{
			name: "unlimited concurrent requests",
			config: Config{
				Port:                  "8080",
				MaxConcurrentRequests: 0,
			},
			description: "MaxConcurrentRequests=0 means unlimited",
		},
		{
			name: "debug mode enabled",
			config: Config{
				Port:  "8080",
				Debug: true,
			},
			description: "debug mode for development",
		},
		{
			name: "negative concurrent requests",
			config: Config{
				Port:                  "8080",
				MaxConcurrentRequests: -1,
			},
			description: "negative values allowed (validation happens in server.go)",
		},
		{
			name: "empty port string",
			config: Config{
				Port: "",
			},
			description: "empty port string allowed (validation happens in server.go)",
		},
		{
			name: "TLS enabled without cert paths",
			config: Config{
				Port:      "8443",
				EnableTLS: true,
				TLSCert:   "",
				TLSKey:    "",
			},
			description: "TLS enabled but cert/key empty (validation happens in server.go)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test that Config can be JSON serialized (common use case for config files, APIs)
			data, err := json.Marshal(tt.config)
			if err != nil {
				t.Errorf("Config should be JSON serializable, got error: %v", err)
				return
			}

			// Test that Config can be JSON deserialized
			var decoded Config
			if err := json.Unmarshal(data, &decoded); err != nil {
				t.Errorf("Config should be JSON deserializable, got error: %v", err)
				return
			}

			// Verify all fields are preserved through marshal/unmarshal cycle
			if decoded.Port != tt.config.Port {
				t.Errorf("Port not preserved: got %q, want %q", decoded.Port, tt.config.Port)
			}
			if decoded.Debug != tt.config.Debug {
				t.Errorf("Debug not preserved: got %v, want %v", decoded.Debug, tt.config.Debug)
			}
			if decoded.EnableTLS != tt.config.EnableTLS {
				t.Errorf("EnableTLS not preserved: got %v, want %v", decoded.EnableTLS, tt.config.EnableTLS)
			}
			if decoded.TLSCert != tt.config.TLSCert {
				t.Errorf("TLSCert not preserved: got %q, want %q", decoded.TLSCert, tt.config.TLSCert)
			}
			if decoded.TLSKey != tt.config.TLSKey {
				t.Errorf("TLSKey not preserved: got %q, want %q", decoded.TLSKey, tt.config.TLSKey)
			}
			if decoded.MaxConcurrentRequests != tt.config.MaxConcurrentRequests {
				t.Errorf("MaxConcurrentRequests not preserved: got %d, want %d",
					decoded.MaxConcurrentRequests, tt.config.MaxConcurrentRequests)
			}
		})
	}
}

func TestConfigUsage(t *testing.T) {
	// This test documents that Config is a simple data structure (DTO pattern).
	// Actual validation and default value handling occurs in:
	// - server.go: NewServer() function (validates nil, sets defaults for MaxConcurrentRequests)
	// - server_test.go: TestNewServer tests (tests validation logic)
	//
	// Config itself has no validation logic, which is intentional.
	// This keeps Config as a pure data transfer object, allowing validation
	// to be handled at the appropriate layer (server initialization).

	t.Run("config is a simple data transfer object", func(t *testing.T) {
		// Verify Config can be created without any initialization
		config := Config{
			Port:                  "8080",
			Debug:                 true,
			EnableTLS:             false,
			MaxConcurrentRequests: 100,
		}

		// Verify it can be used in common scenarios (e.g., passed to functions)
		_ = config

		// This test documents that Config is intentionally simple.
		// Complex validation logic belongs in server.go where Config is consumed.
	})
}
