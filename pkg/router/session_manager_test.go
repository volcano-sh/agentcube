package router

import (
	"context"
	"errors"
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

	got, err := m.GetSandboxBySession("sess-1", "default", "test", "AgentRuntime")
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

	_, err := m.GetSandboxBySession("sess-1", "default", "test", "AgentRuntime")
	if err == nil {
		t.Fatalf("expected error for not found session")
	}
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("expected ErrSessionNotFound, got %v", err)
	}
}
