package mtls

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
)

// LoadServerConfig creates a tls.Config for a server that requires and verifies client certificates (mTLS).
func LoadServerConfig(cfg *Config) (*tls.Config, error) {
	cert, caPool, err := loadCertAndCA(cfg)
	if err != nil {
		return nil, fmt.Errorf("server TLS config: %w", err)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientCAs:    caPool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		MinVersion:   tls.VersionTLS12,
	}, nil
}

// LoadClientConfig creates a tls.Config for a client that presents its own cert and verifies the server.
func LoadClientConfig(cfg *Config) (*tls.Config, error) {
	cert, caPool, err := loadCertAndCA(cfg)
	if err != nil {
		return nil, fmt.Errorf("client TLS config: %w", err)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      caPool,
		MinVersion:   tls.VersionTLS12,
	}, nil
}

// loadCertAndCA loads the certificate key pair and CA pool from the config paths.
func loadCertAndCA(cfg *Config) (tls.Certificate, *x509.CertPool, error) {
	cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
	if err != nil {
		return tls.Certificate{}, nil, fmt.Errorf("load cert/key pair (%s, %s): %w",
			cfg.CertFile, cfg.KeyFile, err)
	}

	caCert, err := os.ReadFile(cfg.CAFile)
	if err != nil {
		return tls.Certificate{}, nil, fmt.Errorf("read CA file %s: %w", cfg.CAFile, err)
	}

	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(caCert) {
		return tls.Certificate{}, nil, fmt.Errorf("no valid CA certificates found in %s", cfg.CAFile)
	}

	return cert, caPool, nil
}
