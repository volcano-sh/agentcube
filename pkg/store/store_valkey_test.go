package store

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/alicebob/miniredis/v2/server"
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
	mr.Server().SetPreHook(func(c *server.Peer, cmd string, args ...string) bool {
		if strings.ToUpper(cmd) == "CLIENT" && len(args) > 0 {
			sub := strings.ToUpper(args[0])
			if sub == "SETINFO" || sub == "TRACKING" {
				c.WriteOK()
				return true
			}
			if sub == "ID" {
				c.WriteInt(1)
				return true
			}
		}
		return false
	})

	dialer := func(ctx context.Context, addr string, d *net.Dialer, _ *tls.Config) (net.Conn, error) {
		return d.DialContext(ctx, "tcp", addr)
	}

	cli, err := valkey.NewClient(valkey.ClientOption{
		InitAddress:       []string{mr.Addr()},
		DisableCache:      true,
		AlwaysRESP2:       true,
		ForceSingleClient: true,
		DialCtxFn:         dialer,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &valkeyStore{
		cli:                  cli,
		sessionPrefix:        "session:",
		expiryIndexKey:       "sandbox:expiry",
		lastActivityIndexKey: "sandbox:last_activity",
	}, mr
}

func TestValkeyStore_Ping(t *testing.T) {
	ctx := context.Background()
	c, _ := newValkeyTestClient(t)
	assert.NoError(t, c.Ping(ctx))
}

func TestValkeyStore_GetSandboxBySessionID(t *testing.T) {
	ctx := context.Background()
	c, _ := newValkeyTestClient(t)

	_, err := c.GetSandboxBySessionID(ctx, "non-existent")
	assert.Error(t, err)
	assert.True(t, errors.Is(err, ErrNotFound))

	in := &types.SandboxInfo{
		SessionID:        "TestValkeyStore_GetSandboxBySessionID-SID-01",
		SandboxNamespace: "agent-cube",
		Name:             "TestValkeyStore_GetSandboxBySessionID-NAME-01",
		ExpiresAt:        time.Now(),
	}
	assert.NoError(t, c.StoreSandbox(ctx, in))

	out, err := c.GetSandboxBySessionID(ctx, in.SessionID)
	assert.NoError(t, err)
	assert.Equal(t, in.SessionID, out.SessionID)
	assert.Equal(t, in.Name, out.Name)

	assert.NoError(t, c.DeleteSandboxBySessionID(ctx, in.SessionID))
	_, err = c.GetSandboxBySessionID(ctx, in.SessionID)
	assert.True(t, errors.Is(err, ErrNotFound))
}

func TestValkeyStore_UpdateSandbox(t *testing.T) {
	ctx := context.Background()
	c, _ := newValkeyTestClient(t)
	in := &types.SandboxInfo{SessionID: "TestValkeyStore_UpdateSandbox-SID-02", ExpiresAt: time.Now()}
	err := c.UpdateSandbox(ctx, in)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "key not exists")
}

func TestValkeyStore_ListExpiredSandboxes(t *testing.T) {
	ctx := context.Background()
	c, _ := newValkeyTestClient(t)
	now := time.Now().UTC().Truncate(time.Second)

	sb1 := newTestSandbox("sb-1", "sess-1", now.Add(-5*time.Hour))
	sb2 := newTestSandbox("sb-2", "sess-2", now.Add(-3*time.Hour))
	sb3 := newTestSandbox("sb-3", "sess-3", now.Add(-1*time.Hour))

	assert.NoError(t, c.StoreSandbox(ctx, sb1))
	assert.NoError(t, c.StoreSandbox(ctx, sb2))
	assert.NoError(t, c.StoreSandbox(ctx, sb3))

	list, err := c.ListExpiredSandboxes(ctx, now.Add(-2*time.Hour), 10)
	assert.NoError(t, err)
	assert.Len(t, list, 2)
	assert.Equal(t, "sess-1", list[0].SessionID)
	assert.Equal(t, "sess-2", list[1].SessionID)

	list, err = c.ListExpiredSandboxes(ctx, now, 1)
	assert.NoError(t, err)
	assert.Len(t, list, 1)
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

	list, err := c.ListInactiveSandboxes(ctx, now.Add(-5*time.Hour), 10)
	assert.NoError(t, err)
	assert.Len(t, list, 3)

	list, err = c.ListInactiveSandboxes(ctx, now, 1)
	assert.NoError(t, err)
	assert.Len(t, list, 1)

	list, err = c.ListInactiveSandboxes(ctx, now, 100)
	assert.NoError(t, err)
	assert.Len(t, list, 5)
}

func TestValkeyStore_UpdateSandboxLastActivity(t *testing.T) {
	ctx := context.Background()
	c, mr := newValkeyTestClient(t)
	now := time.Now().UTC().Truncate(time.Second)
	sb := newTestSandbox("sb-1", "sess-1", now.Add(30*time.Minute))

	assert.NoError(t, c.StoreSandbox(ctx, sb))

	newLastActivity := time.Now().Add(time.Hour)
	assert.NoError(t, c.UpdateSessionLastActivity(ctx, "sess-1", newLastActivity))

	score, err := mr.ZScore(c.lastActivityIndexKey, "sess-1")
	assert.NoError(t, err)
	assert.Equal(t, newLastActivity.Unix(), int64(score))

	err = c.UpdateSessionLastActivity(ctx, "sess-1-not-exist", newLastActivity)
	assert.Error(t, err)
	assert.True(t, errors.Is(err, ErrNotFound))
}

func TestValkeyStoreContract(t *testing.T) {
	runContractTests(t, func(t *testing.T) Store {
		s, _ := newValkeyTestClient(t)
		return s
	})
}