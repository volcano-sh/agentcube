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
		assert.Contains(t, err.Error(), "REDIS_PASSWORD is required but not set")
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

func newTestSandboxWithE2B(id string, sessionID string, expiresAt time.Time, e2bID string, apiKeyHash string, templateID string) *types.SandboxInfo {
	return &types.SandboxInfo{
		SandboxID:    id,
		Name:         "test-sandbox-" + id,
		EntryPoints:  nil,
		SessionID:    sessionID,
		CreatedAt:    time.Now().UTC(),
		ExpiresAt:    expiresAt,
		Status:       "running",
		E2BSandboxID: e2bID,
		APIKeyHash:   apiKeyHash,
		TemplateID:   templateID,
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

// TestLoadSandboxesBySessionIDs_OrphanedZSetEntry verifies that
// loadSandboxesBySessionIDs skips session IDs whose hash key has been evicted
// from Redis (orphaned sorted-set entry) instead of aborting the entire batch.
//
// This scenario occurs in production when Redis evicts hash keys under memory
// pressure (allkeys-lru policy) while leaving sorted-set index entries intact,
// causing garbage collection to fail for the whole batch.
func TestLoadSandboxesBySessionIDs_OrphanedZSetEntry(t *testing.T) {
	ctx := context.Background()
	c, mr := newTestRedisClient(t)

	now := time.Now().UTC().Truncate(time.Second)

	sb1 := newTestSandbox("sb-orphan", "sess-orphan", now.Add(-1*time.Hour))
	sb2 := newTestSandbox("sb-alive", "sess-alive", now.Add(-2*time.Hour))

	if err := c.StoreSandbox(ctx, sb1); err != nil {
		t.Fatalf("StoreSandbox sb1 error: %v", err)
	}
	if err := c.StoreSandbox(ctx, sb2); err != nil {
		t.Fatalf("StoreSandbox sb2 error: %v", err)
	}

	// Simulate Redis evicting the hash key for sb1 while leaving its zset entry.
	mr.Del(c.sessionKey("sess-orphan"))

	result, err := c.loadSandboxesBySessionIDs(ctx, []string{"sess-orphan", "sess-alive"})
	if err != nil {
		t.Fatalf("expected no error with orphaned zset entry, got: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 sandbox (the non-evicted one), got %d", len(result))
	}
	if result[0].SandboxID != "sb-alive" {
		t.Fatalf("expected sb-alive, got %s", result[0].SandboxID)
	}
}

func TestListInactiveSandboxes_PopulatesLastActivityAt(t *testing.T) {
	ctx := context.Background()
	c, _ := newTestRedisClient(t)

	now := time.Now().UTC().Truncate(time.Second)

	assert.NoError(t, c.StoreSandbox(ctx, newTestSandbox("sb-1", "sess-1", now.Add(10*time.Minute))))
	assert.NoError(t, c.StoreSandbox(ctx, newTestSandbox("sb-2", "sess-2", now.Add(10*time.Minute))))
	assert.NoError(t, c.UpdateSessionLastActivity(ctx, "sess-1", now.Add(-3*time.Hour)))
	assert.NoError(t, c.UpdateSessionLastActivity(ctx, "sess-2", now.Add(-2*time.Hour)))

	list, err := c.ListInactiveSandboxes(ctx, now, 10)
	assert.NoError(t, err)
	assert.Len(t, list, 2)

	bySession := map[string]*types.SandboxInfo{}
	for _, sb := range list {
		bySession[sb.SessionID] = sb
	}
	// LastActivityAt must reflect the score written by UpdateSessionLastActivity.
	assert.Equal(t, now.Add(-3*time.Hour).Unix(), bySession["sess-1"].LastActivityAt.Unix())
	assert.Equal(t, now.Add(-2*time.Hour).Unix(), bySession["sess-2"].LastActivityAt.Unix())
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

func TestRedisStore_GetSandboxByE2BSandboxID(t *testing.T) {
	ctx := context.Background()
	c, _ := newTestRedisClient(t)

	now := time.Now().UTC().Truncate(time.Second)
	sb := newTestSandboxWithE2B("sb-1", "sess-1", now.Add(10*time.Minute), "e2b-123", "hash-abc", "tpl-1")

	if err := c.StoreSandbox(ctx, sb); err != nil {
		t.Fatalf("StoreSandbox error: %v", err)
	}

	got, err := c.GetSandboxByE2BSandboxID(ctx, "e2b-123")
	if err != nil {
		t.Fatalf("GetSandboxByE2BSandboxID error: %v", err)
	}
	if got.SessionID != "sess-1" {
		t.Fatalf("expected sessionID sess-1, got %s", got.SessionID)
	}

	_, err = c.GetSandboxByE2BSandboxID(ctx, "non-existent")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestRedisStore_ListSandboxesByAPIKeyHash(t *testing.T) {
	ctx := context.Background()
	c, _ := newTestRedisClient(t)

	now := time.Now().UTC().Truncate(time.Second)
	sb1 := newTestSandboxWithE2B("sb-1", "sess-1", now.Add(10*time.Minute), "e2b-1", "hash-abc", "tpl-1")
	sb2 := newTestSandboxWithE2B("sb-2", "sess-2", now.Add(10*time.Minute), "e2b-2", "hash-abc", "tpl-1")
	sb3 := newTestSandboxWithE2B("sb-3", "sess-3", now.Add(10*time.Minute), "e2b-3", "hash-def", "tpl-2")

	if err := c.StoreSandbox(ctx, sb1); err != nil {
		t.Fatalf("StoreSandbox sb1 error: %v", err)
	}
	if err := c.StoreSandbox(ctx, sb2); err != nil {
		t.Fatalf("StoreSandbox sb2 error: %v", err)
	}
	if err := c.StoreSandbox(ctx, sb3); err != nil {
		t.Fatalf("StoreSandbox sb3 error: %v", err)
	}

	list, err := c.ListSandboxesByAPIKeyHash(ctx, "hash-abc")
	if err != nil {
		t.Fatalf("ListSandboxesByAPIKeyHash error: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 sandboxes, got %d", len(list))
	}
	ids := map[string]bool{}
	for _, sb := range list {
		ids[sb.SandboxID] = true
	}
	if !ids["sb-1"] || !ids["sb-2"] {
		t.Fatalf("unexpected sandbox IDs in result: %+v", ids)
	}

	list, err = c.ListSandboxesByAPIKeyHash(ctx, "hash-def")
	if err != nil {
		t.Fatalf("ListSandboxesByAPIKeyHash error: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 sandbox, got %d", len(list))
	}
	if list[0].SandboxID != "sb-3" {
		t.Fatalf("expected sb-3, got %s", list[0].SandboxID)
	}

	list, err = c.ListSandboxesByAPIKeyHash(ctx, "hash-nonexistent")
	if err != nil {
		t.Fatalf("ListSandboxesByAPIKeyHash error: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected 0 sandboxes, got %d", len(list))
	}
}

func TestRedisStore_UpdateSandboxTTL(t *testing.T) {
	ctx := context.Background()
	c, mr := newTestRedisClient(t)

	now := time.Now().UTC().Truncate(time.Second)
	sb := newTestSandbox("sb-1", "sess-1", now.Add(10*time.Minute))

	if err := c.StoreSandbox(ctx, sb); err != nil {
		t.Fatalf("StoreSandbox error: %v", err)
	}

	newExpiresAt := now.Add(30 * time.Minute)
	if err := c.UpdateSandboxTTL(ctx, "sess-1", newExpiresAt); err != nil {
		t.Fatalf("UpdateSandboxTTL error: %v", err)
	}

	got, err := c.GetSandboxBySessionID(ctx, "sess-1")
	if err != nil {
		t.Fatalf("GetSandboxBySessionID error: %v", err)
	}
	if got.ExpiresAt.Unix() != newExpiresAt.Unix() {
		t.Fatalf("expected ExpiresAt %v, got %v", newExpiresAt.Unix(), got.ExpiresAt.Unix())
	}

	score, err := mr.ZScore(c.expiryIndexKey, "sess-1")
	if err != nil {
		t.Fatalf("ZScore error: %v", err)
	}
	if int64(score) != newExpiresAt.Unix() {
		t.Fatalf("expected expiry score %v, got %v", newExpiresAt.Unix(), int64(score))
	}
}

func TestRedisStore_DeleteSandboxBySessionID_CleansIndexes(t *testing.T) {
	ctx := context.Background()
	c, mr := newTestRedisClient(t)

	now := time.Now().UTC().Truncate(time.Second)
	sb := newTestSandboxWithE2B("sb-1", "sess-1", now.Add(10*time.Minute), "e2b-123", "hash-abc", "tpl-1")

	if err := c.StoreSandbox(ctx, sb); err != nil {
		t.Fatalf("StoreSandbox error: %v", err)
	}

	if err := c.DeleteSandboxBySessionID(ctx, "sess-1"); err != nil {
		t.Fatalf("DeleteSandboxBySessionID error: %v", err)
	}

	_, err := mr.Get(c.e2bIDKey("e2b-123"))
	if !errors.Is(err, miniredis.ErrKeyNotFound) {
		t.Fatalf("expected e2bID key deleted, got err=%v", err)
	}

	members, err := mr.SMembers(c.apiKeySetKey("hash-abc"))
	if err != nil && !errors.Is(err, miniredis.ErrKeyNotFound) {
		t.Fatalf("SMembers error: %v", err)
	}
	if len(members) != 0 {
		t.Fatalf("expected apikey set empty, got %v", members)
	}
}

func TestRedisStore_StoreSandbox_E2BIDConflict(t *testing.T) {
	ctx := context.Background()
	c, _ := newTestRedisClient(t)

	now := time.Now().UTC().Truncate(time.Second)
	sb1 := newTestSandboxWithE2B("sb-1", "sess-1", now.Add(10*time.Minute), "e2b-conflict", "hash-1", "tpl-1")
	sb2 := newTestSandboxWithE2B("sb-2", "sess-2", now.Add(10*time.Minute), "e2b-conflict", "hash-2", "tpl-2")

	if err := c.StoreSandbox(ctx, sb1); err != nil {
		t.Fatalf("StoreSandbox first sandbox error: %v", err)
	}

	err := c.StoreSandbox(ctx, sb2)
	if !errors.Is(err, ErrIDConflict) {
		t.Fatalf("expected ErrIDConflict, got %v", err)
	}
}

func TestRedisStore_StoreSandbox_SessionConflict(t *testing.T) {
	ctx := context.Background()
	c, _ := newTestRedisClient(t)

	now := time.Now().UTC().Truncate(time.Second)
	sb1 := newTestSandboxWithE2B("sb-1", "sess-conflict", now.Add(10*time.Minute), "e2b-1", "hash-1", "tpl-1")
	sb2 := newTestSandboxWithE2B("sb-2", "sess-conflict", now.Add(10*time.Minute), "e2b-2", "hash-2", "tpl-2")

	if err := c.StoreSandbox(ctx, sb1); err != nil {
		t.Fatalf("StoreSandbox first sandbox error: %v", err)
	}

	err := c.StoreSandbox(ctx, sb2)
	if !errors.Is(err, ErrIDConflict) {
		t.Fatalf("expected ErrIDConflict, got %v", err)
	}
}
