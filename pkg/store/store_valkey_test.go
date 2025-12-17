package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/assert"
	"github.com/valkey-io/valkey-go"

	"github.com/volcano-sh/agentcube/pkg/common/types"
)

func TestMakeValkeyOptions(t *testing.T) {
	t.Run("missing VALKEY_ADDR", func(t *testing.T) {
		t.Setenv("VALKEY_PASSWORD", "test_pwd")
		opts, err := makeValkeyOptions()
		assert.Nil(t, opts)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "missing env var VALKEY_ADDR")
	})

	t.Run("missing VALKEY_PASSWORD", func(t *testing.T) {
		t.Setenv("VALKEY_ADDR", "127.0.0.1:6379")
		opts, err := makeValkeyOptions()
		assert.Nil(t, opts)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "missing env var VALKEY_PASSWORD")
	})

	t.Run("all basic env vars exist", func(t *testing.T) {
		expectedAddr := "127.0.0.1:6379,127.0.0.1:6380"
		// nolint:gosec
		expectedPwd := "test_valkey_pwd"
		t.Setenv("VALKEY_ADDR", expectedAddr)
		t.Setenv("VALKEY_PASSWORD", expectedPwd)

		opts, err := makeValkeyOptions()
		assert.NoError(t, err)
		assert.NotNil(t, opts)
		assert.Equal(t, strings.Split(expectedAddr, ","), opts.InitAddress)
		assert.Equal(t, expectedPwd, opts.Password)
		assert.False(t, opts.DisableCache)
		assert.False(t, opts.ForceSingleClient)
	})

	t.Run("with VALKEY_DISABLE_CACHE true", func(t *testing.T) {
		t.Setenv("VALKEY_ADDR", "127.0.0.1:6379")
		t.Setenv("VALKEY_PASSWORD", "test_pwd")
		t.Setenv("VALKEY_DISABLE_CACHE", "true")

		opts, err := makeValkeyOptions()
		assert.NoError(t, err)
		assert.True(t, opts.DisableCache)
	})

	t.Run("with VALKEY_DISABLE_CACHE invalid value", func(t *testing.T) {
		t.Setenv("VALKEY_ADDR", "127.0.0.1:6379")
		t.Setenv("VALKEY_PASSWORD", "test_pwd")
		t.Setenv("VALKEY_DISABLE_CACHE", "invalid")

		opts, err := makeValkeyOptions()
		assert.NoError(t, err)
		assert.False(t, opts.DisableCache)
	})

	t.Run("with VALKEY_FORCE_SINGLE true", func(t *testing.T) {
		t.Setenv("VALKEY_ADDR", "127.0.0.1:6379")
		t.Setenv("VALKEY_PASSWORD", "test_pwd")
		t.Setenv("VALKEY_FORCE_SINGLE", "true")

		opts, err := makeValkeyOptions()
		assert.NoError(t, err)
		assert.True(t, opts.ForceSingleClient)
	})

	t.Run("with VALKEY_FORCE_SINGLE invalid value", func(t *testing.T) {
		t.Setenv("VALKEY_ADDR", "127.0.0.1:6379")
		t.Setenv("VALKEY_PASSWORD", "test_pwd")
		t.Setenv("VALKEY_FORCE_SINGLE", "invalid")

		opts, err := makeValkeyOptions()
		assert.NoError(t, err)
		assert.False(t, opts.ForceSingleClient)
	})

	t.Run("with both disable cache and force single true", func(t *testing.T) {
		t.Setenv("VALKEY_ADDR", "127.0.0.1:6379")
		t.Setenv("VALKEY_PASSWORD", "test_pwd")
		t.Setenv("VALKEY_DISABLE_CACHE", "true")
		t.Setenv("VALKEY_FORCE_SINGLE", "true")

		opts, err := makeValkeyOptions()
		assert.NoError(t, err)
		assert.True(t, opts.DisableCache)
		assert.True(t, opts.ForceSingleClient)
	})
}

func newValkeyTestClient(t *testing.T) (*valkeyStore, *miniredis.Miniredis) {
	t.Helper()

	mr := miniredis.RunT(t)

	valkeyClientOptions := &valkey.ClientOption{
		InitAddress:       []string{mr.Addr()},
		DisableCache:      true,
		ForceSingleClient: true,
	}

	client, err := valkey.NewClient(*valkeyClientOptions)
	if err != nil {
		t.Fatalf("valkey NewClient failed: %v", err)
	}

	rs := &valkeyStore{
		cli:                  client,
		sessionPrefix:        "session:",
		expiryIndexKey:       "sandbox:expiry",
		lastActivityIndexKey: "sandbox:last_activity",
	}
	return rs, mr
}

func TestValkeyStore_Ping(t *testing.T) {
	ctx := context.Background()
	c, _ := newValkeyTestClient(t)

	err := c.Ping(ctx)
	assert.Nil(t, err)
}

func TestValkeyStore_GetSandboxBySessionID(t *testing.T) {
	ctx := context.Background()
	c, mr := newValkeyTestClient(t)

	_, err := c.GetSandboxBySessionID(ctx, "non-existent")
	if err == nil {
		t.Fatalf("expected error for non-existent session")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}

	sandboxStoreStruct := &types.SandboxInfo{
		SessionID:        "TestValkeyStore_GetSandboxBySessionID-SID-01",
		SandboxNamespace: "agent-cube",
		Name:             "TestValkeyStore_GetSandboxBySessionID-NAME-01",
		ExpiresAt:        time.Now(),
	}
	err = c.StoreSandbox(ctx, sandboxStoreStruct)
	assert.Nil(t, err)

	sandboxGot, err := c.GetSandboxBySessionID(ctx, "TestValkeyStore_GetSandboxBySessionID-SID-01")
	if err != nil {
		t.Fatalf("expected error for non-existent session")
	}
	assert.NotNil(t, sandboxGot)
	assert.Equal(t, sandboxStoreStruct.SessionID, sandboxGot.SessionID)
	assert.Equal(t, sandboxStoreStruct.Name, sandboxGot.Name)

	sandboxStoreStruct = &types.SandboxInfo{
		SessionID:        "TestValkeyStore_GetSandboxBySessionID-SID-01",
		SandboxNamespace: "agent-cube",
		Name:             "TestValkeyStore_GetSandboxBySessionID-NAME-02",
		ExpiresAt:        time.Now(),
	}
	err = c.UpdateSandbox(ctx, sandboxStoreStruct)
	assert.Nil(t, err)

	_, err = mr.ZScore(c.expiryIndexKey, sandboxStoreStruct.SessionID)
	assert.NoError(t, err, "ZScore expiry should not be error")

	_, err = mr.ZScore(c.lastActivityIndexKey, sandboxStoreStruct.SessionID)
	assert.NoError(t, err)
	assert.NoError(t, err, "ZScore lastActivity should not be error")

	err = c.DeleteSandboxBySessionID(ctx, "TestValkeyStore_GetSandboxBySessionID-SID-01")
	assert.Nil(t, err)

	_, err = c.GetSandboxBySessionID(ctx, "TestValkeyStore_GetSandboxBySessionID-SID-01")
	assert.True(t, errors.Is(err, ErrNotFound))

	_, err = mr.ZScore(c.expiryIndexKey, sandboxStoreStruct.SessionID)
	assert.True(t, errors.Is(err, miniredis.ErrKeyNotFound))

	_, err = mr.ZScore(c.lastActivityIndexKey, sandboxStoreStruct.SessionID)
	assert.True(t, errors.Is(err, miniredis.ErrKeyNotFound))

	err = c.DeleteSandboxBySessionID(ctx, "TestValkeyStore_GetSandboxBySessionID-SID-01-NotExists")
	assert.Nil(t, err)
}

func TestValkeyStore_UpdateSandbox(t *testing.T) {
	ctx := context.Background()
	c, _ := newValkeyTestClient(t)

	sandboxStoreStruct := &types.SandboxInfo{
		SessionID:        "TestValkeyStore_UpdateSandbox-SID-02",
		SandboxNamespace: "agent-cube",
		Name:             "TestValkeyStore_UpdateSandbox-Name",
		ExpiresAt:        time.Now(),
	}
	err := c.UpdateSandbox(ctx, sandboxStoreStruct)
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "key not exists")
}

func TestValkeyStore_ListExpiredSandboxes(t *testing.T) {
	ctx := context.Background()
	c, _ := newValkeyTestClient(t)

	now := time.Now().UTC().Truncate(time.Second)

	sb1 := newTestSandbox("sb-1", "sess-1", now.Add(-5*time.Hour))
	sb2 := newTestSandbox("sb-2", "sess-2", now.Add(-3*time.Hour))
	sb3 := newTestSandbox("sb-3", "sess-3", now.Add(-1*time.Hour))

	if err := c.StoreSandbox(ctx, sb1); err != nil {
		t.Fatalf("TestValkeyStore_ListExpiredSandboxes StoreSandbox sb1 error: %v", err)
	}
	if err := c.StoreSandbox(ctx, sb2); err != nil {
		t.Fatalf("TestValkeyStore_ListExpiredSandboxes StoreSandbox sb2 error: %v", err)
	}
	if err := c.StoreSandbox(ctx, sb3); err != nil {
		t.Fatalf("TestValkeyStore_ListExpiredSandboxes StoreSandbox sb3 error: %v", err)
	}

	sandboxes, err := c.ListExpiredSandboxes(ctx, now.Add(-2*time.Hour), 10)
	if err != nil {
		t.Fatalf("TestValkeyStore_ListExpiredSandboxes error: %v", err)
	}
	assert.Len(t, sandboxes, 2)
	assert.Equal(t, "sess-1", sandboxes[0].SessionID)
	assert.Equal(t, "sess-2", sandboxes[1].SessionID)

	// Limit should be respected.
	sandboxes, err = c.ListExpiredSandboxes(ctx, now, 1)
	if err != nil {
		t.Fatalf("ListExpiredSandboxes with limit error: %v", err)
	}
	if len(sandboxes) != 1 {
		t.Fatalf("expected 1 expired sandbox with limit=1, got %d", len(sandboxes))
	}
	assert.Equal(t, "sess-1", sandboxes[0].SessionID)

	sandboxes, err = c.ListExpiredSandboxes(ctx, now, 100)
	assert.Nil(t, err)
	assert.Len(t, sandboxes, 3)

	sessionIDs := []string{
		"sess-1",
		"sess-2",
		"sb-3",
		"sb-4",
		"sess-3",
	}
	sandboxes, err = c.loadSandboxesBySessionIDs(context.Background(), sessionIDs)
	assert.Nil(t, err)
	assert.Len(t, sandboxes, 3)

	sessionIDs = []string{
		"sb-1",
		"sb-2",
		"sb-3",
		"sb-4",
	}
	sandboxes, err = c.loadSandboxesBySessionIDs(context.Background(), sessionIDs)
	assert.Nil(t, err)
	assert.Len(t, sandboxes, 0)
}

func TestValkeyStore_ListInactiveSandboxes(t *testing.T) {
	ctx := context.Background()
	c, _ := newValkeyTestClient(t)

	now := time.Now().UTC().Truncate(time.Second)

	sb1 := newTestSandbox("sb-1", "sess-1", now.Add(15*time.Minute))
	sb2 := newTestSandbox("sb-2", "sess-2", now.Add(14*time.Minute))
	sb3 := newTestSandbox("sb-3", "sess-3", now.Add(13*time.Minute))
	sb4 := newTestSandbox("sb-4", "sess-4", now.Add(12*time.Minute))
	sb5 := newTestSandbox("sb-5", "sess-5", now.Add(11*time.Minute))

	assert.NoError(t, c.StoreSandbox(ctx, sb1))
	assert.NoError(t, c.StoreSandbox(ctx, sb2))
	assert.NoError(t, c.StoreSandbox(ctx, sb3))
	assert.NoError(t, c.StoreSandbox(ctx, sb4))
	assert.NoError(t, c.StoreSandbox(ctx, sb5))

	assert.NoError(t, c.UpdateSessionLastActivity(ctx, "sess-1", now.Add(-10*time.Hour)))
	assert.NoError(t, c.UpdateSessionLastActivity(ctx, "sess-2", now.Add(-8*time.Hour)))
	assert.NoError(t, c.UpdateSessionLastActivity(ctx, "sess-3", now.Add(-6*time.Hour)))
	assert.NoError(t, c.UpdateSessionLastActivity(ctx, "sess-4", now.Add(-4*time.Hour)))
	assert.NoError(t, c.UpdateSessionLastActivity(ctx, "sess-5", now.Add(-2*time.Hour)))

	expiredSandboxes, err := c.ListInactiveSandboxes(ctx, now.Add(-5*time.Hour), 10)
	assert.Nil(t, err)
	assert.Len(t, expiredSandboxes, 3)
	assert.Equal(t, "sess-1", expiredSandboxes[0].SessionID)
	assert.Equal(t, "sess-2", expiredSandboxes[1].SessionID)
	assert.Equal(t, "sess-3", expiredSandboxes[2].SessionID)

	expiredSandboxes, err = c.ListInactiveSandboxes(ctx, now, 1)
	assert.Nil(t, err)
	assert.Len(t, expiredSandboxes, 1)

	expiredSandboxes, err = c.ListInactiveSandboxes(ctx, now, 100)
	assert.Nil(t, err)
	assert.Len(t, expiredSandboxes, 5)
}

func TestValkeyStore_UpdateSandboxLastActivity(t *testing.T) {
	ctx := context.Background()
	c, mr := newValkeyTestClient(t)

	now := time.Now().UTC().Truncate(time.Second)

	sb := newTestSandbox("sb-1", "sess-1", now.Add(30*time.Minute))

	assert.NoError(t, c.StoreSandbox(ctx, sb))

	sandboxStore, err := c.GetSandboxBySessionID(ctx, "sess-1")
	assert.Nil(t, err)
	assert.Equal(t, "sess-1", sandboxStore.SessionID)

	newLastActivity := time.Now().Add(time.Hour)
	assert.NoError(t, c.UpdateSessionLastActivity(ctx, "sess-1", newLastActivity))

	// last_activity index should be updated.
	score, err := mr.ZScore(c.lastActivityIndexKey, "sess-1")
	assert.Nil(t, err)
	assert.Equal(t, newLastActivity.Unix(), int64(score))

	// session not exists
	err = c.UpdateSessionLastActivity(ctx, "sess-1-not-exist", newLastActivity)
	assert.Error(t, err)
	assert.True(t, errors.Is(err, ErrNotFound))
}
