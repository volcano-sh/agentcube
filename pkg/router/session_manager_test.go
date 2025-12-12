package router

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/volcano-sh/agentcube/pkg/common/types"
	"github.com/volcano-sh/agentcube/pkg/redis"
)

// ---- fakes ----

type fakeRedisClient struct {
	sandbox        *types.SandboxRedis
	err            error
	called         bool
	lastSessionID  string
	lastContextNil bool
}

func (f *fakeRedisClient) GetSandboxBySessionID(ctx context.Context, sessionID string) (*types.SandboxRedis, error) {
	f.called = true
	f.lastSessionID = sessionID
	f.lastContextNil = ctx == nil
	return f.sandbox, f.err
}

func (f *fakeRedisClient) SetSessionLockIfAbsent(ctx context.Context, sessionID string, ttl time.Duration) (bool, error) {
	return false, nil
}

func (f *fakeRedisClient) BindSessionWithSandbox(ctx context.Context, sessionID string, sandboxRedis *types.SandboxRedis, ttl time.Duration) error {
	return nil
}

func (f *fakeRedisClient) DeleteSessionBySandboxIDTx(ctx context.Context, sandboxID string) error {
	return nil
}

func (f *fakeRedisClient) DeleteSandboxBySessionIDTx(ctx context.Context, sessionID string) error {
	return nil
}

func (f *fakeRedisClient) UpdateSandbox(ctx context.Context, sandboxRedis *types.SandboxRedis, ttl time.Duration) error {
	return nil
}

func (f *fakeRedisClient) UpdateSessionLastActivity(ctx context.Context, sessionID string, at time.Time) error {
	return nil
}

func (f *fakeRedisClient) StoreSandbox(ctx context.Context, sandboxRedis *types.SandboxRedis, ttl time.Duration) error {
	return nil
}

func (f *fakeRedisClient) Ping(ctx context.Context) error {
	return nil
}

func (f *fakeRedisClient) ListExpiredSandboxes(ctx context.Context, before time.Time, limit int64) ([]*types.SandboxRedis, error) {
	return nil, nil
}

func (f *fakeRedisClient) ListInactiveSandboxes(ctx context.Context, before time.Time, limit int64) ([]*types.SandboxRedis, error) {
	return nil, nil
}

func (f *fakeRedisClient) UpdateSandboxLastActivity(ctx context.Context, sandboxID string, at time.Time) error {
	return nil
}

// ---- tests: GetSandboxBySession ----

func TestGetSandboxBySession_Success(t *testing.T) {
	sb := &types.SandboxRedis{
		SandboxID:   "sandbox-1",
		SandboxName: "sandbox-1",
		EntryPoints: []types.SandboxEntryPoints{
			{Endpoint: "10.0.0.1:9000"},
		},
		SessionID: "sess-1",
		Status:    "running",
	}

	r := &fakeRedisClient{
		sandbox: sb,
	}
	m := &manager{
		redisClient: r,
	}

	got, err := m.GetSandboxBySession(context.Background(), "sess-1", "default", "test", "AgentRuntime")
	if err != nil {
		t.Fatalf("GetSandboxBySession unexpected error: %v", err)
	}
	if !r.called {
		t.Fatalf("expected RedisClient to be called")
	}
	if r.lastSessionID != "sess-1" {
		t.Fatalf("expected RedisClient to be called with sessionID 'sess-1', got %q", r.lastSessionID)
	}
	if got == nil {
		t.Fatalf("expected non-nil sandbox")
	}
	if got.SandboxID != "sandbox-1" {
		t.Fatalf("unexpected SandboxID: got %q, want %q", got.SandboxID, "sandbox-1")
	}
}

func TestGetSandboxBySession_NotFound(t *testing.T) {
	r := &fakeRedisClient{
		sandbox: nil,
		err:     redis.ErrNotFound,
	}
	m := &manager{
		redisClient: r,
	}

	_, err := m.GetSandboxBySession(context.Background(), "sess-1", "default", "test", "AgentRuntime")
	if err == nil {
		t.Fatalf("expected error for not found session")
	}
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("expected ErrSessionNotFound, got %v", err)
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
		json.NewEncoder(w).Encode(resp)
	}))
	defer mockServer.Close()

	r := &fakeRedisClient{}
	m := &manager{
		redisClient:    r,
		workloadMgrURL: mockServer.URL,
		httpClient:     &http.Client{},
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
	if sandbox.SandboxName != "sandbox-test" {
		t.Errorf("expected SandboxName sandbox-test, got %s", sandbox.SandboxName)
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
		json.NewEncoder(w).Encode(resp)
	}))
	defer mockServer.Close()

	r := &fakeRedisClient{}
	m := &manager{
		redisClient:    r,
		workloadMgrURL: mockServer.URL,
		httpClient:     &http.Client{},
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
	r := &fakeRedisClient{}
	m := &manager{
		redisClient:    r,
		workloadMgrURL: "http://localhost:8080",
		httpClient:     &http.Client{},
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
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

	r := &fakeRedisClient{}
	m := &manager{
		redisClient:    r,
		workloadMgrURL: serverURL,
		httpClient:     &http.Client{},
	}

	_, err := m.GetSandboxBySession(context.Background(), "", "default", "test", types.AgentRuntimeKind)
	if err == nil {
		t.Fatalf("expected error for unavailable workload manager")
	}
	if !errors.Is(err, ErrUpstreamUnavailable) {
		t.Errorf("expected ErrUpstreamUnavailable, got %v", err)
	}
}

func TestGetSandboxBySession_CreateSandbox_NonOKStatus(t *testing.T) {
	// Mock workload manager server that returns error
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal server error"))
	}))
	defer mockServer.Close()

	r := &fakeRedisClient{}
	m := &manager{
		redisClient:    r,
		workloadMgrURL: mockServer.URL,
		httpClient:     &http.Client{},
	}

	_, err := m.GetSandboxBySession(context.Background(), "", "default", "test", types.AgentRuntimeKind)
	if err == nil {
		t.Fatalf("expected error for non-OK status")
	}
	if !errors.Is(err, ErrCreateSandboxFailed) {
		t.Errorf("expected ErrCreateSandboxFailed, got %v", err)
	}
}

func TestGetSandboxBySession_CreateSandbox_InvalidJSON(t *testing.T) {
	// Mock workload manager server that returns invalid JSON
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("invalid json"))
	}))
	defer mockServer.Close()

	r := &fakeRedisClient{}
	m := &manager{
		redisClient:    r,
		workloadMgrURL: mockServer.URL,
		httpClient:     &http.Client{},
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
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		json.NewEncoder(w).Encode(resp)
	}))
	defer mockServer.Close()

	r := &fakeRedisClient{}
	m := &manager{
		redisClient:    r,
		workloadMgrURL: mockServer.URL,
		httpClient:     &http.Client{},
	}

	_, err := m.GetSandboxBySession(context.Background(), "", "default", "test", types.AgentRuntimeKind)
	if err == nil {
		t.Fatalf("expected error for empty sessionID in response")
	}
	if !errors.Is(err, ErrCreateSandboxFailed) {
		t.Errorf("expected ErrCreateSandboxFailed, got %v", err)
	}
}
