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
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/volcano-sh/agentcube/pkg/common/types"
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

// ---- tests: GetSandboxBySession ----

func TestGetSandboxBySession_Success(t *testing.T) {
	sb := &types.SandboxInfo{
		SandboxID: "sandbox-1",
		Name:      "sandbox-1",
		EntryPoints: []types.SandboxEntryPoints{
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
			EntryPoints: []types.SandboxEntryPoints{
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
			EntryPoints: []types.SandboxEntryPoints{
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
			EntryPoints: []types.SandboxEntryPoints{
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
