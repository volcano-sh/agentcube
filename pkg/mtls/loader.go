package mtls

import (
    "crypto/tls"
    "crypto/x509"
    "fmt"
    "os"
)

// LoadServerConfig creates a tls.Config for a server that requires and verifies client certificates (mTLS).
func LoadServerConfig(cfg *CertSourceConfig) (*tls.Config, error) {
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
func LoadClientConfig(cfg *CertSourceConfig) (*tls.Config, error) {
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
func loadCertAndCA(cfg *CertSourceConfig) (tls.Certificate, *x509.CertPool, error) {
    // Resolve paths (apply SPIRE defaults if needed)
    resolved := resolveConfig(cfg)

    cert, err := tls.LoadX509KeyPair(resolved.CertFile, resolved.KeyFile)
    if err != nil {
        return tls.Certificate{}, nil, fmt.Errorf("load cert/key pair (%s, %s): %w",
            resolved.CertFile, resolved.KeyFile, err)
    }

    caCert, err := os.ReadFile(resolved.CAFile)
    if err != nil {
        return tls.Certificate{}, nil, fmt.Errorf("read CA file %s: %w", resolved.CAFile, err)
    }

    caPool := x509.NewCertPool()
    if !caPool.AppendCertsFromPEM(caCert) {
        return tls.Certificate{}, nil, fmt.Errorf("no valid CA certificates found in %s", resolved.CAFile)
    }

    return cert, caPool, nil
}

// resolveConfig fills in SPIRE default paths if source is SPIRE and paths are empty.
func resolveConfig(cfg *CertSourceConfig) *CertSourceConfig {
    if cfg.Source != CertSourceSPIRE {
        return cfg
    }
    defaults := DefaultSPIREPaths()
    resolved := *cfg
    if resolved.CertFile == "" {
        resolved.CertFile = defaults.CertFile
    }
    if resolved.KeyFile == "" {
        resolved.KeyFile = defaults.KeyFile
    }
    if resolved.CAFile == "" {
        resolved.CAFile = defaults.CAFile
    }
    return &resolved
}
