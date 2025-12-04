package redis

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

func newTestClient(t *testing.T) (*client, *miniredis.Miniredis) {
	t.Helper()

	mr := miniredis.RunT(t)

	cli := NewClient(&redisv9.Options{
		Addr: mr.Addr(),
	})
	c, ok := cli.(*client)
	if !ok {
		t.Fatalf("NewClient did not return *client")
	}
	return c, mr
}

func newTestSandbox(id string, sessionID string, expiresAt time.Time) *types.SandboxRedis {
	return &types.SandboxRedis{
		SandboxID:   id,
		SandboxName: "test-sandbox-" + id,
		EntryPoints: nil,
		SessionID:   sessionID,
		CreatedAt:   time.Now().UTC(),
		ExpiresAt:   expiresAt,
		Status:      "running",
	}
}

func TestClient_Ping(t *testing.T) {
	ctx := context.Background()
	c, _ := newTestClient(t)

	err := c.Ping(ctx)
	assert.Nil(t, err)
}

func TestClient_StoreSandbox(t *testing.T) {
	ctx := context.Background()
	c, _ := newTestClient(t)

	sandboxRedis := &types.SandboxRedis{
		SessionID:        "session-id-TestClient_StoreSandbox-01",
		SandboxNamespace: "agent-cube",
		SandboxName:      "fake-sandbox-01",
		ExpiresAt:        time.Now(),
	}
	err := c.StoreSandbox(ctx, sandboxRedis, time.Hour)
	assert.Nil(t, err)

	err = c.UpdateSandbox(ctx, sandboxRedis, time.Hour)
	assert.Nil(t, err)
}

func TestClient_UpdateSandbox(t *testing.T) {
	ctx := context.Background()
	c, _ := newTestClient(t)

	sandboxRedis := &types.SandboxRedis{
		SessionID:        "session-id-TestClient_StoreSandbox-02",
		SandboxNamespace: "agent-cube",
		SandboxName:      "fake-sandbox-01",
		ExpiresAt:        time.Now(),
	}
	err := c.UpdateSandbox(ctx, sandboxRedis, time.Hour)
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "key not exists")
}

func TestGetSandboxBySessionIDNotFound(t *testing.T) {
	ctx := context.Background()
	c, _ := newTestClient(t)

	_, err := c.GetSandboxBySessionID(ctx, "non-existent")
	if err == nil {
		t.Fatalf("expected error for non-existent session")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestListExpiredSandboxes(t *testing.T) {
	ctx := context.Background()
	c, _ := newTestClient(t)

	now := time.Now().UTC().Truncate(time.Second)

	sb1 := newTestSandbox("sb-1", "sess-1", now.Add(-2*time.Hour))
	sb2 := newTestSandbox("sb-2", "sess-2", now.Add(-1*time.Hour))
	sb3 := newTestSandbox("sb-3", "sess-3", now.Add(1*time.Hour))

	if err := c.StoreSandbox(ctx, sb1, time.Hour); err != nil {
		t.Fatalf("StoreSandbox sb1 error: %v", err)
	}
	if err := c.StoreSandbox(ctx, sb2, time.Hour); err != nil {
		t.Fatalf("StoreSandbox sb2 error: %v", err)
	}
	if err := c.StoreSandbox(ctx, sb3, time.Hour); err != nil {
		t.Fatalf("StoreSandbox sb3 error: %v", err)
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
	c, _ := newTestClient(t)

	now := time.Now().UTC().Truncate(time.Second)

	sb1 := newTestSandbox("sb-1", "sess-1", now.Add(10*time.Minute))
	sb2 := newTestSandbox("sb-2", "sess-2", now.Add(10*time.Minute))
	sb3 := newTestSandbox("sb-3", "sess-3", now.Add(10*time.Minute))

	if err := c.StoreSandbox(ctx, sb1, time.Hour); err != nil {
		t.Fatalf("StoreSandbox sb1 error: %v", err)
	}
	if err := c.StoreSandbox(ctx, sb2, time.Hour); err != nil {
		t.Fatalf("StoreSandbox sb2 error: %v", err)
	}
	if err := c.StoreSandbox(ctx, sb3, time.Hour); err != nil {
		t.Fatalf("StoreSandbox sb3 error: %v", err)
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
	c, mr := newTestClient(t)

	now := time.Now().UTC().Truncate(time.Second)
	newLastActivity := now.Add(-5 * time.Minute)

	sb := newTestSandbox("sb-1", "sess-1", now.Add(30*time.Minute))
	ttl := 30 * time.Minute

	if err := c.StoreSandbox(ctx, sb, ttl); err != nil {
		t.Fatalf("StoreSandbox error: %v", err)
	}

	// Check initial TTL and value using the underlying redis client / miniredis.
	key := c.sessionKey("sess-1")
	ttlBefore, err := c.rdb.TTL(ctx, key).Result()
	if err != nil {
		t.Fatalf("TTL before update error: %v", err)
	}
	dataBefore, err := mr.Get(key)
	if err != nil {
		t.Fatalf("get session key before update error: %v", err)
	}

	if err := c.UpdateSessionLastActivity(ctx, "sess-1", newLastActivity); err != nil {
		t.Fatalf("UpdateSessionLastActivity sess-1 error: %v", err)
	}

	// TTL should still be positive (we never touch it in UpdateSandboxLastActivity).
	ttlAfter, err := c.rdb.TTL(ctx, key).Result()
	if err != nil {
		t.Fatalf("TTL after update error: %v", err)
	}
	if ttlBefore <= 0 || ttlAfter <= 0 {
		t.Fatalf("expected positive TTLs, got before=%v, after=%v", ttlBefore, ttlAfter)
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
