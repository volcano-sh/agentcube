package redis

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	redisv9 "github.com/redis/go-redis/v9"

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

func newTestSandbox(id string, expiresAt time.Time) *types.SandboxRedis {
	return &types.SandboxRedis{
		SandboxID:   id,
		SandboxName: "test-sandbox-" + id,
		EntryPoints: nil,
		SessionID:   "",
		CreatedAt:   time.Now().UTC(),
		ExpiresAt:   expiresAt,
		Status:      "running",
	}
}

func TestSetSessionLockIfAbsent(t *testing.T) {
	ctx := context.Background()
	c, _ := newTestClient(t)

	ok, err := c.SetSessionLockIfAbsent(ctx, "sess-1", time.Minute)
	if err != nil {
		t.Fatalf("SetSessionLockIfAbsent error: %v", err)
	}
	if !ok {
		t.Fatalf("expected first SetSessionLockIfAbsent to succeed")
	}

	ok, err = c.SetSessionLockIfAbsent(ctx, "sess-1", time.Minute)
	if err != nil {
		t.Fatalf("SetSessionLockIfAbsent error on second call: %v", err)
	}
	if ok {
		t.Fatalf("expected second SetSessionLockIfAbsent to return false")
	}
}

func TestBindAndGetSandboxBySessionID(t *testing.T) {
	ctx := context.Background()
	c, mr := newTestClient(t)

	now := time.Now().UTC().Truncate(time.Second)
	expiresAt := now.Add(10 * time.Minute)

	sb := newTestSandbox("sb-1", expiresAt)

	// Pre-create lock to verify it gets deleted.
	mr.Set(c.lockKey("sess-1"), "1")

	if err := c.BindSessionWithSandbox(ctx, "sess-1", sb, time.Hour); err != nil {
		t.Fatalf("BindSessionWithSandbox error: %v", err)
	}

	// session key should exist with sandbox JSON.
	data, err := mr.Get(c.sessionKey("sess-1"))
	if err != nil {
		t.Fatalf("expected session key to exist: %v", err)
	}
	var got types.SandboxRedis
	if err := json.Unmarshal([]byte(data), &got); err != nil {
		t.Fatalf("unmarshal session value: %v", err)
	}
	if got.SandboxID != "sb-1" {
		t.Fatalf("unexpected sandbox ID: got %q, want %q", got.SandboxID, "sb-1")
	}

	// sandbox key should map back to session ID.
	sessionID, err := mr.Get(c.sandboxKey("sb-1"))
	if err != nil {
		t.Fatalf("expected sandbox key to exist: %v", err)
	}
	if sessionID != "sess-1" {
		t.Fatalf("unexpected sessionID: got %q, want %q", sessionID, "sess-1")
	}

	// lock should be removed.
	if mr.Exists(c.lockKey("sess-1")) {
		t.Fatalf("expected lock key to be deleted")
	}

	// expiry index should be set.
	score, err := mr.ZScore(c.expiryIndexKey, "sb-1")
	if err != nil {
		t.Fatalf("expected expiry index entry: %v", err)
	}
	if int64(score) != expiresAt.Unix() {
		t.Fatalf("unexpected expiry score: got %v, want %v", score, expiresAt.Unix())
	}

	// last-activity index SHOULD NOT be created by BindSessionWithSandbox.
	if mr.Exists(c.lastActivityIndexKey) {
		t.Fatalf("did not expect last_activity index key to exist after bind")
	}

	// GetSandboxBySessionID should return the same sandbox.
	gotPtr, err := c.GetSandboxBySessionID(ctx, "sess-1")
	if err != nil {
		t.Fatalf("GetSandboxBySessionID error: %v", err)
	}
	if gotPtr.SandboxID != "sb-1" {
		t.Fatalf("GetSandboxBySessionID: sandbox ID mismatch: got %q, want %q", gotPtr.SandboxID, "sb-1")
	}
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

func TestDeleteSessionBySandboxIDTx(t *testing.T) {
	ctx := context.Background()
	c, mr := newTestClient(t)

	now := time.Now().UTC().Truncate(time.Second)
	sb := newTestSandbox("sb-1", now.Add(5*time.Minute))
	if err := c.BindSessionWithSandbox(ctx, "sess-1", sb, time.Hour); err != nil {
		t.Fatalf("BindSessionWithSandbox error: %v", err)
	}

	// Simulate one last-activity update so there is an index entry.
	lastActivity := now.Add(-10 * time.Minute)
	if err := c.UpdateSandboxLastActivity(ctx, "sb-1", lastActivity); err != nil {
		t.Fatalf("UpdateSandboxLastActivity before delete error: %v", err)
	}

	if err := c.DeleteSessionBySandboxIDTx(ctx, "sb-1"); err != nil {
		t.Fatalf("DeleteSessionBySandboxIDTx error: %v", err)
	}

	if mr.Exists(c.sessionKey("sess-1")) {
		t.Fatalf("expected session key to be deleted")
	}
	if mr.Exists(c.sandboxKey("sb-1")) {
		t.Fatalf("expected sandbox key to be deleted")
	}
	// When the last member is removed from a sorted set, miniredis deletes the key.
	if mr.Exists(c.expiryIndexKey) {
		t.Fatalf("expected expiry index key to be deleted or empty")
	}
	if mr.Exists(c.lastActivityIndexKey) {
		t.Fatalf("expected last_activity index key to be deleted or empty")
	}

	// Second delete should be treated as success (mapping already gone).
	if err := c.DeleteSessionBySandboxIDTx(ctx, "sb-1"); err != nil {
		t.Fatalf("DeleteSessionBySandboxIDTx second call error: %v", err)
	}
}

func TestListExpiredSandboxes(t *testing.T) {
	ctx := context.Background()
	c, _ := newTestClient(t)

	now := time.Now().UTC().Truncate(time.Second)

	sb1 := newTestSandbox("sb-1", now.Add(-2*time.Hour))
	sb2 := newTestSandbox("sb-2", now.Add(-1*time.Hour))
	sb3 := newTestSandbox("sb-3", now.Add(1*time.Hour))

	if err := c.BindSessionWithSandbox(ctx, "sess-1", sb1, time.Hour); err != nil {
		t.Fatalf("BindSessionWithSandbox sb1 error: %v", err)
	}
	if err := c.BindSessionWithSandbox(ctx, "sess-2", sb2, time.Hour); err != nil {
		t.Fatalf("BindSessionWithSandbox sb2 error: %v", err)
	}
	if err := c.BindSessionWithSandbox(ctx, "sess-3", sb3, time.Hour); err != nil {
		t.Fatalf("BindSessionWithSandbox sb3 error: %v", err)
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

	sb1 := newTestSandbox("sb-1", now.Add(10*time.Minute))
	sb2 := newTestSandbox("sb-2", now.Add(10*time.Minute))
	sb3 := newTestSandbox("sb-3", now.Add(10*time.Minute))

	if err := c.BindSessionWithSandbox(ctx, "sess-1", sb1, time.Hour); err != nil {
		t.Fatalf("BindSessionWithSandbox sb1 error: %v", err)
	}
	if err := c.BindSessionWithSandbox(ctx, "sess-2", sb2, time.Hour); err != nil {
		t.Fatalf("BindSessionWithSandbox sb2 error: %v", err)
	}
	if err := c.BindSessionWithSandbox(ctx, "sess-3", sb3, time.Hour); err != nil {
		t.Fatalf("BindSessionWithSandbox sb3 error: %v", err)
	}

	// Only UpdateSandboxLastActivity writes last-activity index.
	if err := c.UpdateSandboxLastActivity(ctx, "sb-1", now.Add(-3*time.Hour)); err != nil {
		t.Fatalf("UpdateSandboxLastActivity sb-1 error: %v", err)
	}
	if err := c.UpdateSandboxLastActivity(ctx, "sb-2", now.Add(-2*time.Hour)); err != nil {
		t.Fatalf("UpdateSandboxLastActivity sb-2 error: %v", err)
	}
	if err := c.UpdateSandboxLastActivity(ctx, "sb-3", now.Add(1*time.Hour)); err != nil {
		t.Fatalf("UpdateSandboxLastActivity sb-3 error: %v", err)
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

	sb := newTestSandbox("sb-1", now.Add(30*time.Minute))
	ttl := 30 * time.Minute

	if err := c.BindSessionWithSandbox(ctx, "sess-1", sb, ttl); err != nil {
		t.Fatalf("BindSessionWithSandbox error: %v", err)
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

	if err := c.UpdateSandboxLastActivity(ctx, "sb-1", newLastActivity); err != nil {
		t.Fatalf("UpdateSandboxLastActivity error: %v", err)
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
	score, err := mr.ZScore(c.lastActivityIndexKey, "sb-1")
	if err != nil {
		t.Fatalf("expected last_activity index entry after update: %v", err)
	}
	if int64(score) != newLastActivity.Unix() {
		t.Fatalf("unexpected lastActivity score after update: got %v, want %v", score, newLastActivity.Unix())
	}
}
