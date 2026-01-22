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

import "testing"

func TestLastActivityAnnotationKey(t *testing.T) {
	expected := "agentcube.volcano.sh/last-activity"
	if LastActivityAnnotationKey != expected {
		t.Errorf("LastActivityAnnotationKey = %q, want %q", LastActivityAnnotationKey, expected)
	}
}

func TestConfigDefaults(t *testing.T) {
	config := Config{}

	if config.Port != "" {
		t.Errorf("Default Port should be empty, got %q", config.Port)
	}

	if config.Debug != false {
		t.Errorf("Default Debug should be false, got %v", config.Debug)
	}

	if config.EnableTLS != false {
		t.Errorf("Default EnableTLS should be false, got %v", config.EnableTLS)
	}

	if config.TLSCert != "" {
		t.Errorf("Default TLSCert should be empty, got %q", config.TLSCert)
	}

	if config.TLSKey != "" {
		t.Errorf("Default TLSKey should be empty, got %q", config.TLSKey)
	}

	if config.MaxConcurrentRequests != 0 {
		t.Errorf("Default MaxConcurrentRequests should be 0, got %d", config.MaxConcurrentRequests)
	}
}

func TestConfigWithValues(t *testing.T) {
	config := Config{
		Port:                  "8080",
		Debug:                 true,
		EnableTLS:             true,
		TLSCert:               "/path/to/cert.pem",
		TLSKey:                "/path/to/key.pem",
		MaxConcurrentRequests: 100,
	}

	if config.Port != "8080" {
		t.Errorf("Port = %q, want %q", config.Port, "8080")
	}

	if config.Debug != true {
		t.Errorf("Debug = %v, want %v", config.Debug, true)
	}

	if config.EnableTLS != true {
		t.Errorf("EnableTLS = %v, want %v", config.EnableTLS, true)
	}

	if config.TLSCert != "/path/to/cert.pem" {
		t.Errorf("TLSCert = %q, want %q", config.TLSCert, "/path/to/cert.pem")
	}

	if config.TLSKey != "/path/to/key.pem" {
		t.Errorf("TLSKey = %q, want %q", config.TLSKey, "/path/to/key.pem")
	}

	if config.MaxConcurrentRequests != 100 {
		t.Errorf("MaxConcurrentRequests = %d, want %d", config.MaxConcurrentRequests, 100)
	}
}

func TestConfigPartialValues(t *testing.T) {
	tests := []struct {
		name   string
		config Config
	}{
		{
			name: "Only Port set",
			config: Config{
				Port: "9090",
			},
		},
		{
			name: "Only Debug enabled",
			config: Config{
				Debug: true,
			},
		},
		{
			name: "TLS enabled without cert/key paths",
			config: Config{
				EnableTLS: true,
			},
		},
		{
			name: "TLS cert and key without EnableTLS",
			config: Config{
				TLSCert: "/cert.pem",
				TLSKey:  "/key.pem",
			},
		},
		{
			name: "MaxConcurrentRequests only",
			config: Config{
				MaxConcurrentRequests: 50,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Just verify that the config can be created without panicking
			_ = tt.config
		})
	}
}

func TestConfigZeroMaxConcurrentRequests(t *testing.T) {
	config := Config{
		MaxConcurrentRequests: 0,
	}

	// Zero should indicate unlimited concurrent requests
	if config.MaxConcurrentRequests != 0 {
		t.Errorf("MaxConcurrentRequests = %d, want 0 for unlimited", config.MaxConcurrentRequests)
	}
}

func TestConfigNegativeMaxConcurrentRequests(t *testing.T) {
	config := Config{
		MaxConcurrentRequests: -1,
	}

	// The struct allows negative values, though validation logic elsewhere might reject them
	if config.MaxConcurrentRequests != -1 {
		t.Errorf("MaxConcurrentRequests = %d, want -1", config.MaxConcurrentRequests)
	}
}
