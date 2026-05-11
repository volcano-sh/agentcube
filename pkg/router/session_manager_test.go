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

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/volcano-sh/agentcube/pkg/common/types"
	"github.com/volcano-sh/agentcube/pkg/mtls"
	"github.com/volcano-sh/agentcube/pkg/store"
)

// ---- fakes ----

type fakeStoreClient struct {
	sandbox        *types.SandboxInfo
	err            error
	called         bool
	lastSessionID  string
	lastContextNil bool
}

func (f *fakeStoreClient) GetSandboxBySessionID(ctx context.Context, sessionID string) (*types.SandboxInfo, error) {
	f.called = true
	f.lastSessionID = sessionID
	f.lastContextNil = ctx == nil
	return f.sandbox, f.err
}

func (f *fakeStoreClient) SetSessionLockIfAbsent(_ context.Context, _ string, _ time.Duration) (bool, error) {
	return false, nil
}

func (f *fakeStoreClient) BindSessionWithSandbox(_ context.Context, _ string, _ *types.SandboxInfo, _ time.Duration) error {
	return nil
}

func (f *fakeStoreClient) DeleteSessionBySandboxID(_ context.Context, _ string) error {
	return nil
}

func (f *fakeStoreClient) DeleteSandboxBySessionID(_ context.Context, _ string) error {
	return nil
}

func (f *fakeStoreClient) UpdateSandbox(_ context.Context, _ *types.SandboxInfo) error {
	return nil
}

func (f *fakeStoreClient) UpdateSessionLastActivity(_ context.Context, _ string, _ time.Time) error {
	return nil
}

func (f *fakeStoreClient) StoreSandbox(_ context.Context, _ *types.SandboxInfo) error {
	return nil
}

func (f *fakeStoreClient) Ping(_ context.Context) error {
	return nil
}

func (f *fakeStoreClient) ListExpiredSandboxes(_ context.Context, _ time.Time, _ int64) ([]*types.SandboxInfo, error) {
	return nil, nil
}

func (f *fakeStoreClient) ListInactiveSandboxes(_ context.Context, _ time.Time, _ int64) ([]*types.SandboxInfo, error) {
	return nil, nil
}

func (f *fakeStoreClient) UpdateSandboxLastActivity(_ context.Context, _ string, _ time.Time) error {
	return nil
}

func (f *fakeStoreClient) Close() error {
	return nil
}

// ---- tests: GetSandboxBySession ----

func TestGetSandboxBySession_Success(t *testing.T) {
	sb := &types.SandboxInfo{
		SandboxID: "sandbox-1",
		Name:      "sandbox-1",
		EntryPoints: []types.SandboxEntryPoint{
			{Endpoint: "10.0.0.1:9000"},
		},
		SessionID: "sess-1",
		Status:    "running",
	}

	r := &fakeStoreClient{
		sandbox: sb,
	}
	m := &manager{
		storeClient: r,
	}

	got, err := m.GetSandboxBySession(context.Background(), "sess-1", "default", "test", "AgentRuntime")
	if err != nil {
		t.Fatalf("GetSandboxBySession unexpected error: %v", err)
	}
	if !r.called {
		t.Fatalf("expected StoreClient to be called")
	}
	if r.lastSessionID != "sess-1" {
		t.Fatalf("expected StoreClient to be called with sessionID 'sess-1', got %q", r.lastSessionID)
	}
	if got == nil {
		t.Fatalf("expected non-nil sandbox")
	}
	if got.SandboxID != "sandbox-1" {
		t.Fatalf("unexpected SandboxID: got %q, want %q", got.SandboxID, "sandbox-1")
	}
}

func TestGetSandboxBySession_NotFound(t *testing.T) {
	r := &fakeStoreClient{
		sandbox: nil,
		err:     store.ErrNotFound,
	}
	m := &manager{
		storeClient: r,
	}

	_, err := m.GetSandboxBySession(context.Background(), "sess-1", "default", "test", "AgentRuntime")
	if err == nil {
		t.Fatalf("expected error for not found session")
	}
	if !apierrors.IsNotFound(err) {
		t.Fatalf("expected not found error, got %v", err)
	}
}

// ---- tests: GetSandboxBySession with empty sessionID (sandbox creation path) ----

func TestGetSandboxBySession_CreateSandbox_AgentRuntime_Success(t *testing.T) {
	// Mock workload manager server
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request method and path
		if r.Method != http.MethodPost {
			t.Errorf("expected POST request, got %s", r.Method)
		}
		if r.URL.Path != "/v1/agent-runtime" {
			t.Errorf("expected path /v1/agent-runtime, got %s", r.URL.Path)
		}

		// Verify request body
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("failed to read request body: %v", err)
		}
		var req types.CreateSandboxRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("failed to unmarshal request: %v", err)
		}
		if req.Kind != types.AgentRuntimeKind {
			t.Errorf("expected kind %s, got %s", types.AgentRuntimeKind, req.Kind)
		}
		if req.Name != "test-runtime" {
			t.Errorf("expected name test-runtime, got %s", req.Name)
		}
		if req.Namespace != "default" {
			t.Errorf("expected namespace default, got %s", req.Namespace)
		}

		// Send successful response
		resp := types.CreateSandboxResponse{
			SessionID:   "new-session-123",
			SandboxID:   "sandbox-456",
			SandboxName: "sandbox-test",
			EntryPoints: []types.SandboxEntryPoint{
				{Endpoint: "10.0.0.1:9000", Protocol: "http", Path: "/"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer mockServer.Close()

	r := &fakeStoreClient{}
	m := &manager{
		storeClient:     r,
		workloadMgrAddr: mockServer.URL,
		httpClient:      &http.Client{},
	}

	sandbox, err := m.GetSandboxBySession(context.Background(), "", "default", "test-runtime", types.AgentRuntimeKind)
	if err != nil {
		t.Fatalf("GetSandboxBySession unexpected error: %v", err)
	}
	if sandbox == nil {
		t.Fatalf("expected non-nil sandbox")
	}
	if sandbox.SessionID != "new-session-123" {
		t.Errorf("expected SessionID new-session-123, got %s", sandbox.SessionID)
	}
	if sandbox.SandboxID != "sandbox-456" {
		t.Errorf("expected SandboxID sandbox-456, got %s", sandbox.SandboxID)
	}
	if sandbox.Name != "sandbox-test" {
		t.Errorf("expected Name sandbox-test, got %s", sandbox.Name)
	}
	if len(sandbox.EntryPoints) != 1 {
		t.Fatalf("expected 1 entry point, got %d", len(sandbox.EntryPoints))
	}
	if sandbox.EntryPoints[0].Endpoint != "10.0.0.1:9000" {
		t.Errorf("expected endpoint 10.0.0.1:9000, got %s", sandbox.EntryPoints[0].Endpoint)
	}
}

func TestGetSandboxBySession_CreateSandbox_SetsAuthHeaderFromFile(t *testing.T) {
	// #nosec G101 -- test token, not a real credential.
	const token = "file-token-456"

	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "token")
	if err := os.WriteFile(tokenPath, []byte(token+"\n"), 0o600); err != nil {
		t.Fatalf("failed to write token file: %v", err)
	}

	origTokenPath := serviceAccountTokenPath
	serviceAccountTokenPath = tokenPath
	defer func() {
		serviceAccountTokenPath = origTokenPath
	}()

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+token {
			t.Fatalf("expected Authorization header %q, got %q", "Bearer "+token, got)
		}
		resp := types.CreateSandboxResponse{
			SessionID:   "new-session-123",
			SandboxID:   "sandbox-456",
			SandboxName: "sandbox-test",
			EntryPoints: []types.SandboxEntryPoint{{Endpoint: "10.0.0.1:9000"}},
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer mockServer.Close()

	m := &manager{
		storeClient:     &fakeStoreClient{},
		workloadMgrAddr: mockServer.URL,
		httpClient:      &http.Client{},
	}

	if _, err := m.GetSandboxBySession(context.Background(), "", "default", "test-runtime", types.AgentRuntimeKind); err != nil {
		t.Fatalf("GetSandboxBySession unexpected error: %v", err)
	}
}

func TestGetSandboxBySession_CreateSandbox_NoAuthHeaderWhenNoToken(t *testing.T) {
	dir := t.TempDir()
	missingPath := filepath.Join(dir, "missing-token")

	origTokenPath := serviceAccountTokenPath
	serviceAccountTokenPath = missingPath
	defer func() {
		serviceAccountTokenPath = origTokenPath
	}()

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("expected no Authorization header, got %q", got)
		}
		resp := types.CreateSandboxResponse{
			SessionID:   "new-session-123",
			SandboxID:   "sandbox-456",
			SandboxName: "sandbox-test",
			EntryPoints: []types.SandboxEntryPoint{{Endpoint: "10.0.0.1:9000"}},
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer mockServer.Close()

	m := &manager{
		storeClient:     &fakeStoreClient{},
		workloadMgrAddr: mockServer.URL,
		httpClient:      &http.Client{},
	}

	if _, err := m.GetSandboxBySession(context.Background(), "", "default", "test-runtime", types.AgentRuntimeKind); err != nil {
		t.Fatalf("GetSandboxBySession unexpected error: %v", err)
	}
}

func TestGetSandboxBySession_CreateSandbox_TokenFileReadError(t *testing.T) {
	dir := t.TempDir()

	origTokenPath := serviceAccountTokenPath
	serviceAccountTokenPath = dir // ReadFile on a directory returns an error (not IsNotExist)
	defer func() {
		serviceAccountTokenPath = origTokenPath
	}()

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("expected no Authorization header, got %q", got)
		}
		resp := types.CreateSandboxResponse{
			SessionID:   "new-session-123",
			SandboxID:   "sandbox-456",
			SandboxName: "sandbox-test",
			EntryPoints: []types.SandboxEntryPoint{{Endpoint: "10.0.0.1:9000"}},
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer mockServer.Close()

	m := &manager{
		storeClient:     &fakeStoreClient{},
		workloadMgrAddr: mockServer.URL,
		httpClient:      &http.Client{},
	}

	if _, err := m.GetSandboxBySession(context.Background(), "", "default", "test-runtime", types.AgentRuntimeKind); err != nil {
		t.Fatalf("GetSandboxBySession unexpected error: %v", err)
	}
}

func TestGetSandboxBySession_CreateSandbox_CodeInterpreter_Success(t *testing.T) {
	// Mock workload manager server
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request method and path
		if r.Method != http.MethodPost {
			t.Errorf("expected POST request, got %s", r.Method)
		}
		if r.URL.Path != "/v1/code-interpreter" {
			t.Errorf("expected path /v1/code-interpreter, got %s", r.URL.Path)
		}

		// Verify request body
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("failed to read request body: %v", err)
		}
		var req types.CreateSandboxRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("failed to unmarshal request: %v", err)
		}
		if req.Kind != types.CodeInterpreterKind {
			t.Errorf("expected kind %s, got %s", types.CodeInterpreterKind, req.Kind)
		}

		// Send successful response
		resp := types.CreateSandboxResponse{
			SessionID:   "ci-session-789",
			SandboxID:   "ci-sandbox-101",
			SandboxName: "ci-sandbox-test",
			EntryPoints: []types.SandboxEntryPoint{
				{Endpoint: "10.0.0.2:8080", Protocol: "http", Path: "/"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer mockServer.Close()

	r := &fakeStoreClient{}
	m := &manager{
		storeClient:     r,
		workloadMgrAddr: mockServer.URL,
		httpClient:      &http.Client{},
	}

	sandbox, err := m.GetSandboxBySession(context.Background(), "", "default", "test-ci", types.CodeInterpreterKind)
	if err != nil {
		t.Fatalf("GetSandboxBySession unexpected error: %v", err)
	}
	if sandbox == nil {
		t.Fatalf("expected non-nil sandbox")
	}
	if sandbox.SessionID != "ci-session-789" {
		t.Errorf("expected SessionID ci-session-789, got %s", sandbox.SessionID)
	}
}

func TestGetSandboxBySession_CreateSandbox_UnsupportedKind(t *testing.T) {
	r := &fakeStoreClient{}
	m := &manager{
		storeClient:     r,
		workloadMgrAddr: "http://localhost:8080",
		httpClient:      &http.Client{},
	}

	_, err := m.GetSandboxBySession(context.Background(), "", "default", "test", "UnsupportedKind")
	if err == nil {
		t.Fatalf("expected error for unsupported kind")
	}
	if err.Error() != "unsupported kind: UnsupportedKind" {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestGetSandboxBySession_CreateSandbox_WorkloadManagerUnavailable(t *testing.T) {
	// Mock workload manager server that closes connection immediately
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Close connection without sending response
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("webserver doesn't support hijacking")
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			t.Fatal(err)
		}
		conn.Close()
	}))
	serverURL := mockServer.URL
	mockServer.Close() // Close the server to make it unavailable

	r := &fakeStoreClient{}
	m := &manager{
		storeClient:     r,
		workloadMgrAddr: serverURL,
		httpClient:      &http.Client{},
	}

	_, err := m.GetSandboxBySession(context.Background(), "", "default", "test", types.AgentRuntimeKind)
	if err == nil {
		t.Fatalf("expected error for unavailable workload manager")
	}
	if !apierrors.IsInternalError(err) {
		t.Errorf("expected internal error, got %v", err)
	}
}

func TestGetSandboxBySession_CreateSandbox_NonOKStatus(t *testing.T) {
	// Mock workload manager server that returns error
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal server error"))
	}))
	defer mockServer.Close()

	r := &fakeStoreClient{}
	m := &manager{
		storeClient:     r,
		workloadMgrAddr: mockServer.URL,
		httpClient:      &http.Client{},
	}

	_, err := m.GetSandboxBySession(context.Background(), "", "default", "test", types.AgentRuntimeKind)
	if err == nil {
		t.Fatalf("expected error for non-OK status")
	}
	if !apierrors.IsInternalError(err) {
		t.Errorf("expected internal error, got %v", err)
	}
}

func TestGetSandboxBySession_CreateSandbox_InvalidJSON(t *testing.T) {
	// Mock workload manager server that returns invalid JSON
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("invalid json"))
	}))
	defer mockServer.Close()

	r := &fakeStoreClient{}
	m := &manager{
		storeClient:     r,
		workloadMgrAddr: mockServer.URL,
		httpClient:      &http.Client{},
	}

	_, err := m.GetSandboxBySession(context.Background(), "", "default", "test", types.AgentRuntimeKind)
	if err == nil {
		t.Fatalf("expected error for invalid JSON")
	}
	if err.Error() == "" {
		t.Errorf("expected error message for invalid JSON")
	}
}

func TestGetSandboxBySession_CreateSandbox_EmptySessionID(t *testing.T) {
	// Mock workload manager server that returns empty sessionID
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := types.CreateSandboxResponse{
			SessionID:   "", // Empty sessionID
			SandboxID:   "sandbox-456",
			SandboxName: "sandbox-test",
			EntryPoints: []types.SandboxEntryPoint{
				{Endpoint: "10.0.0.1:9000"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer mockServer.Close()

	r := &fakeStoreClient{}
	m := &manager{
		storeClient:     r,
		workloadMgrAddr: mockServer.URL,
		httpClient:      &http.Client{},
	}

	_, err := m.GetSandboxBySession(context.Background(), "", "default", "test", types.AgentRuntimeKind)
	if err == nil {
		t.Fatalf("expected error for empty sessionID in response")
	}
	if !apierrors.IsInternalError(err) {
		t.Errorf("expected internal error, got %v", err)
	}
}

// ---- tests: NewSessionManager mTLS wiring ----

func TestNewSessionManager_MTLSEnabled_ValidCerts(t *testing.T) {
	// Generate test certs using a temp self-signed CA
	dir := t.TempDir()
	certFile, keyFile, caFile := generateTestCertsForRouter(t, dir)

	t.Setenv("WORKLOAD_MANAGER_URL", "https://localhost:8080")

	cfg := &mtls.Config{CertFile: certFile, KeyFile: keyFile, CAFile: caFile}
	sm, err := NewSessionManager(&fakeStoreClient{}, cfg)
	if err != nil {
		t.Fatalf("NewSessionManager with mTLS failed: %v", err)
	}
	if sm == nil {
		t.Fatal("expected non-nil SessionManager")
	}

	// Verify the underlying manager has a certWatcher set
	m, ok := sm.(*manager)
	if !ok {
		t.Fatal("expected *manager type")
	}
	if m.certWatcher == nil {
		t.Error("expected certWatcher to be non-nil when mTLS is enabled")
	}
	m.certWatcher.Stop()
}

func TestNewSessionManager_MTLSDisabled(t *testing.T) {
	t.Setenv("WORKLOAD_MANAGER_URL", "http://localhost:8080")

	tests := []struct {
		name string
		cfg  *mtls.Config
	}{
		{name: "nil config", cfg: nil},
		{name: "empty config", cfg: &mtls.Config{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sm, err := NewSessionManager(&fakeStoreClient{}, tt.cfg)
			if err != nil {
				t.Fatalf("NewSessionManager without mTLS failed: %v", err)
			}

			m, ok := sm.(*manager)
			if !ok {
				t.Fatal("expected *manager type")
			}
			if m.certWatcher != nil {
				t.Error("expected certWatcher to be nil when mTLS is disabled")
			}
		})
	}
}

// generateTestCertsForRouter creates a self-signed CA and leaf cert for testing.
func generateTestCertsForRouter(t *testing.T, dir string) (certFile, keyFile, caFile string) {
	t.Helper()

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
	writePEMFile(t, caFile, "CERTIFICATE", caCertDER)

	caCert, _ := x509.ParseCertificate(caCertDER)

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate leaf key: %v", err)
	}
	spiffeURL, _ := url.Parse("spiffe://cluster.local/ns/agentcube-system/sa/agentcube-router")
	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{Organization: []string{"Test Router"}},
		NotBefore:    time.Now().Add(-1 * time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		URIs:         []*url.URL{spiffeURL},
	}
	leafCertDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create leaf cert: %v", err)
	}
	certFile = filepath.Join(dir, "cert.pem")
	writePEMFile(t, certFile, "CERTIFICATE", leafCertDER)

	keyDER, _ := x509.MarshalECPrivateKey(leafKey)
	keyFile = filepath.Join(dir, "key.pem")
	writePEMFile(t, keyFile, "EC PRIVATE KEY", keyDER)

	return certFile, keyFile, caFile
}

func writePEMFile(t *testing.T, path, blockType string, data []byte) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer f.Close()
	if err := pem.Encode(f, &pem.Block{Type: blockType, Bytes: data}); err != nil {
		t.Fatalf("encode PEM %s: %v", path, err)
	}
}
