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
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"path/filepath"
	"testing"
	"time"
)

// generateMTLSTestPKI creates a shared CA plus two leaf certs (server and client),
// each with its own SPIFFE ID URI SAN. Both leaves are signed by the same CA
// so they share a trust domain — exactly what a real SPIRE deployment provides.
func generateMTLSTestPKI(t *testing.T, serverSPIFFEID, clientSPIFFEID string) (
	serverCert, serverKey, clientCert, clientKey, caFile string,
) {
	t.Helper()
	dir := t.TempDir()

	// --- Shared CA ---
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
		t.Fatalf("write CA: %v", err)
	}
	caCert, err := x509.ParseCertificate(caCertDER)
	if err != nil {
		t.Fatalf("parse CA cert: %v", err)
	}

	// helper to create a leaf cert signed by the shared CA
	makeLeaf := func(name, spiffeID string, serial int64) (certPath, keyPath string) {
		leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatalf("generate %s key: %v", name, err)
		}
		leafTemplate := &x509.Certificate{
			SerialNumber: big.NewInt(serial),
			Subject:      pkix.Name{Organization: []string{"Test " + name}},
			NotBefore:    time.Now().Add(-1 * time.Hour),
			NotAfter:     time.Now().Add(24 * time.Hour),
			KeyUsage:     x509.KeyUsageDigitalSignature,
			ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		}
		if spiffeID != "" {
			u, err := url.Parse(spiffeID)
			if err != nil {
				t.Fatalf("parse SPIFFE URI for %s: %v", name, err)
			}
			leafTemplate.URIs = []*url.URL{u}
		}
		leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, caCert, &leafKey.PublicKey, caKey)
		if err != nil {
			t.Fatalf("create %s cert: %v", name, err)
		}
		certPath = filepath.Join(dir, name+"-cert.pem")
		if err := writePEM(certPath, "CERTIFICATE", leafDER); err != nil {
			t.Fatalf("write %s cert: %v", name, err)
		}
		keyPath = filepath.Join(dir, name+"-key.pem")
		keyDER, err := x509.MarshalECPrivateKey(leafKey)
		if err != nil {
			t.Fatalf("marshal %s key: %v", name, err)
		}
		if err := writePEM(keyPath, "EC PRIVATE KEY", keyDER); err != nil {
			t.Fatalf("write %s key: %v", name, err)
		}
		return certPath, keyPath
	}

	serverCert, serverKey = makeLeaf("server", serverSPIFFEID, 2)
	clientCert, clientKey = makeLeaf("client", clientSPIFFEID, 3)
	return
}

// --- mTLS E2E Tests ---

const (
	testServerSPIFFEID = "spiffe://cluster.local/ns/default/sa/server"
	testClientSPIFFEID = "spiffe://cluster.local/ns/default/sa/client"
)

// TestMTLS_E2E_SuccessfulHandshake verifies that a server configured with
// LoadServerConfig and a client configured with LoadClientConfig can
// complete a full mTLS handshake when both present valid SPIFFE certificates
// signed by the same CA.
func TestMTLS_E2E_SuccessfulHandshake(t *testing.T) {
	serverID := testServerSPIFFEID
	clientID := testClientSPIFFEID

	serverCert, serverKey, clientCert, clientKey, caFile := generateMTLSTestPKI(t, serverID, clientID)

	// Server requires the client to present the correct SPIFFE ID
	serverCfg := &Config{CertFile: serverCert, KeyFile: serverKey, CAFile: caFile}
	serverTLS, serverWatcher, err := LoadServerConfig(serverCfg, []string{clientID})
	if err != nil {
		t.Fatalf("LoadServerConfig: %v", err)
	}
	defer serverWatcher.Stop()

	ln, err := tls.Listen("tcp", "127.0.0.1:0", serverTLS)
	if err != nil {
		t.Fatalf("tls.Listen: %v", err)
	}
	defer ln.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/ping", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "pong")
	})
	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	// Client expects the server to present the correct SPIFFE ID
	clientCfg := &Config{CertFile: clientCert, KeyFile: clientKey, CAFile: caFile}
	clientTLS, clientWatcher, err := LoadClientConfig(clientCfg, serverID)
	if err != nil {
		t.Fatalf("LoadClientConfig: %v", err)
	}
	defer clientWatcher.Stop()

	httpClient := &http.Client{
		Transport: &http.Transport{TLSClientConfig: clientTLS},
		Timeout:   5 * time.Second,
	}
	resp, err := httpClient.Get(fmt.Sprintf("https://%s/ping", ln.Addr().String()))
	if err != nil {
		t.Fatalf("HTTPS GET /ping failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != "pong" {
		t.Errorf("expected 'pong', got %q", string(body))
	}
	t.Logf("mTLS handshake succeeded: server=%s, client=%s", serverID, clientID)
}

// TestMTLS_E2E_WrongClientSPIFFEID verifies that the server rejects a client
// whose SPIFFE ID does not match the server's expected list.
func TestMTLS_E2E_WrongClientSPIFFEID(t *testing.T) {
	serverID := testServerSPIFFEID
	actualClientID := "spiffe://cluster.local/ns/default/sa/wrong-client"
	expectedClientID := "spiffe://cluster.local/ns/default/sa/correct-client"

	serverCert, serverKey, clientCert, clientKey, caFile := generateMTLSTestPKI(t, serverID, actualClientID)

	// Server expects a DIFFERENT client ID than the one the client has
	serverCfg := &Config{CertFile: serverCert, KeyFile: serverKey, CAFile: caFile}
	serverTLS, serverWatcher, err := LoadServerConfig(serverCfg, []string{expectedClientID})
	if err != nil {
		t.Fatalf("LoadServerConfig: %v", err)
	}
	defer serverWatcher.Stop()

	ln, err := tls.Listen("tcp", "127.0.0.1:0", serverTLS)
	if err != nil {
		t.Fatalf("tls.Listen: %v", err)
	}
	defer ln.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/ping", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "pong")
	})
	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	clientCfg := &Config{CertFile: clientCert, KeyFile: clientKey, CAFile: caFile}
	clientTLS, clientWatcher, err := LoadClientConfig(clientCfg, serverID)
	if err != nil {
		t.Fatalf("LoadClientConfig: %v", err)
	}
	defer clientWatcher.Stop()

	httpClient := &http.Client{
		Transport: &http.Transport{TLSClientConfig: clientTLS},
		Timeout:   5 * time.Second,
	}
	resp, err := httpClient.Get(fmt.Sprintf("https://%s/ping", ln.Addr().String()))
	if err != nil {
		t.Logf("Connection correctly rejected: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		t.Error("expected rejection with wrong client SPIFFE ID, but got 200 OK")
	}
}

// TestMTLS_E2E_WrongServerSPIFFEID verifies that the client rejects a server
// whose SPIFFE ID does not match what the client expects.
func TestMTLS_E2E_WrongServerSPIFFEID(t *testing.T) {
	actualServerID := "spiffe://cluster.local/ns/default/sa/actual-server"
	expectedServerID := "spiffe://cluster.local/ns/default/sa/expected-server"
	clientID := testClientSPIFFEID

	serverCert, serverKey, clientCert, clientKey, caFile := generateMTLSTestPKI(t, actualServerID, clientID)

	// Server is set up normally (no client ID enforcement for this test)
	serverCfg := &Config{CertFile: serverCert, KeyFile: serverKey, CAFile: caFile}
	serverTLS, serverWatcher, err := LoadServerConfig(serverCfg, nil)
	if err != nil {
		t.Fatalf("LoadServerConfig: %v", err)
	}
	defer serverWatcher.Stop()

	ln, err := tls.Listen("tcp", "127.0.0.1:0", serverTLS)
	if err != nil {
		t.Fatalf("tls.Listen: %v", err)
	}
	defer ln.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/ping", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "pong")
	})
	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	// Client expects a DIFFERENT server SPIFFE ID
	clientCfg := &Config{CertFile: clientCert, KeyFile: clientKey, CAFile: caFile}
	clientTLS, clientWatcher, err := LoadClientConfig(clientCfg, expectedServerID)
	if err != nil {
		t.Fatalf("LoadClientConfig: %v", err)
	}
	defer clientWatcher.Stop()

	httpClient := &http.Client{
		Transport: &http.Transport{TLSClientConfig: clientTLS},
		Timeout:   5 * time.Second,
	}
	_, err = httpClient.Get(fmt.Sprintf("https://%s/ping", ln.Addr().String()))
	if err == nil {
		t.Fatal("expected client to reject server with wrong SPIFFE ID, but request succeeded")
	}
	t.Logf("Client correctly rejected server with wrong SPIFFE ID: %v", err)
}

// TestMTLS_E2E_UntrustedCA verifies that the handshake fails when the client
// and server certificates are signed by different CAs (no shared trust).
func TestMTLS_E2E_UntrustedCA(t *testing.T) {
	serverID := testServerSPIFFEID
	clientID := testClientSPIFFEID

	// Generate two completely independent PKIs (different CAs)
	serverCert, serverKey, _, _, serverCA := generateMTLSTestPKI(t, serverID, "spiffe://unused")
	_, _, clientCert, clientKey, clientCA := generateMTLSTestPKI(t, "spiffe://unused", clientID)

	// Server trusts its own CA
	serverCfg := &Config{CertFile: serverCert, KeyFile: serverKey, CAFile: serverCA}
	serverTLS, serverWatcher, err := LoadServerConfig(serverCfg, nil)
	if err != nil {
		t.Fatalf("LoadServerConfig: %v", err)
	}
	defer serverWatcher.Stop()

	ln, err := tls.Listen("tcp", "127.0.0.1:0", serverTLS)
	if err != nil {
		t.Fatalf("tls.Listen: %v", err)
	}
	defer ln.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/ping", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "pong")
	})
	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	// Client trusts a DIFFERENT CA
	clientCfg := &Config{CertFile: clientCert, KeyFile: clientKey, CAFile: clientCA}
	clientTLS, clientWatcher, err := LoadClientConfig(clientCfg, serverID)
	if err != nil {
		t.Fatalf("LoadClientConfig: %v", err)
	}
	defer clientWatcher.Stop()

	httpClient := &http.Client{
		Transport: &http.Transport{TLSClientConfig: clientTLS},
		Timeout:   5 * time.Second,
	}
	_, err = httpClient.Get(fmt.Sprintf("https://%s/ping", ln.Addr().String()))
	if err == nil {
		t.Fatal("expected handshake to fail with untrusted CA, but request succeeded")
	}
	t.Logf("Handshake correctly failed with untrusted CA: %v", err)
}

// TestMTLS_E2E_NoClientCert verifies that the server rejects a client that
// does not present any certificate (plain TLS, not mTLS).
func TestMTLS_E2E_NoClientCert(t *testing.T) {
	serverID := testServerSPIFFEID

	serverCert, serverKey, _, _, caFile := generateMTLSTestPKI(t, serverID, "spiffe://unused")

	serverCfg := &Config{CertFile: serverCert, KeyFile: serverKey, CAFile: caFile}
	serverTLS, serverWatcher, err := LoadServerConfig(serverCfg, nil)
	if err != nil {
		t.Fatalf("LoadServerConfig: %v", err)
	}
	defer serverWatcher.Stop()

	ln, err := tls.Listen("tcp", "127.0.0.1:0", serverTLS)
	if err != nil {
		t.Fatalf("tls.Listen: %v", err)
	}
	defer ln.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/ping", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "pong")
	})
	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	// Plain TLS client — no client cert, just trust the CA
	plainTLS := &tls.Config{
		RootCAs:    loadTestCAPool(t, caFile),
		MinVersion: tls.VersionTLS12,
	}

	httpClient := &http.Client{
		Transport: &http.Transport{TLSClientConfig: plainTLS},
		Timeout:   5 * time.Second,
	}
	_, err = httpClient.Get(fmt.Sprintf("https://%s/ping", ln.Addr().String()))
	if err == nil {
		t.Fatal("expected server to reject client without certificate, but request succeeded")
	}
	t.Logf("Server correctly rejected client with no certificate: %v", err)
}
