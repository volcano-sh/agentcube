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

package mtls

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
)

// LoadServerConfig returns a tls.Config for a server that requires mTLS.
// Certs and the CA bundle are loaded dynamically via CertWatcher to support
// zero-downtime rotation of both identity certificates and trust bundles.
// If expectedClientIDs is non-empty, clients must present a matching SPIFFE ID.
func LoadServerConfig(cfg *Config, expectedClientIDs []string) (*tls.Config, *CertWatcher, error) {
	if cfg == nil {
		return nil, nil, fmt.Errorf("server TLS config: mtls.Config is nil")
	}
	if cfg.CAFile == "" {
		return nil, nil, fmt.Errorf("server TLS config: CAFile is required for mTLS verification")
	}

	watcher, err := NewCertWatcher(cfg.CertFile, cfg.KeyFile, cfg.CAFile)
	if err != nil {
		return nil, nil, fmt.Errorf("server TLS cert watcher: %w", err)
	}

	tlsCfg := &tls.Config{
		GetCertificate: watcher.GetCertificate,
		// Use RequireAnyClientCert instead of RequireAndVerifyClientCert so we can
		// verify the chain against the dynamically-reloaded CA pool in VerifyPeerCertificate.
		ClientAuth: tls.RequireAnyClientCert,
		MinVersion: tls.VersionTLS13,
		// Manually verify the client certificate chain and SPIFFE ID against the
		// watcher's dynamic CA pool, enabling trust bundle rotation without restart.
		VerifyPeerCertificate: verifyClientCert(watcher, expectedClientIDs),
	}

	return tlsCfg, watcher, nil
}

// LoadClientConfig returns a tls.Config for a client that presents its cert and verifies the server.
// Certs and the CA bundle are loaded dynamically via CertWatcher to support
// zero-downtime rotation of both identity certificates and trust bundles.
//
// The expectedServerID is required; the server must present a matching SPIFFE ID in its URI SAN.
// This sets InsecureSkipVerify=true to bypass standard DNS hostname checking
// and manually verifies the cryptographic chain and the SPIFFE ID instead.
func LoadClientConfig(cfg *Config, expectedServerID string) (*tls.Config, *CertWatcher, error) {
	if expectedServerID == "" {
		return nil, nil, fmt.Errorf("client TLS config: expectedServerID is required for SPIFFE verification")
	}

	if cfg == nil {
		return nil, nil, fmt.Errorf("client TLS config: mtls.Config is nil")
	}
	if cfg.CAFile == "" {
		return nil, nil, fmt.Errorf("client TLS config: CAFile is required for mTLS verification")
	}

	watcher, err := NewCertWatcher(cfg.CertFile, cfg.KeyFile, cfg.CAFile)
	if err != nil {
		return nil, nil, fmt.Errorf("client TLS cert watcher: %w", err)
	}

	tlsCfg := &tls.Config{
		GetClientCertificate: watcher.GetClientCertificate,
		MinVersion:           tls.VersionTLS13,
		//nolint:gosec // G402: we use VerifyPeerCertificate for custom SPIFFE ID verification instead
		InsecureSkipVerify: true,
		// Manually verify the server certificate chain and SPIFFE ID against the
		// watcher's dynamic CA pool, enabling trust bundle rotation without restart.
		VerifyPeerCertificate: verifyServerCert(watcher, expectedServerID),
	}

	return tlsCfg, watcher, nil
}

// verifyClientCert verifies the client's certificate chain against the dynamic CA pool
// and optionally checks the client's SPIFFE ID. Used server-side where ClientAuth is
// RequireAnyClientCert (chain verification is done manually, not by the TLS stack).
func verifyClientCert(watcher *CertWatcher, expectedIDs []string) func([][]byte, [][]*x509.Certificate) error {
	return func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
		if len(rawCerts) == 0 {
			return fmt.Errorf("no client certificate presented")
		}

		peerCert, err := x509.ParseCertificate(rawCerts[0])
		if err != nil {
			return fmt.Errorf("parse client certificate: %w", err)
		}

		intermediates := x509.NewCertPool()
		for i, rawCert := range rawCerts[1:] {
			ic, err := x509.ParseCertificate(rawCert)
			if err != nil {
				return fmt.Errorf("parse client intermediate certificate %d: %w", i+1, err)
			}
			intermediates.AddCert(ic)
		}

		caPool, err := requireCAPool(watcher)
		if err != nil {
			return fmt.Errorf("verify client certificate chain: %w", err)
		}
		opts := x509.VerifyOptions{
			Roots:         caPool,
			Intermediates: intermediates,
			KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		}
		if _, err := peerCert.Verify(opts); err != nil {
			return fmt.Errorf("verify client certificate chain: %w", err)
		}

		if len(expectedIDs) > 0 {
			for _, uri := range peerCert.URIs {
				for _, expected := range expectedIDs {
					if uri.String() == expected {
						return nil
					}
				}
			}
			return fmt.Errorf("client certificate SPIFFE ID does not match any expected ID %v", expectedIDs)
		}

		return nil
	}
}

// verifyServerCert verifies the server's certificate chain against the dynamic CA pool
// and checks the server's SPIFFE ID. Used client-side where InsecureSkipVerify is true
// (chain verification and SPIFFE ID check are done manually).
func verifyServerCert(watcher *CertWatcher, expectedID string) func([][]byte, [][]*x509.Certificate) error {
	return func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
		if len(rawCerts) == 0 {
			return fmt.Errorf("no server certificate presented")
		}

		peerCert, err := x509.ParseCertificate(rawCerts[0])
		if err != nil {
			return fmt.Errorf("parse server certificate: %w", err)
		}

		intermediates := x509.NewCertPool()
		for i, rawCert := range rawCerts[1:] {
			intermediateCert, err := x509.ParseCertificate(rawCert)
			if err != nil {
				return fmt.Errorf("parse server intermediate certificate %d: %w", i+1, err)
			}
			intermediates.AddCert(intermediateCert)
		}

		caPool, err := requireCAPool(watcher)
		if err != nil {
			return fmt.Errorf("verify server certificate chain: %w", err)
		}
		opts := x509.VerifyOptions{
			Roots:         caPool,
			Intermediates: intermediates,
			KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		}
		if _, err := peerCert.Verify(opts); err != nil {
			return fmt.Errorf("verify server certificate chain: %w", err)
		}

		for _, uri := range peerCert.URIs {
			if uri.String() == expectedID {
				return nil
			}
		}
		return fmt.Errorf("server certificate SPIFFE ID does not match expected %q", expectedID)
	}
}

func requireCAPool(watcher *CertWatcher) (*x509.CertPool, error) {
	caPool := watcher.GetCAPool()
	if caPool == nil {
		return nil, fmt.Errorf("CA pool is required for mTLS verification")
	}
	return caPool, nil
}
