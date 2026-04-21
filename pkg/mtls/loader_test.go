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
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// generateTestCerts creates a self-signed CA and a leaf certificate signed by that CA.
func generateTestCerts(t *testing.T) (certFile, keyFile, caFile string) {
	t.Helper()
	return generateTestCertsWithSPIFFEID(t, "")
}

// generateTestCertsWithSPIFFEID creates a CA + leaf cert with an optional SPIFFE ID URI SAN.
func generateTestCertsWithSPIFFEID(t *testing.T, spiffeID string) (certFile, keyFile, caFile string) {
	t.Helper()
	dir := t.TempDir()

	// Generate CA
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{Organization: []string{"Test CA"}},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	caCertDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create CA cert: %v", err)
	}
	caFile = filepath.Join(dir, "ca.pem")
	if err := writePEM(caFile, "CERTIFICATE", caCertDER); err != nil {
		t.Fatalf("write CA file: %v", err)
	}
	caCert, err := x509.ParseCertificate(caCertDER)
	if err != nil {
		t.Fatalf("parse CA cert: %v", err)
	}

	// Generate leaf cert
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate leaf key: %v", err)
	}
	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{Organization: []string{"Test Leaf"}},
		NotBefore:    time.Now().Add(-1 * time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	if spiffeID != "" {
		u, err := url.Parse(spiffeID)
		if err != nil {
			t.Fatalf("parse SPIFFE ID URL: %v", err)
		}
		leafTemplate.URIs = []*url.URL{u}
	}
	leafCertDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create leaf cert: %v", err)
	}
	certFile = filepath.Join(dir, "cert.pem")
	if err := writePEM(certFile, "CERTIFICATE", leafCertDER); err != nil {
		t.Fatalf("write cert file: %v", err)
	}
	keyFile = filepath.Join(dir, "key.pem")
	keyDER, err := x509.MarshalECPrivateKey(leafKey)
	if err != nil {
		t.Fatalf("marshal leaf key: %v", err)
	}
	if err := writePEM(keyFile, "EC PRIVATE KEY", keyDER); err != nil {
		t.Fatalf("write key file: %v", err)
	}

	return certFile, keyFile, caFile
}

func writePEM(path, blockType string, data []byte) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return pem.Encode(f, &pem.Block{Type: blockType, Bytes: data})
}

// --- LoadServerConfig ---

func TestLoadServerConfig(t *testing.T) {
	certFile, keyFile, caFile := generateTestCerts(t)
	cfg := &Config{CertFile: certFile, KeyFile: keyFile, CAFile: caFile}

	tlsCfg, err := LoadServerConfig(cfg, nil)
	if err != nil {
		t.Fatalf("LoadServerConfig() error: %v", err)
	}
	if tlsCfg.ClientAuth != tls.RequireAndVerifyClientCert {
		t.Errorf("ClientAuth = %v, want RequireAndVerifyClientCert", tlsCfg.ClientAuth)
	}
	if tlsCfg.ClientCAs == nil {
		t.Error("ClientCAs is nil")
	}
	if tlsCfg.GetCertificate == nil {
		t.Error("GetCertificate callback is nil")
	}
	if tlsCfg.MinVersion != tls.VersionTLS12 {
		t.Errorf("MinVersion = %d, want TLS 1.2", tlsCfg.MinVersion)
	}
	// No SPIFFE IDs → no VerifyPeerCertificate
	if tlsCfg.VerifyPeerCertificate != nil {
		t.Error("VerifyPeerCertificate should be nil when no expectedClientIDs")
	}
}

func TestLoadClientConfig(t *testing.T) {
	certFile, keyFile, caFile := generateTestCerts(t)
	cfg := &Config{CertFile: certFile, KeyFile: keyFile, CAFile: caFile}

	tlsCfg, err := LoadClientConfig(cfg, "")
	if err != nil {
		t.Fatalf("LoadClientConfig() error: %v", err)
	}
	if tlsCfg.RootCAs == nil {
		t.Error("RootCAs is nil")
	}
	if tlsCfg.GetClientCertificate == nil {
		t.Error("GetClientCertificate callback is nil")
	}
	// No SPIFFE ID → InsecureSkipVerify false, no VerifyPeerCertificate
	if tlsCfg.InsecureSkipVerify {
		t.Error("InsecureSkipVerify should be false when no expectedServerID")
	}
}

func TestLoadClientConfig_WithSPIFFEID(t *testing.T) {
	certFile, keyFile, caFile := generateTestCerts(t)
	cfg := &Config{CertFile: certFile, KeyFile: keyFile, CAFile: caFile}

	tlsCfg, err := LoadClientConfig(cfg, "spiffe://cluster.local/ns/default/sa/wm")
	if err != nil {
		t.Fatalf("LoadClientConfig() error: %v", err)
	}
	if !tlsCfg.InsecureSkipVerify {
		t.Error("InsecureSkipVerify should be true when expectedServerID is set")
	}
	if tlsCfg.VerifyPeerCertificate == nil {
		t.Error("VerifyPeerCertificate should be set when expectedServerID provided")
	}
}

// --- Error cases ---

func TestLoadServerConfig_MissingCAFile(t *testing.T) {
	certFile, keyFile, _ := generateTestCerts(t)
	cfg := &Config{CertFile: certFile, KeyFile: keyFile, CAFile: "/nonexistent/ca.pem"}

	_, err := LoadServerConfig(cfg, nil)
	if err == nil {
		t.Fatal("expected error for missing CA file")
	}
	if !strings.Contains(err.Error(), "read CA file") {
		t.Errorf("error = %q, want mention of CA file", err.Error())
	}
}

func TestLoadServerConfig_InvalidCAPEM(t *testing.T) {
	certFile, keyFile, _ := generateTestCerts(t)
	invalidCA := filepath.Join(t.TempDir(), "bad_ca.pem")
	os.WriteFile(invalidCA, []byte("not valid PEM"), 0600)

	cfg := &Config{CertFile: certFile, KeyFile: keyFile, CAFile: invalidCA}

	_, err := LoadServerConfig(cfg, nil)
	if err == nil {
		t.Fatal("expected error for invalid CA PEM")
	}
	if !strings.Contains(err.Error(), "no valid CA certificates") {
		t.Errorf("error = %q, want mention of no valid CA", err.Error())
	}
}

// --- SPIFFE ID verification ---

func TestVerifyPeerSPIFFEID_MatchingID(t *testing.T) {
	spiffeID := "spiffe://cluster.local/ns/agentcube-system/sa/agentcube-router"
	certFile, _, caFile := generateTestCertsWithSPIFFEID(t, spiffeID)

	chains := verifyAndGetChains(t, certFile, caFile)
	verifyFn := verifyPeerSPIFFEID([]string{spiffeID})
	if err := verifyFn(nil, chains); err != nil {
		t.Errorf("should accept matching ID, got: %v", err)
	}
}

func TestVerifyPeerSPIFFEID_NonMatchingID(t *testing.T) {
	spiffeID := "spiffe://cluster.local/ns/agentcube-system/sa/agentcube-router"
	certFile, _, caFile := generateTestCertsWithSPIFFEID(t, spiffeID)

	chains := verifyAndGetChains(t, certFile, caFile)
	verifyFn := verifyPeerSPIFFEID([]string{"spiffe://cluster.local/sa/wrong"})
	if err := verifyFn(nil, chains); err == nil {
		t.Error("should reject non-matching SPIFFE ID")
	}
}

func TestVerifyPeerChainAndSPIFFEID_MatchingID(t *testing.T) {
	spiffeID := "spiffe://cluster.local/ns/agentcube-system/sa/workloadmanager"
	certFile, _, caFile := generateTestCertsWithSPIFFEID(t, spiffeID)

	rawCert := readRawCert(t, certFile)
	caPool := loadTestCAPool(t, caFile)

	verifyFn := verifyPeerChainAndSPIFFEID(caPool, spiffeID)
	if err := verifyFn([][]byte{rawCert}, nil); err != nil {
		t.Errorf("should accept matching ID, got: %v", err)
	}
}

func TestVerifyPeerChainAndSPIFFEID_UntrustedCA(t *testing.T) {
	spiffeID := "spiffe://cluster.local/ns/agentcube-system/sa/workloadmanager"
	certFile, _, _ := generateTestCertsWithSPIFFEID(t, spiffeID)

	// Use a DIFFERENT CA — chain verification should fail
	_, _, differentCAFile := generateTestCerts(t)
	differentPool := loadTestCAPool(t, differentCAFile)

	rawCert := readRawCert(t, certFile)
	verifyFn := verifyPeerChainAndSPIFFEID(differentPool, spiffeID)
	err := verifyFn([][]byte{rawCert}, nil)
	if err == nil {
		t.Error("should reject cert signed by untrusted CA")
	}
	if !strings.Contains(err.Error(), "verify peer certificate chain") {
		t.Errorf("error = %q, want chain verification error", err.Error())
	}
}

// --- test helpers ---

func verifyAndGetChains(t *testing.T, certFile, caFile string) [][]*x509.Certificate {
	t.Helper()
	certPEM, _ := os.ReadFile(certFile)
	block, _ := pem.Decode(certPEM)
	cert, _ := x509.ParseCertificate(block.Bytes)
	caPool := loadTestCAPool(t, caFile)
	chains, err := cert.Verify(x509.VerifyOptions{Roots: caPool})
	if err != nil {
		t.Fatalf("verify cert: %v", err)
	}
	return chains
}

func readRawCert(t *testing.T, certFile string) []byte {
	t.Helper()
	certPEM, _ := os.ReadFile(certFile)
	block, _ := pem.Decode(certPEM)
	return block.Bytes
}

func loadTestCAPool(t *testing.T, caFile string) *x509.CertPool {
	t.Helper()
	caPEM, _ := os.ReadFile(caFile)
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(caPEM)
	return pool
}
