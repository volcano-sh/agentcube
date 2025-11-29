package sessionmgr

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/volcano-sh/agentcube/pkg/redis"
)

// --------- Fake implementations ---------

type fakeRedisClient struct {
	gotSessionID string
	sandbox      *redis.Sandbox
	err          error
}

func (f *fakeRedisClient) GetSandboxBySessionID(ctx Context, sessionID string) (*redis.Sandbox, error) {
	f.gotSessionID = sessionID
	return f.sandbox, f.err
}

type fakeSandboxMgrClient struct {
	gotReq *CreateSandboxRequest
	resp   *CreateSandboxResponse
	err    error
}

func (f *fakeSandboxMgrClient) CreateSandbox(ctx Context, req *CreateSandboxRequest) (*CreateSandboxResponse, error) {
	// Simulate latency and ensure the Context interface behaves correctly (optional).
	if deadline, ok := ctx.Deadline(); ok && time.Now().After(deadline) {
		return nil, context.DeadlineExceeded
	}
	f.gotReq = req
	return f.resp, f.err
}

// --------- Helper functions ---------

func newTestManager(r RedisClient, s SandboxManagerClient) Manager {
	return New(r, s)
}

func newSandbox(endpoint string) *redis.Sandbox {
	return &redis.Sandbox{
		SandboxID: "sandbox-1",
		Endpoint:  endpoint,
	}
}

// --------- Unit tests ---------

func TestGetSandboxBySession_Existing_Success(t *testing.T) {
	ctx := context.Background()

	fRedis := &fakeRedisClient{
		sandbox: newSandbox("10.0.0.1:9000"),
	}
	fSB := &fakeSandboxMgrClient{}

	mgr := newTestManager(fRedis, fSB)

	req := &GetSandboxBySessionRequest{
		SessionID: "sess-123",
	}

	resp, err := mgr.GetSandboxBySession(ctx, req)
	if err != nil {
		t.Fatalf("GetSandboxBySession returned error: %v", err)
	}

	if fRedis.gotSessionID != "sess-123" {
		t.Fatalf("redis gotSessionID = %q, want %q", fRedis.gotSessionID, "sess-123")
	}

	if resp.SessionID != "sess-123" {
		t.Errorf("resp.SessionID = %q, want %q", resp.SessionID, "sess-123")
	}
	if resp.Endpoint != "10.0.0.1:9000" {
		t.Errorf("resp.Endpoint = %q, want %q", resp.Endpoint, "10.0.0.1:9000")
	}
	if resp.Sandbox == nil || resp.Sandbox.Endpoint != "10.0.0.1:9000" {
		t.Errorf("resp.Sandbox invalid: %+v", resp.Sandbox)
	}
}

func TestGetSandboxBySession_Existing_NotFound(t *testing.T) {
	ctx := context.Background()

	fRedis := &fakeRedisClient{
		err: redis.ErrNotFound,
	}
	mgr := newTestManager(fRedis, &fakeSandboxMgrClient{})

	req := &GetSandboxBySessionRequest{
		SessionID: "sess-404",
	}

	_, err := mgr.GetSandboxBySession(ctx, req)
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("expected ErrSessionNotFound, got %v", err)
	}
}

func TestGetSandboxBySession_Existing_RedisError(t *testing.T) {
	ctx := context.Background()

	redisErr := errors.New("boom")
	fRedis := &fakeRedisClient{
		err: redisErr,
	}
	mgr := newTestManager(fRedis, &fakeSandboxMgrClient{})

	req := &GetSandboxBySessionRequest{
		SessionID: "sess-err",
	}

	_, err := mgr.GetSandboxBySession(ctx, req)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("expected generic redis error, got ErrSessionNotFound: %v", err)
	}
}

func TestGetSandboxBySession_New_InvalidArgs(t *testing.T) {
	ctx := context.Background()

	mgr := newTestManager(&fakeRedisClient{}, &fakeSandboxMgrClient{})

	// Kind is empty.
	req := &GetSandboxBySessionRequest{
		SessionID: "",
		Kind:      "",
		Namespace: "ns",
	}
	_, err := mgr.GetSandboxBySession(ctx, req)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument when Kind empty, got %v", err)
	}

	// Namespace is empty.
	req = &GetSandboxBySessionRequest{
		SessionID: "",
		Kind:      redis.CodeInterpreter,
		Namespace: "",
	}
	_, err = mgr.GetSandboxBySession(ctx, req)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument when Namespace empty, got %v", err)
	}
}

func TestGetSandboxBySession_New_CreateSuccess(t *testing.T) {
	ctx := context.Background()

	fRedis := &fakeRedisClient{}
	fSB := &fakeSandboxMgrClient{
		resp: &CreateSandboxResponse{
			SessionID: "sess-new",
			Sandbox:   newSandbox("10.0.0.2:9000"),
		},
	}
	mgr := newTestManager(fRedis, fSB)

	req := &GetSandboxBySessionRequest{
		SessionID: "",
		Kind:      redis.CodeInterpreter,
		Name:      "pcap-analyzer",
		Namespace: "agent-runtime",
	}

	resp, err := mgr.GetSandboxBySession(ctx, req)
	if err != nil {
		t.Fatalf("GetSandboxBySession returned error: %v", err)
	}

	if fSB.gotReq == nil {
		t.Fatal("sandbox manager did not receive request")
	}
	if fSB.gotReq.Kind != redis.CodeInterpreter {
		t.Errorf("gotReq.Kind = %q, want %q", fSB.gotReq.Kind, redis.CodeInterpreter)
	}
	if fSB.gotReq.Name != "pcap-analyzer" {
		t.Errorf("gotReq.Name = %q, want %q", fSB.gotReq.Name, "pcap-analyzer")
	}
	if fSB.gotReq.Namespace != "agent-runtime" {
		t.Errorf("gotReq.Namespace = %q, want %q", fSB.gotReq.Namespace, "agent-runtime")
	}

	if resp.SessionID != "sess-new" {
		t.Errorf("resp.SessionID = %q, want %q", resp.SessionID, "sess-new")
	}
	if resp.Endpoint != "10.0.0.2:9000" {
		t.Errorf("resp.Endpoint = %q, want %q", resp.Endpoint, "10.0.0.2:9000")
	}
}

func TestGetSandboxBySession_New_CreateFailed_UpstreamUnavailable(t *testing.T) {
	ctx := context.Background()

	fSB := &fakeSandboxMgrClient{
		err: errors.New("network error"),
	}
	mgr := newTestManager(&fakeRedisClient{}, fSB)

	req := &GetSandboxBySessionRequest{
		SessionID: "",
		Kind:      redis.Agent,
		Namespace: "agent-runtime",
	}

	_, err := mgr.GetSandboxBySession(ctx, req)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrUpstreamUnavailable) {
		t.Fatalf("expected ErrUpstreamUnavailable, got %v", err)
	}
}

func TestGetSandboxBySession_New_CreateFailed_BadResponse(t *testing.T) {
	ctx := context.Background()

	// Simulate sandbox manager returning an invalid response:
	// empty SessionID and nil Sandbox.
	fSB := &fakeSandboxMgrClient{
		resp: &CreateSandboxResponse{
			SessionID: "",
			Sandbox:   nil,
		},
	}
	mgr := newTestManager(&fakeRedisClient{}, fSB)

	req := &GetSandboxBySessionRequest{
		SessionID: "",
		Kind:      redis.Agent,
		Namespace: "agent-runtime",
	}

	_, err := mgr.GetSandboxBySession(ctx, req)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrCreateSandboxFailed) {
		t.Fatalf("expected ErrCreateSandboxFailed, got %v", err)
	}
}

func TestGetSandboxBySession_RequestNil(t *testing.T) {
	ctx := context.Background()
	mgr := newTestManager(&fakeRedisClient{}, &fakeSandboxMgrClient{})

	_, err := mgr.GetSandboxBySession(ctx, nil)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument, got %v", err)
	}
}
