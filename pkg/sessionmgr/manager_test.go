package sessionmgr

import (
	"context"
	"errors"
	"strings"
	"testing"

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

type fakeSandboxManagerClient struct {
	resp       *types.CreateSandboxResponse
	err        error
	called     bool
	lastReq    *types.CreateSandboxRequest
	lastCtxNil bool
	calls      int
}

func (f *fakeSandboxManagerClient) CreateSandbox(ctx context.Context, req *types.CreateSandboxRequest) (*types.CreateSandboxResponse, error) {
	f.called = true
	f.calls++
	f.lastReq = req
	f.lastCtxNil = ctx == nil
	return f.resp, f.err
}

// ---- tests: GetSandboxBySession ----

func TestGetSandboxBySession_Success(t *testing.T) {
	ctx := context.Background()

	sb := &types.SandboxRedis{
		SandboxID:   "sandbox-1",
		SandboxName: "sandbox-1",
		EntryPoints: []types.SandboxAccess{
			{Endpoint: "10.0.0.1:9000"},
		},
		SessionID: "sess-1",
		Status:    "running",
	}

	r := &fakeRedisClient{
		sandbox: sb,
	}
	m := New(r, &fakeSandboxManagerClient{})

	got, err := m.GetSandboxBySession(ctx, "sess-1")
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

func TestGetSandboxBySession_EmptySessionID(t *testing.T) {
	ctx := context.Background()
	m := New(&fakeRedisClient{}, &fakeSandboxManagerClient{})

	_, err := m.GetSandboxBySession(ctx, "")
	if err == nil {
		t.Fatalf("expected error for empty sessionID")
	}
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument, got %v", err)
	}
}

func TestGetSandboxBySession_NotFound(t *testing.T) {
	ctx := context.Background()
	r := &fakeRedisClient{
		sandbox: nil,
		err:     redis.ErrNotFound,
	}
	m := New(r, &fakeSandboxManagerClient{})

	_, err := m.GetSandboxBySession(ctx, "sess-1")
	if err == nil {
		t.Fatalf("expected error for not found session")
	}
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("expected ErrSessionNotFound, got %v", err)
	}
}

func TestGetSandboxBySession_OtherErrorWrapped(t *testing.T) {
	ctx := context.Background()
	inner := errors.New("redis boom")
	r := &fakeRedisClient{
		sandbox: nil,
		err:     inner,
	}
	m := New(r, &fakeSandboxManagerClient{})

	_, err := m.GetSandboxBySession(ctx, "sess-1")
	if err == nil {
		t.Fatalf("expected error")
	}
	// Should wrap the inner error.
	if !errors.Is(err, inner) {
		t.Fatalf("expected error to wrap inner error, got %v", err)
	}
	if !strings.Contains(err.Error(), "sessionmgr: get sandbox by sessionID") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestGetSandboxBySession_NilSandbox(t *testing.T) {
	ctx := context.Background()
	r := &fakeRedisClient{
		sandbox: nil,
		err:     nil,
	}
	m := New(r, &fakeSandboxManagerClient{})

	_, err := m.GetSandboxBySession(ctx, "sess-1")
	if err == nil {
		t.Fatalf("expected error when redis returns nil sandbox")
	}
	if !strings.Contains(err.Error(), "returned nil sandbox") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

// ---- tests: CreateSandbox ----

func TestCreateSandbox_Success(t *testing.T) {
	ctx := context.Background()

	req := &types.CreateSandboxRequest{
		Kind:      "agent",
		Name:      "sandbox-name",
		Namespace: "default",
	}

	resp := &types.CreateSandboxResponse{
		SessionID:   "sess-1",
		SandboxID:   "sandbox-1",
		SandboxName: "sandbox-name",
		Accesses: []types.SandboxAccess{
			{Endpoint: "10.0.0.1:9000"},
		},
	}

	s := &fakeSandboxManagerClient{
		resp: resp,
	}
	m := New(&fakeRedisClient{}, s)

	got, err := m.CreateSandbox(ctx, req)
	if err != nil {
		t.Fatalf("CreateSandbox unexpected error: %v", err)
	}
	if !s.called {
		t.Fatalf("expected SandboxManagerClient to be called")
	}
	if got == nil {
		t.Fatalf("expected non-nil sandbox")
	}
	if got.SandboxID != "sandbox-1" {
		t.Fatalf("unexpected SandboxID: got %q, want %q", got.SandboxID, "sandbox-1")
	}
	if got.SessionID != "sess-1" {
		t.Fatalf("unexpected SessionID: got %q, want %q", got.SessionID, "sess-1")
	}
	if len(got.EntryPoints) != 1 || got.EntryPoints[0].Endpoint != "10.0.0.1:9000" {
		t.Fatalf("unexpected EntryPoints: %+v", got.EntryPoints)
	}
}

func TestCreateSandbox_NilRequest(t *testing.T) {
	ctx := context.Background()
	s := &fakeSandboxManagerClient{}
	m := New(&fakeRedisClient{}, s)

	_, err := m.CreateSandbox(ctx, nil)
	if err == nil {
		t.Fatalf("expected error for nil request")
	}
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument, got %v", err)
	}
	if s.called {
		t.Fatalf("expected SandboxManagerClient not to be called for nil request")
	}
}

func TestCreateSandbox_InvalidRequest_MissingKindOrNamespace(t *testing.T) {
	ctx := context.Background()
	s := &fakeSandboxManagerClient{}
	m := New(&fakeRedisClient{}, s)

	// Missing Kind.
	_, err := m.CreateSandbox(ctx, &types.CreateSandboxRequest{
		Kind:      "",
		Name:      "name",
		Namespace: "ns",
	})
	if err == nil {
		t.Fatalf("expected error for missing Kind")
	}
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument, got %v", err)
	}
	if s.called {
		t.Fatalf("expected SandboxManagerClient not to be called when Kind is empty")
	}

	// Reset fake.
	s.called = false
	s.calls = 0

	// Missing Namespace.
	_, err = m.CreateSandbox(ctx, &types.CreateSandboxRequest{
		Kind:      "agent",
		Name:      "name",
		Namespace: "",
	})
	if err == nil {
		t.Fatalf("expected error for missing Namespace")
	}
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument, got %v", err)
	}
	if s.called {
		t.Fatalf("expected SandboxManagerClient not to be called when Namespace is empty")
	}
}

func TestCreateSandbox_CreateSandboxFailed(t *testing.T) {
	ctx := context.Background()
	s := &fakeSandboxManagerClient{
		err: ErrCreateSandboxFailed,
	}
	m := New(&fakeRedisClient{}, s)

	req := &types.CreateSandboxRequest{
		Kind:      "agent",
		Name:      "sandbox-name",
		Namespace: "default",
	}

	_, err := m.CreateSandbox(ctx, req)
	if err == nil {
		t.Fatalf("expected error")
	}
	// ErrCreateSandboxFailed should be propagated as is.
	if !errors.Is(err, ErrCreateSandboxFailed) {
		t.Fatalf("expected ErrCreateSandboxFailed, got %v", err)
	}
}

func TestCreateSandbox_UpstreamUnavailableWrapped(t *testing.T) {
	ctx := context.Background()
	inner := errors.New("upstream timeout")
	s := &fakeSandboxManagerClient{
		err: inner,
	}
	m := New(&fakeRedisClient{}, s)

	req := &types.CreateSandboxRequest{
		Kind:      "agent",
		Name:      "sandbox-name",
		Namespace: "default",
	}

	_, err := m.CreateSandbox(ctx, req)
	if err == nil {
		t.Fatalf("expected error")
	}
	// Should be wrapped as ErrUpstreamUnavailable.
	if !errors.Is(err, ErrUpstreamUnavailable) {
		t.Fatalf("expected error to be ErrUpstreamUnavailable, got %v", err)
	}
	// Inner error is included in the message, but not wrapped with %w,
	// so errors.Is on inner should be false.
	if errors.Is(err, inner) {
		t.Fatalf("did not expect error to wrap inner error via errors.Is")
	}
	if !strings.Contains(err.Error(), "upstream timeout") {
		t.Fatalf("expected error message to contain inner error, got %v", err)
	}
}

func TestCreateSandbox_InvalidResponse(t *testing.T) {
	ctx := context.Background()
	// Response missing SessionID and SandboxID.
	s := &fakeSandboxManagerClient{
		resp: &types.CreateSandboxResponse{},
	}
	m := New(&fakeRedisClient{}, s)

	req := &types.CreateSandboxRequest{
		Kind:      "agent",
		Name:      "sandbox-name",
		Namespace: "default",
	}

	_, err := m.CreateSandbox(ctx, req)
	if err == nil {
		t.Fatalf("expected error for invalid CreateSandboxResponse")
	}
	if !errors.Is(err, ErrCreateSandboxFailed) {
		t.Fatalf("expected ErrCreateSandboxFailed, got %v", err)
	}
}
