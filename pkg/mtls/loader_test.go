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
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// generateTestCerts creates a self-signed CA and a leaf certificate signed by that CA.
// Returns paths to cert.pem, key.pem, and ca.pem in a temp directory.
func generateTestCerts(t *testing.T) (certFile, keyFile, caFile string) {
	t.Helper()
	dir := t.TempDir()

	// Generate CA key and certificate
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}

	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{Organization: []string{"Test CA"}},
		NotBefore:    time.Now().Add(-1 * time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		IsCA:         true,
		BasicConstraintsValid: true,
	}

	caCertDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create CA cert: %v", err)
	}

	// Write CA cert
	caFile = filepath.Join(dir, "ca.pem")
	if err := writePEM(caFile, "CERTIFICATE", caCertDER); err != nil {
		t.Fatalf("write CA file: %v", err)
	}

	// Parse CA cert for signing
	caCert, err := x509.ParseCertificate(caCertDER)
	if err != nil {
		t.Fatalf("parse CA cert: %v", err)
	}

	// Generate leaf key and certificate
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

	leafCertDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create leaf cert: %v", err)
	}

	// Write leaf cert
	certFile = filepath.Join(dir, "cert.pem")
	if err := writePEM(certFile, "CERTIFICATE", leafCertDER); err != nil {
		t.Fatalf("write cert file: %v", err)
	}

	// Write leaf key
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

func TestLoadServerConfig(t *testing.T) {
	certFile, keyFile, caFile := generateTestCerts(t)

	cfg := &CertSourceConfig{
		Source:   CertSourceFile,
		CertFile: certFile,
		KeyFile:  keyFile,
		CAFile:   caFile,
	}

	tlsCfg, err := LoadServerConfig(cfg)
	if err != nil {
		t.Fatalf("LoadServerConfig() error: %v", err)
	}

	if tlsCfg.ClientAuth != tls.RequireAndVerifyClientCert {
		t.Errorf("ClientAuth = %v, want RequireAndVerifyClientCert", tlsCfg.ClientAuth)
	}
	if tlsCfg.ClientCAs == nil {
		t.Error("ClientCAs is nil, want non-nil CA pool")
	}
	if len(tlsCfg.Certificates) != 1 {
		t.Errorf("Certificates length = %d, want 1", len(tlsCfg.Certificates))
	}
	if tlsCfg.MinVersion != tls.VersionTLS12 {
		t.Errorf("MinVersion = %d, want TLS 1.2 (%d)", tlsCfg.MinVersion, tls.VersionTLS12)
	}
}

func TestLoadClientConfig(t *testing.T) {
	certFile, keyFile, caFile := generateTestCerts(t)

	cfg := &CertSourceConfig{
		Source:   CertSourceFile,
		CertFile: certFile,
		KeyFile:  keyFile,
		CAFile:   caFile,
	}

	tlsCfg, err := LoadClientConfig(cfg)
	if err != nil {
		t.Fatalf("LoadClientConfig() error: %v", err)
	}

	if tlsCfg.RootCAs == nil {
		t.Error("RootCAs is nil, want non-nil CA pool")
	}
	if len(tlsCfg.Certificates) != 1 {
		t.Errorf("Certificates length = %d, want 1", len(tlsCfg.Certificates))
	}
	if tlsCfg.MinVersion != tls.VersionTLS12 {
		t.Errorf("MinVersion = %d, want TLS 1.2 (%d)", tlsCfg.MinVersion, tls.VersionTLS12)
	}
}

func TestLoadServerConfig_MissingCertFile(t *testing.T) {
	_, keyFile, caFile := generateTestCerts(t)

	cfg := &CertSourceConfig{
		Source:   CertSourceFile,
		CertFile: "/nonexistent/cert.pem",
		KeyFile:  keyFile,
		CAFile:   caFile,
	}

	_, err := LoadServerConfig(cfg)
	if err == nil {
		t.Fatal("LoadServerConfig() expected error for missing cert file, got nil")
	}
	if !strings.Contains(err.Error(), "cert/key pair") {
		t.Errorf("error = %q, want it to mention cert/key pair", err.Error())
	}
}

func TestLoadServerConfig_MissingKeyFile(t *testing.T) {
	certFile, _, caFile := generateTestCerts(t)

	cfg := &CertSourceConfig{
		Source:   CertSourceFile,
		CertFile: certFile,
		KeyFile:  "/nonexistent/key.pem",
		CAFile:   caFile,
	}

	_, err := LoadServerConfig(cfg)
	if err == nil {
		t.Fatal("LoadServerConfig() expected error for missing key file, got nil")
	}
	if !strings.Contains(err.Error(), "cert/key pair") {
		t.Errorf("error = %q, want it to mention cert/key pair", err.Error())
	}
}

func TestLoadServerConfig_MissingCAFile(t *testing.T) {
	certFile, keyFile, _ := generateTestCerts(t)

	cfg := &CertSourceConfig{
		Source:   CertSourceFile,
		CertFile: certFile,
		KeyFile:  keyFile,
		CAFile:   "/nonexistent/ca.pem",
	}

	_, err := LoadServerConfig(cfg)
	if err == nil {
		t.Fatal("LoadServerConfig() expected error for missing CA file, got nil")
	}
	if !strings.Contains(err.Error(), "read CA file") {
		t.Errorf("error = %q, want it to mention reading CA file", err.Error())
	}
}

func TestLoadServerConfig_InvalidCAPEM(t *testing.T) {
	certFile, keyFile, _ := generateTestCerts(t)

	// Create a CA file with invalid PEM content
	invalidCAFile := filepath.Join(t.TempDir(), "bad_ca.pem")
	if err := os.WriteFile(invalidCAFile, []byte("this is not valid PEM"), 0600); err != nil {
		t.Fatalf("write invalid CA file: %v", err)
	}

	cfg := &CertSourceConfig{
		Source:   CertSourceFile,
		CertFile: certFile,
		KeyFile:  keyFile,
		CAFile:   invalidCAFile,
	}

	_, err := LoadServerConfig(cfg)
	if err == nil {
		t.Fatal("LoadServerConfig() expected error for invalid CA PEM, got nil")
	}
	if !strings.Contains(err.Error(), "no valid CA certificates") {
		t.Errorf("error = %q, want it to mention no valid CA certificates", err.Error())
	}
}

func TestResolveConfig_SPIREDefaults(t *testing.T) {
	cfg := &CertSourceConfig{
		Source: CertSourceSPIRE,
		// All paths empty — should get defaults
	}

	resolved := resolveConfig(cfg)
	defaults := DefaultSPIREPaths()

	if resolved.CertFile != defaults.CertFile {
		t.Errorf("CertFile = %q, want %q", resolved.CertFile, defaults.CertFile)
	}
	if resolved.KeyFile != defaults.KeyFile {
		t.Errorf("KeyFile = %q, want %q", resolved.KeyFile, defaults.KeyFile)
	}
	if resolved.CAFile != defaults.CAFile {
		t.Errorf("CAFile = %q, want %q", resolved.CAFile, defaults.CAFile)
	}
}

func TestResolveConfig_SPIRECustomPaths(t *testing.T) {
	cfg := &CertSourceConfig{
		Source:   CertSourceSPIRE,
		CertFile: "/custom/cert.pem",
		KeyFile:  "/custom/key.pem",
		CAFile:   "/custom/ca.pem",
	}

	resolved := resolveConfig(cfg)

	if resolved.CertFile != "/custom/cert.pem" {
		t.Errorf("CertFile = %q, want /custom/cert.pem", resolved.CertFile)
	}
	if resolved.KeyFile != "/custom/key.pem" {
		t.Errorf("KeyFile = %q, want /custom/key.pem", resolved.KeyFile)
	}
	if resolved.CAFile != "/custom/ca.pem" {
		t.Errorf("CAFile = %q, want /custom/ca.pem", resolved.CAFile)
	}
}

func TestResolveConfig_FileModePassthrough(t *testing.T) {
	cfg := &CertSourceConfig{
		Source:   CertSourceFile,
		CertFile: "/my/cert.pem",
		KeyFile:  "/my/key.pem",
		CAFile:   "/my/ca.pem",
	}

	resolved := resolveConfig(cfg)

	// For file mode, resolveConfig should return the same config (no defaults applied)
	if resolved != cfg {
		t.Error("resolveConfig() should return same pointer for file mode")
	}
}
