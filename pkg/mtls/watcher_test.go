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
	"testing"
	"time"
)

// generateWatcherTestCerts creates cert/key files suitable for the CertWatcher tests.
func generateWatcherTestCerts(t *testing.T, dir string) (certFile, keyFile string) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		t.Fatalf("Failed to generate test serial number: %v", err)
	}

	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject:      pkix.Name{Organization: []string{"Watcher Test"}},
		NotBefore:    time.Now().Add(-1 * time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}

	certFile = filepath.Join(dir, "cert.pem")
	keyFile = filepath.Join(dir, "key.pem")

	if err := writeWatcherPEM(certFile, "CERTIFICATE", certDER); err != nil {
		t.Fatalf("write cert: %v", err)
	}

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	if err := writeWatcherPEM(keyFile, "EC PRIVATE KEY", keyDER); err != nil {
		t.Fatalf("write key: %v", err)
	}

	return certFile, keyFile
}

func writeWatcherPEM(path, blockType string, data []byte) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return pem.Encode(f, &pem.Block{Type: blockType, Bytes: data})
}

func TestNewCertWatcher_InitialLoad(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := generateWatcherTestCerts(t, dir)

	cw, err := NewCertWatcher(certFile, keyFile, "")
	if err != nil {
		t.Fatalf("NewCertWatcher() error: %v", err)
	}
	defer cw.Stop()

	// GetCertificate should return a valid cert
	cert, err := cw.GetCertificate(nil)
	if err != nil {
		t.Fatalf("GetCertificate() error: %v", err)
	}
	if cert == nil {
		t.Fatal("GetCertificate() returned nil cert")
	}

	// GetClientCertificate should also work
	clientCert, err := cw.GetClientCertificate(nil)
	if err != nil {
		t.Fatalf("GetClientCertificate() error: %v", err)
	}
	if clientCert == nil {
		t.Fatal("GetClientCertificate() returned nil cert")
	}
}

func TestNewCertWatcher_FileUpdate(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := generateWatcherTestCerts(t, dir)

	cw, err := NewCertWatcher(certFile, keyFile, "")
	if err != nil {
		t.Fatalf("NewCertWatcher() error: %v", err)
	}
	defer cw.Stop()

	// Get the initial certificate
	initialCert, err := cw.GetCertificate(nil)
	if err != nil {
		t.Fatalf("GetCertificate() error: %v", err)
	}

	// Overwrite with new certs (different serial number → different cert)
	generateWatcherTestCerts(t, dir)

	// Wait for fsnotify to detect the change and reload, using robust polling
	var updatedCert *tls.Certificate
	timeout := time.After(2 * time.Second)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			t.Fatal("timeout waiting for watcher to reload updated certificate")
		case <-ticker.C:
			updatedCert, err = cw.GetCertificate(nil)
			if err != nil {
				t.Fatalf("GetCertificate() after update error: %v", err)
			}
			// Once the pointer changes, fsnotify has swapped the cert in the cache
			if initialCert != updatedCert {
				goto CertUpdated
			}
		}
	}

CertUpdated:
	if updatedCert == nil {
		t.Fatal("GetCertificate() returned nil after update")
	}
}

func TestNewCertWatcher_KubernetesSymlinkUpdate(t *testing.T) {
	dir := t.TempDir()

	version1 := filepath.Join(dir, "..2026_01_01_00_00_00.000000001")
	if err := os.Mkdir(version1, 0700); err != nil {
		t.Fatalf("create first version dir: %v", err)
	}
	generateWatcherTestCerts(t, version1)

	if err := os.Symlink(filepath.Base(version1), filepath.Join(dir, "..data")); err != nil {
		t.Fatalf("create ..data symlink: %v", err)
	}
	certFile := filepath.Join(dir, "cert.pem")
	keyFile := filepath.Join(dir, "key.pem")
	if err := os.Symlink(filepath.Join("..data", "cert.pem"), certFile); err != nil {
		t.Fatalf("create cert symlink: %v", err)
	}
	if err := os.Symlink(filepath.Join("..data", "key.pem"), keyFile); err != nil {
		t.Fatalf("create key symlink: %v", err)
	}

	cw, err := NewCertWatcher(certFile, keyFile, "")
	if err != nil {
		t.Fatalf("NewCertWatcher() error: %v", err)
	}
	defer cw.Stop()

	initialCert, err := cw.GetCertificate(nil)
	if err != nil {
		t.Fatalf("GetCertificate() error: %v", err)
	}

	version2 := filepath.Join(dir, "..2026_01_01_00_00_00.000000002")
	if err := os.Mkdir(version2, 0700); err != nil {
		t.Fatalf("create second version dir: %v", err)
	}
	generateWatcherTestCerts(t, version2)

	nextDataLink := filepath.Join(dir, "..data_tmp")
	if err := os.Symlink(filepath.Base(version2), nextDataLink); err != nil {
		t.Fatalf("create replacement ..data symlink: %v", err)
	}
	if err := os.Rename(nextDataLink, filepath.Join(dir, "..data")); err != nil {
		t.Fatalf("swap ..data symlink: %v", err)
	}

	var updatedCert *tls.Certificate
	timeout := time.After(2 * time.Second)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			t.Fatal("timeout waiting for watcher to reload certificate after symlink swap")
		case <-ticker.C:
			updatedCert, err = cw.GetCertificate(nil)
			if err != nil {
				t.Fatalf("GetCertificate() after symlink update error: %v", err)
			}
			if initialCert != updatedCert {
				return
			}
		}
	}
}

func TestCertWatcher_StopIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := generateWatcherTestCerts(t, dir)

	cw, err := NewCertWatcher(certFile, keyFile, "")
	if err != nil {
		t.Fatalf("NewCertWatcher() error: %v", err)
	}

	// Stop should be safe to call multiple times
	cw.Stop()
	cw.Stop()
	cw.Stop()
	// If we get here without panicking, the test passes
}

func TestNewCertWatcher_InvalidCertFile(t *testing.T) {
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "key.pem")

	// Create a valid key but no cert file
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	if err := writeWatcherPEM(keyFile, "EC PRIVATE KEY", keyDER); err != nil {
		t.Fatalf("write key: %v", err)
	}

	_, err = NewCertWatcher("/nonexistent/cert.pem", keyFile, "")
	if err == nil {
		t.Fatal("NewCertWatcher() expected error for nonexistent cert, got nil")
	}
}
