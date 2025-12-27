package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	redisv9 "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"

	"github.com/volcano-sh/agentcube/pkg/common/types"
)

func TestMakeRedisOptions(t *testing.T) {
	t.Run("missing REDIS_ADDR", func(t *testing.T) {
		t.Setenv("REDIS_PASSWORD", "test_pwd")
		opts, err := makeRedisOptions()
		assert.Nil(t, opts)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "missing env var REDIS_ADDR")
	})

	t.Run("missing REDIS_PASSWORD", func(t *testing.T) {
		t.Setenv("REDIS_ADDR", "127.0.0.1:6379")
		opts, err := makeRedisOptions()
		assert.Nil(t, opts)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "missing env var REDIS_PASSWORD")
	})

	t.Run("all env vars exist", func(t *testing.T) {
		expectedAddr := "127.0.0.1:6379"
		// nolint:gosec
		expectedPwd := "test_redis_pwd"
		t.Setenv("REDIS_ADDR", expectedAddr)
		t.Setenv("REDIS_PASSWORD", expectedPwd)
		opts, err := makeRedisOptions()
		assert.NoError(t, err)
		assert.NotNil(t, opts)
		assert.Equal(t, expectedAddr, opts.Addr)
		assert.Equal(t, expectedPwd, opts.Password)
	})
}

func newTestRedisClient(t *testing.T) (*redisStore, *miniredis.Miniredis) {
	t.Helper()

	mr := miniredis.RunT(t)
	rs := &redisStore{
		cli:                  redisv9.NewClient(&redisv9.Options{Addr: mr.Addr()}),
		sessionPrefix:        "session:",
		expiryIndexKey:       "sandbox:expiry",
		lastActivityIndexKey: "sandbox:last_activity",
	}
	return rs, mr
}

func newTestSandbox(id string, sessionID string, expiresAt time.Time) *types.SandboxInfo {
	return &types.SandboxInfo{
		SandboxID:   id,
		Name:        "test-sandbox-" + id,
		EntryPoints: nil,
		SessionID:   sessionID,
		CreatedAt:   time.Now().UTC(),
		ExpiresAt:   expiresAt,
		Status:      "running",
	}
}

func TestRedisStore_Ping(t *testing.T) {
	ctx := context.Background()
	c, _ := newTestRedisClient(t)

	err := c.Ping(ctx)
	assert.Nil(t, err)
}

func TestRedisStore_StoreSandbox(t *testing.T) {
	ctx := context.Background()
	c, _ := newTestRedisClient(t)

	sandboxStoreStruct := &types.SandboxInfo{
		SessionID:        "session-id-TestClient_StoreSandbox-01",
		SandboxNamespace: "agent-cube",
		Name:             "fake-sandbox-01",
		ExpiresAt:        time.Now(),
	}
	err := c.StoreSandbox(ctx, sandboxStoreStruct)
	assert.Nil(t, err)

	err = c.UpdateSandbox(ctx, sandboxStoreStruct)
	assert.Nil(t, err)
}

func TestRedisStore_UpdateSandbox(t *testing.T) {
	ctx := context.Background()
	c, _ := newTestRedisClient(t)

	sandboxStoreStruct := &types.SandboxInfo{
		SessionID:        "session-id-TestClient_StoreSandbox-02",
		SandboxNamespace: "agent-cube",
		Name:             "fake-sandbox-01",
		ExpiresAt:        time.Now(),
	}
	err := c.UpdateSandbox(ctx, sandboxStoreStruct)
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "key not exists")
}

func TestGetSandboxBySessionIDNotFound(t *testing.T) {
	ctx := context.Background()
	c, _ := newTestRedisClient(t)

	_, err := c.GetSandboxBySessionID(ctx, "non-existent")
	if err == nil {
		t.Fatalf("expected error for non-existent session")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestListExpiredSandboxes(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	ctx := context.Background()
	c, _ := newTestRedisClient(t)

	sb1 := newTestSandbox("sb-1", "sess-1", now.Add(-2*time.Hour))
	sb2 := newTestSandbox("sb-2", "sess-2", now.Add(-1*time.Hour))
	sb3 := newTestSandbox("sb-3", "sess-3", now.Add(1*time.Hour))

	if err := c.StoreSandbox(ctx, sb1); err != nil {
		t.Fatalf("TestListExpiredSandboxes StoreSandbox sb1 error: %v", err)
	}
	if err := c.StoreSandbox(ctx, sb2); err != nil {
		t.Fatalf("TestListExpiredSandboxes StoreSandbox sb2 error: %v", err)
	}
	if err := c.StoreSandbox(ctx, sb3); err != nil {
		t.Fatalf("TestListExpiredSandboxes StoreSandbox sb3 error: %v", err)
	}

	// All expired before "now" should be sb-1 and sb-2.
	list, err := c.ListExpiredSandboxes(ctx, now, 10)
	if err != nil {
		t.Fatalf("ListExpiredSandboxes error: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 expired sandboxes, got %d", len(list))
	}
	ids := map[string]bool{}
	for _, sb := range list {
		ids[sb.SandboxID] = true
	}
	if !ids["sb-1"] || !ids["sb-2"] {
		t.Fatalf("unexpected sandbox IDs in result: %+v", ids)
	}

	// Limit should be respected.
	list, err = c.ListExpiredSandboxes(ctx, now, 1)
	if err != nil {
		t.Fatalf("ListExpiredSandboxes with limit error: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 expired sandbox with limit=1, got %d", len(list))
	}
}

func TestListInactiveSandboxes(t *testing.T) {
	ctx := context.Background()
	c, _ := newTestRedisClient(t)

	now := time.Now().UTC().Truncate(time.Second)

	sb1 := newTestSandbox("sb-1", "sess-1", now.Add(10*time.Minute))
	sb2 := newTestSandbox("sb-2", "sess-2", now.Add(10*time.Minute))
	sb3 := newTestSandbox("sb-3", "sess-3", now.Add(10*time.Minute))

	if err := c.StoreSandbox(ctx, sb1); err != nil {
		t.Fatalf("TestListInactiveSandboxes StoreSandbox sb1 error: %v", err)
	}
	if err := c.StoreSandbox(ctx, sb2); err != nil {
		t.Fatalf("TestListInactiveSandboxes StoreSandbox sb2 error: %v", err)
	}
	if err := c.StoreSandbox(ctx, sb3); err != nil {
		t.Fatalf("TestListInactiveSandboxes StoreSandbox sb3 error: %v", err)
	}

	// Only UpdateSandboxLastActivity writes last-activity index.
	if err := c.UpdateSessionLastActivity(ctx, "sess-1", now.Add(-3*time.Hour)); err != nil {
		t.Fatalf("UpdateSessionLastActivity sess-1 error: %v", err)
	}
	if err := c.UpdateSessionLastActivity(ctx, "sess-2", now.Add(-2*time.Hour)); err != nil {
		t.Fatalf("UpdateSessionLastActivity sess-2 error: %v", err)
	}
	if err := c.UpdateSessionLastActivity(ctx, "sess-3", now.Add(1*time.Hour)); err != nil {
		t.Fatalf("UpdateSessionLastActivity sess-3 error: %v", err)
	}

	// Inactive before "now" should be sb-1 and sb-2.
	list, err := c.ListInactiveSandboxes(ctx, now, 10)
	if err != nil {
		t.Fatalf("ListInactiveSandboxes error: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 inactive sandboxes, got %d", len(list))
	}
	ids := map[string]bool{}
	for _, sb := range list {
		ids[sb.SandboxID] = true
	}
	if !ids["sb-1"] || !ids["sb-2"] {
		t.Fatalf("unexpected sandbox IDs in result: %+v", ids)
	}

	// Limit should be respected.
	list, err = c.ListInactiveSandboxes(ctx, now, 1)
	if err != nil {
		t.Fatalf("ListInactiveSandboxes with limit error: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 inactive sandbox with limit=1, got %d", len(list))
	}
}

func TestUpdateSandboxLastActivity(t *testing.T) {
	ctx := context.Background()
	c, mr := newTestRedisClient(t)

	now := time.Now().UTC().Truncate(time.Second)
	newLastActivity := now.Add(-5 * time.Minute)

	sb := newTestSandbox("sb-1", "sess-1", now.Add(30*time.Minute))

	if err := c.StoreSandbox(ctx, sb); err != nil {
		t.Fatalf("StoreSandbox error: %v", err)
	}

	// Check initial TTL and value using the underlying redis client / miniredis.
	key := c.sessionKey("sess-1")
	dataBefore, err := mr.Get(key)
	if err != nil {
		t.Fatalf("get session key before update error: %v", err)
	}

	if err := c.UpdateSessionLastActivity(ctx, "sess-1", newLastActivity); err != nil {
		t.Fatalf("UpdateSessionLastActivity sess-1 error: %v", err)
	}

	// Session value should remain unchanged (UpdateSandboxLastActivity only updates the index).
	dataAfter, err := mr.Get(key)
	if err != nil {
		t.Fatalf("get session key after update error: %v", err)
	}
	if dataBefore != dataAfter {
		t.Fatalf("expected session value to remain unchanged after UpdateSandboxLastActivity")
	}

	// last_activity index should be updated.
	score, err := mr.ZScore(c.lastActivityIndexKey, "sess-1")
	if err != nil {
		t.Fatalf("expected last_activity index entry after update: %v", err)
	}
	if int64(score) != newLastActivity.Unix() {
		t.Fatalf("unexpected lastActivity score after update: got %v, want %v", score, newLastActivity.Unix())
	}
}

func TestRedisStoreContract(t *testing.T) {
	runContractTests(t, func(t *testing.T) Store {
		mr := miniredis.RunT(t)
		return &redisStore{
			cli:                  redisv9.NewClient(&redisv9.Options{Addr: mr.Addr()}), // fix 1
			sessionPrefix:        "session:",
			expiryIndexKey:       "sandbox:expiry",
			lastActivityIndexKey: "sandbox:last_activity",
		}
	})
}

func runContractTests(t *testing.T, newStore func(*testing.T) Store) {
	ctx := context.Background()
	tests := []struct {
		name string
		fn   func(*testing.T, Store, context.Context)
	}{
		{"Ping", func(t *testing.T, s Store, ctx context.Context) { assert.NoError(t, s.Ping(ctx)) }},
		{"GetNotFound", func(t *testing.T, s Store, ctx context.Context) {
			_, err := s.GetSandboxBySessionID(ctx, "no-such-session")
			assert.ErrorIs(t, err, ErrNotFound)
		}},
		{"StoreGetRoundTrip", func(t *testing.T, s Store, ctx context.Context) {
			in := &types.SandboxInfo{
				SessionID: "contract-round", SandboxID: "sb-round",
				Name: "round-trip", ExpiresAt: time.Now().Add(time.Hour).UTC(),
				CreatedAt: time.Now().UTC(), Status: "running",
			}
			assert.NoError(t, s.StoreSandbox(ctx, in))
			out, err := s.GetSandboxBySessionID(ctx, in.SessionID)
			assert.NoError(t, err)
			assert.Equal(t, in.SessionID, out.SessionID)
			assert.Equal(t, in.Name, out.Name)
		}},
		{"UpdateNonExistentFails", func(t *testing.T, s Store, ctx context.Context) {
			err := s.UpdateSandbox(ctx, &types.SandboxInfo{SessionID: "does-not-exist"})
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "not exists")
		}},
		{"DeleteIdempotent", func(t *testing.T, s Store, ctx context.Context) {
			in := &types.SandboxInfo{SessionID: "del-idem", ExpiresAt: time.Now().Add(time.Hour)}
			assert.NoError(t, s.StoreSandbox(ctx, in))
			assert.NoError(t, s.DeleteSandboxBySessionID(ctx, in.SessionID))
			_, err := s.GetSandboxBySessionID(ctx, in.SessionID)
			assert.ErrorIs(t, err, ErrNotFound)
			assert.NoError(t, s.DeleteSandboxBySessionID(ctx, in.SessionID)) // idempotent
		}},
		{"ListExpired", func(t *testing.T, s Store, ctx context.Context) {
			now := time.Now().UTC().Truncate(time.Second)
			sb1 := &types.SandboxInfo{SessionID: "exp-1", ExpiresAt: now.Add(-2 * time.Hour)}
			sb2 := &types.SandboxInfo{SessionID: "exp-2", ExpiresAt: now.Add(-1 * time.Hour)}
			sb3 := &types.SandboxInfo{SessionID: "exp-3", ExpiresAt: now.Add(time.Hour)}
			assert.NoError(t, s.StoreSandbox(ctx, sb1))
			assert.NoError(t, s.StoreSandbox(ctx, sb2))
			assert.NoError(t, s.StoreSandbox(ctx, sb3))

			list, err := s.ListExpiredSandboxes(ctx, now, 10)
			assert.NoError(t, err)
			assert.Len(t, list, 2)
			assert.Equal(t, "exp-1", list[0].SessionID)
			assert.Equal(t, "exp-2", list[1].SessionID)

			list, err = s.ListExpiredSandboxes(ctx, now, 1)
			assert.NoError(t, err)
			assert.Len(t, list, 1)
		}},
		{"ListInactive", func(t *testing.T, s Store, ctx context.Context) {
			now := time.Now().UTC().Truncate(time.Second)
			sb1 := &types.SandboxInfo{SessionID: "in-1", ExpiresAt: now.Add(time.Hour)}
			sb2 := &types.SandboxInfo{SessionID: "in-2", ExpiresAt: now.Add(time.Hour)}
			assert.NoError(t, s.StoreSandbox(ctx, sb1))
			assert.NoError(t, s.StoreSandbox(ctx, sb2))
			assert.NoError(t, s.UpdateSessionLastActivity(ctx, "in-1", now.Add(-2*time.Hour)))
			assert.NoError(t, s.UpdateSessionLastActivity(ctx, "in-2", now.Add(-1*time.Hour)))

			list, err := s.ListInactiveSandboxes(ctx, now.Add(-30*time.Minute), 10)
			assert.NoError(t, err)
			assert.Len(t, list, 2)
		}},
		{"UpdateLastActivity", func(t *testing.T, s Store, ctx context.Context) {
			sb := &types.SandboxInfo{SessionID: "last-act", ExpiresAt: time.Now().Add(time.Hour)}
			assert.NoError(t, s.StoreSandbox(ctx, sb))
			newTime := time.Now().Add(-time.Hour).UTC()
			assert.NoError(t, s.UpdateSessionLastActivity(ctx, sb.SessionID, newTime))
			list, err := s.ListInactiveSandboxes(ctx, newTime.Add(time.Minute), 10)
			assert.NoError(t, err)
			assert.Len(t, list, 1)
			assert.Equal(t, sb.SessionID, list[0].SessionID)
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newStore(t)
			tc.fn(t, s, ctx)
		})
	}
}
