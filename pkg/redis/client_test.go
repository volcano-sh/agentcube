package redis

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
)

func newTestClient(t *testing.T) (*client, *miniredis.Miniredis) {
	t.Helper()

	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("failed to start miniredis: %v", err)
	}

	rdb := goredis.NewClient(&goredis.Options{
		Addr: mr.Addr(),
	})

	c := NewClient(rdb).(*client)
	return c, mr
}

func TestGetSandboxBySessionID_NotFound(t *testing.T) {
	c, mr := newTestClient(t)
	defer mr.Close()

	ctx := context.Background()
	_, err := c.GetSandboxBySessionID(ctx, "non-existing-session")
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestGetSandboxBySessionID_Success(t *testing.T) {
	c, mr := newTestClient(t)
	defer mr.Close()

	ctx := context.Background()

	sb := &Sandbox{
		SandboxID: "sb-1",
		IP:        "10.0.0.1",
		Port:      9000,
		Endpoint:  "10.0.0.1:9000",
		Workload: WorkloadSpec{
			Kind:      Agent,
			Name:      "test-agent",
			Namespace: "default",
		},
	}

	data, err := json.Marshal(sb)
	if err != nil {
		t.Fatalf("failed to marshal sandbox: %v", err)
	}

	sessionID := "sess-1"
	key := c.sessionKey(sessionID)
	if err := mr.Set(key, string(data)); err != nil {
		t.Fatalf("failed to set value in miniredis: %v", err)
	}

	got, err := c.GetSandboxBySessionID(ctx, sessionID)
	if err != nil {
		t.Fatalf("GetSandboxBySessionID returned error: %v", err)
	}

	if got.SandboxID != sb.SandboxID ||
		got.IP != sb.IP ||
		got.Port != sb.Port ||
		got.Endpoint != sb.Endpoint ||
		got.Workload.Kind != sb.Workload.Kind ||
		got.Workload.Name != sb.Workload.Name ||
		got.Workload.Namespace != sb.Workload.Namespace {
		t.Fatalf("sandbox mismatch: got %+v, want %+v", got, sb)
	}
}

func TestSetSessionLockIfAbsent_FirstTime(t *testing.T) {
	c, mr := newTestClient(t)
	defer mr.Close()

	ctx := context.Background()
	sessionID := "sess-lock-1"
	ttl := 5 * time.Minute

	ok, err := c.SetSessionLockIfAbsent(ctx, sessionID, ttl)
	if err != nil {
		t.Fatalf("SetSessionLockIfAbsent returned error: %v", err)
	}
	if !ok {
		t.Fatalf("expected ok=true on first lock, got false")
	}

	lockKey := c.lockKey(sessionID)
	if !mr.Exists(lockKey) {
		t.Fatalf("expected lock key to exist in redis")
	}
}

func TestSetSessionLockIfAbsent_AlreadyExists(t *testing.T) {
	c, mr := newTestClient(t)
	defer mr.Close()

	ctx := context.Background()
	sessionID := "sess-lock-2"
	lockKey := c.lockKey(sessionID)

	// Pre-create lock.
	mr.Set(lockKey, "1")

	ok, err := c.SetSessionLockIfAbsent(ctx, sessionID, time.Minute)
	if err != nil {
		t.Fatalf("SetSessionLockIfAbsent returned error: %v", err)
	}
	if ok {
		t.Fatalf("expected ok=false when lock already exists, got true")
	}
}

func TestBindSessionWithSandbox_NilSandbox(t *testing.T) {
	c, mr := newTestClient(t)
	defer mr.Close()

	ctx := context.Background()

	err := c.BindSessionWithSandbox(ctx, "sess-1", nil, time.Minute)
	if err == nil {
		t.Fatalf("expected error for nil sandbox, got nil")
	}
}

func TestBindSessionWithSandbox_EmptySandboxID(t *testing.T) {
	c, mr := newTestClient(t)
	defer mr.Close()

	ctx := context.Background()

	sb := &Sandbox{
		SandboxID: "",
	}

	err := c.BindSessionWithSandbox(ctx, "sess-1", sb, time.Minute)
	if err == nil {
		t.Fatalf("expected error for empty SandboxID, got nil")
	}
}

func TestBindSessionWithSandbox_Success(t *testing.T) {
	c, mr := newTestClient(t)
	defer mr.Close()

	ctx := context.Background()
	sessionID := "sess-1"

	// prepare a lock to be deleted
	lockKey := c.lockKey(sessionID)
	mr.Set(lockKey, "1")

	sb := &Sandbox{
		SandboxID: "sb-1",
		IP:        "10.0.0.1",
		Port:      9000,
		Endpoint:  "10.0.0.1:9000",
		Workload: WorkloadSpec{
			Kind:      CodeInterpreter,
			Name:      "ci",
			Namespace: "default",
		},
	}

	ttl := time.Minute

	if err := c.BindSessionWithSandbox(ctx, sessionID, sb, ttl); err != nil {
		t.Fatalf("BindSessionWithSandbox returned error: %v", err)
	}

	sessionKey := c.sessionKey(sessionID)
	sandboxKey := c.sandboxKey(sb.SandboxID)

	// session -> sandbox json
	raw, err := mr.Get(sessionKey)
	if err != nil {
		t.Fatalf("expected session key to exist: %v", err)
	}
	var got Sandbox
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("failed to unmarshal sandbox from redis: %v", err)
	}
	if got.SandboxID != sb.SandboxID {
		t.Fatalf("sandbox mismatch: got %s, want %s", got.SandboxID, sb.SandboxID)
	}

	// sandbox -> session id
	gotSessionID, err := mr.Get(sandboxKey)
	if err != nil {
		t.Fatalf("expected sandbox key to exist: %v", err)
	}
	if gotSessionID != sessionID {
		t.Fatalf("sandbox mapping mismatch: got %s, want %s", gotSessionID, sessionID)
	}

	// lock should be deleted
	if mr.Exists(lockKey) {
		t.Fatalf("expected lock key to be deleted")
	}
}

func TestDeleteSessionBySandboxIDTx_NotFound(t *testing.T) {
	c, mr := newTestClient(t)
	defer mr.Close()

	ctx := context.Background()
	sandboxID := "non-existing-sb"
	sandboxKey := c.sandboxKey(sandboxID)

	if mr.Exists(sandboxKey) {
		t.Fatalf("expected sandbox key not to exist in redis before test")
	}

	if err := c.DeleteSessionBySandboxIDTx(ctx, sandboxID); err != nil {
		t.Fatalf("expected nil error when sandbox does not exist, got %v", err)
	}
}

func TestDeleteSessionBySandboxIDTx_Success(t *testing.T) {
	c, mr := newTestClient(t)
	defer mr.Close()

	ctx := context.Background()
	sandboxID := "sb-1"
	sessionID := "sess-1"

	sandboxKey := c.sandboxKey(sandboxID)
	sessionKey := c.sessionKey(sessionID)

	// create bidirectional mapping
	mr.Set(sandboxKey, sessionID)
	mr.Set(sessionKey, `{"sandbox_id":"sb-1"}`)

	if err := c.DeleteSessionBySandboxIDTx(ctx, sandboxID); err != nil {
		t.Fatalf("DeleteSessionBySandboxIDTx returned error: %v", err)
	}

	if mr.Exists(sandboxKey) {
		t.Fatalf("expected sandbox key to be deleted")
	}
	if mr.Exists(sessionKey) {
		t.Fatalf("expected session key to be deleted")
	}
}

func TestNewClient_WithKeyPrefixes(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("failed to start miniredis: %v", err)
	}
	defer mr.Close()

	rdb := goredis.NewClient(&goredis.Options{
		Addr: mr.Addr(),
	})

	customSessionPrefix := "custom_session:"
	customSandboxPrefix := "custom_sandbox:"
	customLockPrefix := "custom_lock:"

	c := NewClient(
		rdb,
		WithKeyPrefixes(customSessionPrefix, customSandboxPrefix, customLockPrefix),
	).(*client)

	if got := c.sessionKey("id"); got != customSessionPrefix+"id" {
		t.Fatalf("sessionKey prefix mismatch: got %s", got)
	}
	if got := c.sandboxKey("id"); got != customSandboxPrefix+"id" {
		t.Fatalf("sandboxKey prefix mismatch: got %s", got)
	}
	if got := c.lockKey("id"); got != customLockPrefix+"id" {
		t.Fatalf("lockKey prefix mismatch: got %s", got)
	}
}
