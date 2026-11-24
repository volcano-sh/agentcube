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
	"strings"
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
	assert.Contains(t, err.Error(), "key does not exist")
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

	// session not exists
	err = c.UpdateSessionLastActivity(ctx, "sess-1-not-exist", newLastActivity)
	if err == nil {
		t.Fatalf("expected error for non-existent session")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestRedisStore_UpdateSandboxExpiry(t *testing.T) {
	ctx := context.Background()
	c, mr := newTestRedisClient(t)

	now := time.Now().UTC().Truncate(time.Second)
	initial := now.Add(10 * time.Minute)
	sb := newTestSandbox("sb-1", "sess-1", initial)
	sb.OwnerID = "owner-a"
	sb.EntryPoints = []types.SandboxEntryPoint{{Path: "/", Protocol: "HTTP", Endpoint: "10.0.0.1:49983"}}
	sb.Metadata = map[string]string{"env": "dev"}

	assert.NoError(t, c.StoreSandbox(ctx, sb))

	// extendOnly with earlier timestamp should preserve initial expiry.
	shorter := now.Add(5 * time.Minute)
	got, err := c.UpdateSandboxExpiry(ctx, "sess-1", shorter, true)
	assert.NoError(t, err)
	assert.Equal(t, initial.Unix(), got.ExpiresAt.Unix())

	// extendOnly with later timestamp should extend expiry and update ZSet.
	longer := now.Add(20 * time.Minute)
	got, err = c.UpdateSandboxExpiry(ctx, "sess-1", longer, true)
	assert.NoError(t, err)
	assert.Equal(t, longer.Unix(), got.ExpiresAt.Unix())
	assert.Equal(t, "dev", got.Metadata["env"])
	assert.Len(t, got.EntryPoints, 1)

	score, err := mr.ZScore(c.expiryIndexKey, "sess-1")
	assert.NoError(t, err)
	assert.Equal(t, longer.Unix(), int64(score))

	// set mode (extendOnly=false) can shorten expiry.
	got, err = c.UpdateSandboxExpiry(ctx, "sess-1", shorter, false)
	assert.NoError(t, err)
	assert.Equal(t, shorter.Unix(), got.ExpiresAt.Unix())
	score, err = mr.ZScore(c.expiryIndexKey, "sess-1")
	assert.NoError(t, err)
	assert.Equal(t, shorter.Unix(), int64(score))

	// missing session returns ErrNotFound.
	_, err = c.UpdateSandboxExpiry(ctx, "missing", longer, false)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestRedisStore_ListActiveSandboxes(t *testing.T) {
	ctx := context.Background()
	c, _ := newTestRedisClient(t)

	now := time.Now().UTC().Truncate(time.Second)

	sb1 := newTestSandbox("sb-1", "sess-1", now.Add(10*time.Minute))
	sb1.OwnerID = "owner-a"
	sb1.SandboxNamespace = "ns-1"
	sb1.CreatedAt = now.Add(-2 * time.Minute)

	sb2 := newTestSandbox("sb-2", "sess-2", now.Add(10*time.Minute))
	sb2.OwnerID = "owner-a"
	sb2.SandboxNamespace = "ns-2"
	sb2.CreatedAt = now.Add(-1 * time.Minute)

	sb3 := newTestSandbox("sb-3", "sess-3", now.Add(-1*time.Minute)) // expired
	sb3.OwnerID = "owner-a"
	sb3.SandboxNamespace = "ns-1"

	sb4 := newTestSandbox("sb-4", "sess-4", now.Add(10*time.Minute))
	sb4.OwnerID = "owner-b"
	sb4.SandboxNamespace = "ns-1"

	for _, sb := range []*types.SandboxInfo{sb1, sb2, sb3, sb4} {
		assert.NoError(t, c.StoreSandbox(ctx, sb))
	}

	activeOwnerA, err := c.ListActiveSandboxes(ctx, "owner-a", "")
	assert.NoError(t, err)
	assert.Len(t, activeOwnerA, 2)
	assert.Equal(t, "sess-2", activeOwnerA[0].SessionID)
	assert.Equal(t, "sess-1", activeOwnerA[1].SessionID)

	activeOwnerANS1, err := c.ListActiveSandboxes(ctx, "owner-a", "ns-1")
	assert.NoError(t, err)
	assert.Len(t, activeOwnerANS1, 1)
	assert.Equal(t, "sess-1", activeOwnerANS1[0].SessionID)

	assert.NoError(t, c.DeleteSandboxBySessionID(ctx, "sess-1"))
	activeOwnerAAfterDelete, err := c.ListActiveSandboxes(ctx, "owner-a", "ns-1")
	assert.NoError(t, err)
	assert.Empty(t, activeOwnerAAfterDelete)
}

func TestRedisStore_ExcludesIdleExemptFromInactiveIndex(t *testing.T) {
	ctx := context.Background()
	c, _ := newTestRedisClient(t)

	now := time.Now().UTC().Truncate(time.Second)
	exempt := newTestSandbox("sb-exempt", "sess-exempt", now.Add(30*time.Minute))
	exempt.DisableIdleTimeout = true
	native := newTestSandbox("sb-native", "sess-native", now.Add(30*time.Minute))

	assert.NoError(t, c.StoreSandbox(ctx, exempt))
	assert.NoError(t, c.StoreSandbox(ctx, native))

	inactive, err := c.ListInactiveSandboxes(ctx, now.Add(time.Minute), 10)
	assert.NoError(t, err)
	assert.Len(t, inactive, 1)
	assert.Equal(t, "sess-native", inactive[0].SessionID)
}

func TestRedisStore_ClaimExpiredSandbox(t *testing.T) {
	ctx := context.Background()
	c, mr := newTestRedisClient(t)

	assert.False(t, strings.Contains(updateSandboxExpiryLua, "KEYS[2]"))
	assert.False(t, strings.Contains(claimExpiredSandboxLua, "KEYS[2]"))

	now := time.Now().UTC().Truncate(time.Second)
	expired := newTestSandbox("sb-exp", "sess-exp", now.Add(-1*time.Minute))
	expired.Status = "ready"
	plus8 := time.FixedZone("UTC+8", 8*3600)
	alive := newTestSandbox("sb-alive", "sess-alive", now.In(plus8).Add(5*time.Minute))
	alive.Status = "ready"

	assert.NoError(t, c.StoreSandbox(ctx, expired))
	assert.NoError(t, c.StoreSandbox(ctx, alive))

	// Unexpired session (even if stored from a non-UTC zone) cannot be claimed.
	claimed, err := c.ClaimExpiredSandbox(ctx, "sess-alive", now)
	assert.NoError(t, err)
	assert.False(t, claimed)

	// Expired session is claimed once, retains an expiry-index lease, blocks immediate re-claim,
	// blocks TTL updates, and becomes claimable again after the lease elapses.
	claimed, err = c.ClaimExpiredSandbox(ctx, "sess-exp", now)
	assert.NoError(t, err)
	assert.True(t, claimed)

	score, err := mr.ZScore(c.expiryIndexKey, "sess-exp")
	assert.NoError(t, err)
	assert.Equal(t, now.Add(expiredClaimLease).Unix(), int64(score))

	withinLease, err := c.ListExpiredSandboxes(ctx, now.Add(30*time.Second), 10)
	assert.NoError(t, err)
	assert.Empty(t, withinLease)

	claimedAgain, err := c.ClaimExpiredSandbox(ctx, "sess-exp", now.Add(30*time.Second))
	assert.NoError(t, err)
	assert.False(t, claimedAgain)

	_, err = c.UpdateSandboxExpiry(ctx, "sess-exp", now.Add(10*time.Minute), true)
	assert.ErrorIs(t, err, ErrNotFound)

	afterLease := now.Add(expiredClaimLease)
	retryList, err := c.ListExpiredSandboxes(ctx, afterLease, 10)
	assert.NoError(t, err)
	assert.Len(t, retryList, 1)
	assert.Equal(t, "sess-exp", retryList[0].SessionID)

	reclaimed, err := c.ClaimExpiredSandbox(ctx, "sess-exp", afterLease)
	assert.NoError(t, err)
	assert.True(t, reclaimed)
	score, err = mr.ZScore(c.expiryIndexKey, "sess-exp")
	assert.NoError(t, err)
	assert.Equal(t, afterLease.Add(expiredClaimLease).Unix(), int64(score))
}
