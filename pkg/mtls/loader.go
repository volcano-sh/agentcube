package mtls

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
)

// LoadServerConfig returns a tls.Config for a server that requires mTLS.
// Certs are loaded dynamically via GetCertificate to support rotation.
// If expectedClientIDs is non-empty, clients must present a matching SPIFFE ID.
func LoadServerConfig(cfg *Config, expectedClientIDs []string) (*tls.Config, error) {
	caPool, err := loadCAPool(cfg.CAFile)
	if err != nil {
		return nil, fmt.Errorf("server TLS config: %w", err)
	}

	tlsCfg := &tls.Config{
		GetCertificate: func(_ *tls.ClientHelloInfo) (*tls.Certificate, error) {
			cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
			if err != nil {
				return nil, err
			}
			return &cert, nil
		},
		ClientCAs:  caPool,
		ClientAuth: tls.RequireAndVerifyClientCert,
		MinVersion: tls.VersionTLS12,
	}

	if len(expectedClientIDs) > 0 {
		tlsCfg.VerifyPeerCertificate = verifyPeerSPIFFEID(expectedClientIDs)
	}

	return tlsCfg, nil
}

// LoadClientConfig returns a tls.Config for a client that presents its cert and verifies the server.
// Certs are loaded dynamically via GetClientCertificate to support rotation.
// If expectedServerID is non-empty, the server must present a matching SPIFFE ID.
// This sets InsecureSkipVerify=true to bypass hostname checking (SPIFFE uses URI SANs)
// and manually verifies the chain + SPIFFE ID instead.
func LoadClientConfig(cfg *Config, expectedServerID string) (*tls.Config, error) {
	caPool, err := loadCAPool(cfg.CAFile)
	if err != nil {
		return nil, fmt.Errorf("client TLS config: %w", err)
	}

	tlsCfg := &tls.Config{
		GetClientCertificate: func(_ *tls.CertificateRequestInfo) (*tls.Certificate, error) {
			cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
			if err != nil {
				return nil, err
			}
			return &cert, nil
		},
		RootCAs:    caPool,
		MinVersion: tls.VersionTLS12,
	}

	if expectedServerID != "" {
		tlsCfg.InsecureSkipVerify = true
		tlsCfg.VerifyPeerCertificate = verifyPeerChainAndSPIFFEID(caPool, expectedServerID)
	}

	return tlsCfg, nil
}

// verifyPeerSPIFFEID checks the peer's URI SAN against expected SPIFFE IDs.
// Used server-side where the stdlib already verified the chain.
func verifyPeerSPIFFEID(expectedIDs []string) func([][]byte, [][]*x509.Certificate) error {
	return func(_ [][]byte, verifiedChains [][]*x509.Certificate) error {
		if len(verifiedChains) == 0 || len(verifiedChains[0]) == 0 {
			return fmt.Errorf("no verified peer certificate")
		}
		peerCert := verifiedChains[0][0]
		for _, uri := range peerCert.URIs {
			for _, expected := range expectedIDs {
				if uri.String() == expected {
					return nil
				}
			}
		}
		return fmt.Errorf("peer certificate SPIFFE ID does not match any expected ID %v", expectedIDs)
	}
}

// verifyPeerChainAndSPIFFEID manually verifies the chain against the CA pool
// and checks the SPIFFE ID. Used client-side where InsecureSkipVerify is true.
func verifyPeerChainAndSPIFFEID(caPool *x509.CertPool, expectedID string) func([][]byte, [][]*x509.Certificate) error {
	return func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
		if len(rawCerts) == 0 {
			return fmt.Errorf("no peer certificate presented")
		}

		peerCert, err := x509.ParseCertificate(rawCerts[0])
		if err != nil {
			return fmt.Errorf("parse peer certificate: %w", err)
		}

		opts := x509.VerifyOptions{Roots: caPool}
		if _, err := peerCert.Verify(opts); err != nil {
			return fmt.Errorf("verify peer certificate chain: %w", err)
		}

		for _, uri := range peerCert.URIs {
			if uri.String() == expectedID {
				return nil
			}
		}
		return fmt.Errorf("server certificate SPIFFE ID does not match expected %q", expectedID)
	}
}

// loadCAPool reads and parses the CA certificate file into a CertPool.
func loadCAPool(caFile string) (*x509.CertPool, error) {
	caCert, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("read CA file %s: %w", caFile, err)
	}

	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(caCert) {
		return nil, fmt.Errorf("no valid CA certificates found in %s", caFile)
	}

	return caPool, nil
}
