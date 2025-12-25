package router

import "testing"

func TestConfig_Fields(t *testing.T) {
	config := &Config{
		Port:                  "8080",
		SandboxEndpoints:      []string{"http://localhost:9000"},
		Debug:                 true,
		EnableTLS:             true,
		TLSCert:               "/path/to/cert.pem",
		TLSKey:                "/path/to/key.pem",
		MaxConcurrentRequests: 100,
		RequestTimeout:        30,
		MaxIdleConns:          50,
		MaxConnsPerHost:       10,
	}

	if config.Port != "8080" {
		t.Errorf("Expected Port 8080, got %s", config.Port)
	}
	if len(config.SandboxEndpoints) != 1 {
		t.Errorf("Expected 1 SandboxEndpoint, got %d", len(config.SandboxEndpoints))
	}
	if !config.Debug {
		t.Error("Expected Debug true")
	}
	if !config.EnableTLS {
		t.Error("Expected EnableTLS true")
	}
	if config.TLSCert != "/path/to/cert.pem" {
		t.Errorf("Expected TLSCert /path/to/cert.pem, got %s", config.TLSCert)
	}
	if config.TLSKey != "/path/to/key.pem" {
		t.Errorf("Expected TLSKey /path/to/key.pem, got %s", config.TLSKey)
	}
	if config.MaxConcurrentRequests != 100 {
		t.Errorf("Expected MaxConcurrentRequests 100, got %d", config.MaxConcurrentRequests)
	}
	if config.RequestTimeout != 30 {
		t.Errorf("Expected RequestTimeout 30, got %d", config.RequestTimeout)
	}
	if config.MaxIdleConns != 50 {
		t.Errorf("Expected MaxIdleConns 50, got %d", config.MaxIdleConns)
	}
	if config.MaxConnsPerHost != 10 {
		t.Errorf("Expected MaxConnsPerHost 10, got %d", config.MaxConnsPerHost)
	}
}
