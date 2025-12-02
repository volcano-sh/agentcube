package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	redisv9 "github.com/redis/go-redis/v9"

	"github.com/volcano-sh/agentcube/pkg/common/types"
)

// ErrNotFound indicates that the record is not found in Redis.
var (
	ErrNotFound = errors.New("redis: not found")
)

// Client defines the Redis-backed sandbox session store.
type Client interface {
	// GetSandboxBySessionID returns the sandbox bound to the given session ID.
	GetSandboxBySessionID(ctx context.Context, sessionID string) (*types.SandboxRedis, error)
	// SetSessionLockIfAbsent tries to acquire a session-level lock if it does not exist.
	SetSessionLockIfAbsent(ctx context.Context, sessionID string, ttl time.Duration) (bool, error)
	// BindSessionWithSandbox stores a bidirectional mapping between session and sandbox.
	BindSessionWithSandbox(ctx context.Context, sessionID string, sandboxRedis *types.SandboxRedis, ttl time.Duration) error
	// DeleteSessionBySandboxIDTx removes the bidirectional mapping by sandbox ID.
	DeleteSessionBySandboxIDTx(ctx context.Context, sandboxID string) error
	// StoreSandbox store sandbox into redis
	StoreSandbox(ctx context.Context, sandboxRedis *types.SandboxRedis, ttl time.Duration) error
	// Ping redis ping
	Ping(ctx context.Context) error

	// ListExpiredSandboxes returns up to limit sandboxes with ExpiresAt before the given time.
	ListExpiredSandboxes(ctx context.Context, before time.Time, limit int64) ([]*types.SandboxRedis, error)
	// ListInactiveSandboxes returns up to limit sandboxes with last-activity time before the given time.
	// Last activity is tracked only in the sorted-set index.
	ListInactiveSandboxes(ctx context.Context, before time.Time, limit int64) ([]*types.SandboxRedis, error)
	// UpdateSandboxLastActivity updates the last-activity index for the given sandbox.
	UpdateSandboxLastActivity(ctx context.Context, sandboxID string, at time.Time) error
}

// client is the concrete implementation of Client backed by go-redis.
type client struct {
	rdb *redisv9.Client

	sessionPrefix string
	sandboxPrefix string
	lockPrefix    string

	// Sorted-set indexes:
	//   expiryIndexKey:        score = ExpiresAt.Unix(),     member = sandboxID
	//   lastActivityIndexKey:  score = last-activity.Unix(), member = sandboxID
	expiryIndexKey       string
	lastActivityIndexKey string
}

// NewClient creates a new Client and initializes the underlying go-redis client.
func NewClient(redisOpts *redisv9.Options) Client {
	rdb := redisv9.NewClient(redisOpts)

	return &client{
		rdb:           rdb,
		sessionPrefix: "session:",
		sandboxPrefix: "sandbox:",
		lockPrefix:    "session_lock:",

		expiryIndexKey:       "sandbox:expiry",
		lastActivityIndexKey: "sandbox:last_activity",
	}
}

func (c *client) sessionKey(sessionID string) string {
	return c.sessionPrefix + sessionID
}

func (c *client) sandboxKey(sandboxID string) string {
	return c.sandboxPrefix + sandboxID
}

func (c *client) lockKey(sessionID string) string {
	return c.lockPrefix + sessionID
}

// GetSandboxBySessionID looks up the sandbox bound to the given session ID.
// Underlying Redis: GET session:{sessionID} -> SandboxRedis(JSON).
func (c *client) GetSandboxBySessionID(ctx context.Context, sessionID string) (*types.SandboxRedis, error) {
	key := c.sessionKey(sessionID)

	b, err := c.rdb.Get(ctx, key).Bytes()
	if errors.Is(err, redisv9.Nil) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("GetSandboxBySessionID: redis GET %s: %w", key, err)
	}

	var sandboxRedis types.SandboxRedis
	if err := json.Unmarshal(b, &sandboxRedis); err != nil {
		return nil, fmt.Errorf("GetSandboxBySessionID: unmarshal sandbox: %w", err)
	}
	return &sandboxRedis, nil
}

// SetSessionLockIfAbsent tries to acquire a lock for the given session ID.
// Underlying Redis: SETNX session_lock:{sessionID} 1 EX ttl.
func (c *client) SetSessionLockIfAbsent(ctx context.Context, sessionID string, ttl time.Duration) (bool, error) {
	key := c.lockKey(sessionID)
	ok, err := c.rdb.SetNX(ctx, key, "1", ttl).Result()
	if err != nil {
		return false, fmt.Errorf("SetSessionLockIfAbsent: redis SETNX %s: %w", key, err)
	}
	return ok, nil
}

// BindSessionWithSandbox writes a bidirectional mapping between session and sandbox,
// and updates the expiry index.
//
//	SETEX session:{sessionID} sandboxJSON
//	SETEX sandbox:{sandboxID} sessionID
//	ZADD  sandbox:expiry (ExpiresAt, sandboxID)
//	DEL   session_lock:{sessionID}
func (c *client) BindSessionWithSandbox(ctx context.Context, sessionID string, sandboxRedis *types.SandboxRedis, ttl time.Duration) error {
	if sandboxRedis == nil {
		return errors.New("BindSessionWithSandbox: sandbox is nil")
	}
	if sandboxRedis.SandboxID == "" {
		return errors.New("BindSessionWithSandbox: sandbox.SandboxID is empty")
	}

	sessionKey := c.sessionKey(sessionID)
	sandboxKey := c.sandboxKey(sandboxRedis.SandboxID)
	lockKey := c.lockKey(sessionID)

	b, err := json.Marshal(sandboxRedis)
	if err != nil {
		return fmt.Errorf("BindSessionWithSandbox: marshal sandbox: %w", err)
	}

	pipe := c.rdb.TxPipeline()
	pipe.Set(ctx, sessionKey, b, ttl)
	pipe.Set(ctx, sandboxKey, sessionID, ttl)
	pipe.Del(ctx, lockKey)

	if !sandboxRedis.ExpiresAt.IsZero() {
		pipe.ZAdd(ctx, c.expiryIndexKey, redisv9.Z{
			Score:  float64(sandboxRedis.ExpiresAt.Unix()),
			Member: sandboxRedis.SandboxID,
		})
	}

	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("BindSessionWithSandbox: redis TxPipeline EXEC: %w", err)
	}
	return nil
}

func (c *client) StoreSandbox(ctx context.Context, sandboxRedis *types.SandboxRedis, ttl time.Duration) error {
	// TODO: implement
	return nil
}

func (c *client) Ping(ctx context.Context) error {
	// TODO: implement
	return nil
}

// DeleteSessionBySandboxIDTx deletes the bidirectional mapping by sandboxID and
// removes the related index entries. Missing mappings are treated as success.
func (c *client) DeleteSessionBySandboxIDTx(ctx context.Context, sandboxID string) error {
	sandboxKey := c.sandboxKey(sandboxID)

	// Best-effort lookup of the current session mapping.
	sessionID, err := c.rdb.Get(ctx, sandboxKey).Result()
	if errors.Is(err, redisv9.Nil) {
		// Mapping already gone, treat as success.
		return nil
	}
	if err != nil {
		return fmt.Errorf("DeleteSessionBySandboxIDTx: redis GET %s: %w", sandboxKey, err)
	}

	sessionKey := c.sessionKey(sessionID)

	pipe := c.rdb.Pipeline()
	pipe.Del(ctx, sandboxKey)
	pipe.Del(ctx, sessionKey)
	pipe.ZRem(ctx, c.expiryIndexKey, sandboxID)
	pipe.ZRem(ctx, c.lastActivityIndexKey, sandboxID)

	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("DeleteSessionBySandboxIDTx: pipeline EXEC: %w", err)
	}
	return nil
}

// ListExpiredSandboxes returns up to limit sandboxes whose ExpiresAt is before before.
// It uses a sorted-set index and is linear in the number of results.
func (c *client) ListExpiredSandboxes(ctx context.Context, before time.Time, limit int64) ([]*types.SandboxRedis, error) {
	if limit <= 0 {
		return nil, nil
	}

	maxScore := before.Unix()
	ids, err := c.rdb.ZRangeByScore(ctx, c.expiryIndexKey, &redisv9.ZRangeBy{
		Min:    "-inf",
		Max:    fmt.Sprintf("%d", maxScore),
		Offset: 0,
		Count:  limit,
	}).Result()
	if err != nil {
		return nil, fmt.Errorf("ListExpiredSandboxes: ZRangeByScore: %w", err)
	}

	return c.loadSandboxesByIDs(ctx, ids)
}

// ListInactiveSandboxes returns up to limit sandboxes whose last activity
// time is before before, using the last-activity sorted-set index.
func (c *client) ListInactiveSandboxes(ctx context.Context, before time.Time, limit int64) ([]*types.SandboxRedis, error) {
	if limit <= 0 {
		return nil, nil
	}

	maxScore := before.Unix()
	ids, err := c.rdb.ZRangeByScore(ctx, c.lastActivityIndexKey, &redisv9.ZRangeBy{
		Min:    "-inf",
		Max:    fmt.Sprintf("%d", maxScore),
		Offset: 0,
		Count:  limit,
	}).Result()
	if err != nil {
		return nil, fmt.Errorf("ListInactiveSandboxes: ZRangeByScore: %w", err)
	}

	return c.loadSandboxesByIDs(ctx, ids)
}

// loadSandboxesByIDs loads sandbox objects for the given sandbox IDs.
func (c *client) loadSandboxesByIDs(ctx context.Context, sandboxIDs []string) ([]*types.SandboxRedis, error) {
	if len(sandboxIDs) == 0 {
		return nil, nil
	}

	sessionIDCmds := make([]*redisv9.StringCmd, len(sandboxIDs))
	pipe := c.rdb.Pipeline()
	for i, id := range sandboxIDs {
		sessionKey := c.sandboxKey(id)
		sessionIDCmds[i] = pipe.Get(ctx, sessionKey)
	}
	_, _ = pipe.Exec(ctx)

	type pair struct {
		sandboxID string
		sessionID string
	}
	pairs := make([]pair, 0, len(sandboxIDs))

	for i, cmd := range sessionIDCmds {
		sessionID, err := cmd.Result()
		if errors.Is(err, redisv9.Nil) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("loadSandboxesByIDs: get sessionID for sandbox %s: %w", sandboxIDs[i], err)
		}
		pairs = append(pairs, pair{
			sandboxID: sandboxIDs[i],
			sessionID: sessionID,
		})
	}

	if len(pairs) == 0 {
		return nil, nil
	}

	sandboxCmds := make([]*redisv9.StringCmd, len(pairs))
	pipe = c.rdb.Pipeline()
	for i, p := range pairs {
		sessionKey := c.sessionKey(p.sessionID)
		sandboxCmds[i] = pipe.Get(ctx, sessionKey)
	}
	_, _ = pipe.Exec(ctx)

	result := make([]*types.SandboxRedis, 0, len(pairs))
	for i, cmd := range sandboxCmds {
		data, err := cmd.Bytes()
		if errors.Is(err, redisv9.Nil) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("loadSandboxesByIDs: get sandbox JSON for session %s: %w", pairs[i].sessionID, err)
		}
		var sandboxRedis types.SandboxRedis
		if err := json.Unmarshal(data, &sandboxRedis); err != nil {
			return nil, fmt.Errorf("loadSandboxesByIDs: unmarshal sandbox for session %s: %w", pairs[i].sessionID, err)
		}
		result = append(result, &sandboxRedis)
	}

	return result, nil
}

// UpdateSandboxLastActivity updates the last-activity index for the given sandbox.
// Last activity is only stored in the sorted set, not in the session value.
func (c *client) UpdateSandboxLastActivity(ctx context.Context, sandboxID string, at time.Time) error {
	if sandboxID == "" {
		return errors.New("UpdateSandboxLastActivity: sandboxID is empty")
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}

	// Ensure the sandbox mapping exists; otherwise treat as not found.
	sandboxKey := c.sandboxKey(sandboxID)
	_, err := c.rdb.Get(ctx, sandboxKey).Result()
	if errors.Is(err, redisv9.Nil) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("UpdateSandboxLastActivity: get mapping for sandbox %s: %w", sandboxID, err)
	}

	if _, err := c.rdb.ZAdd(ctx, c.lastActivityIndexKey, redisv9.Z{
		Score:  float64(at.Unix()),
		Member: sandboxID,
	}).Result(); err != nil {
		return fmt.Errorf("UpdateSandboxLastActivity: ZAdd: %w", err)
	}

	return nil
}
