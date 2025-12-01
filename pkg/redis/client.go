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
	BindSessionWithSandbox(ctx context.Context, sessionID string, sb *types.SandboxRedis, ttl time.Duration) error
	// DeleteSessionBySandboxIDTx removes the bidirectional mapping by sandbox ID.
	DeleteSessionBySandboxIDTx(ctx context.Context, sandboxID string) error

	// ListExpiredSandboxes returns up to limit sandboxes with ExpiresAt before the given time.
	ListExpiredSandboxes(ctx context.Context, before time.Time, limit int64) ([]*types.SandboxRedis, error)
	// ListInactiveSandboxes returns up to limit sandboxes with LastActivityAt before the given time.
	ListInactiveSandboxes(ctx context.Context, before time.Time, limit int64) ([]*types.SandboxRedis, error)
	// UpdateSandboxLastActivity updates LastActivityAt for the given sandbox and refreshes the index.
	UpdateSandboxLastActivity(ctx context.Context, sandboxID string, at time.Time) error
}

// client is the concrete implementation of Client backed by go-redis.
type client struct {
	rdb *redisv9.Client

	sessionPrefix string
	sandboxPrefix string
	lockPrefix    string

	// Sorted-set indexes:
	//   expiryIndexKey:        score = ExpiresAt.Unix(),       member = sandboxID
	//   lastActivityIndexKey:  score = LastActivityAt.Unix(),  member = sandboxID
	expiryIndexKey       string
	lastActivityIndexKey string
}

// Option configures client behavior (e.g. key prefixes).
type Option func(*client)

// WithKeyPrefixes sets custom key prefixes for session / sandbox / lock.
func WithKeyPrefixes(session, sandbox, lock string) Option {
	return func(c *client) {
		c.sessionPrefix = session
		c.sandboxPrefix = sandbox
		c.lockPrefix = lock

		c.expiryIndexKey = sandbox + "expiry"
		c.lastActivityIndexKey = sandbox + "last_activity"
	}
}

// NewClient creates a new Client and initializes the underlying go-redis client.
func NewClient(redisOpts *redisv9.Options, opts ...Option) Client {
	rdb := redisv9.NewClient(redisOpts)

	c := &client{
		rdb:           rdb,
		sessionPrefix: "session:",
		sandboxPrefix: "sandbox:",
		lockPrefix:    "session_lock:",

		expiryIndexKey:       "sandbox:expiry",
		lastActivityIndexKey: "sandbox:last_activity",
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
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

	var sb types.SandboxRedis
	if err := json.Unmarshal(b, &sb); err != nil {
		return nil, fmt.Errorf("GetSandboxBySessionID: unmarshal sandbox: %w", err)
	}
	return &sb, nil
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
// and updates the expiry and last-activity indexes.
//
//	SETEX session: {sessionID} sandboxJSON
//	SETEX sandbox: {sandboxID} sessionID
//	ZADD  sandbox: expiry        (ExpiresAt,      sandboxID)
//	ZADD  sandbox: last_activity (LastActivityAt, sandboxID)
//	DEL   session_lock: {sessionID}
func (c *client) BindSessionWithSandbox(ctx context.Context, sessionID string, sb *types.SandboxRedis, ttl time.Duration) error {
	if sb == nil {
		return errors.New("BindSessionWithSandbox: sandbox is nil")
	}
	if sb.SandboxID == "" {
		return errors.New("BindSessionWithSandbox: sandbox.SandboxID is empty")
	}

	sessionKey := c.sessionKey(sessionID)
	sandboxKey := c.sandboxKey(sb.SandboxID)
	lockKey := c.lockKey(sessionID)

	b, err := json.Marshal(sb)
	if err != nil {
		return fmt.Errorf("BindSessionWithSandbox: marshal sandbox: %w", err)
	}

	pipe := c.rdb.TxPipeline()
	pipe.Set(ctx, sessionKey, b, ttl)
	pipe.Set(ctx, sandboxKey, sessionID, ttl)
	pipe.Del(ctx, lockKey)

	if !sb.ExpiresAt.IsZero() {
		pipe.ZAdd(ctx, c.expiryIndexKey, redisv9.Z{
			Score:  float64(sb.ExpiresAt.Unix()),
			Member: sb.SandboxID,
		})
	}

	if !sb.LastActivityAt.IsZero() {
		pipe.ZAdd(ctx, c.lastActivityIndexKey, redisv9.Z{
			Score:  float64(sb.LastActivityAt.Unix()),
			Member: sb.SandboxID,
		})
	}

	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("BindSessionWithSandbox: redis TxPipeline EXEC: %w", err)
	}
	return nil
}

// DeleteSessionBySandboxIDTx deletes the bidirectional mapping by sandboxID and
// removes the related index entries, using WATCH + TxPipelined for atomicity.
func (c *client) DeleteSessionBySandboxIDTx(ctx context.Context, sandboxID string) error {
	sandboxKey := c.sandboxKey(sandboxID)

	for {
		err := c.rdb.Watch(ctx, func(tx *redisv9.Tx) error {
			sessionID, err := tx.Get(ctx, sandboxKey).Result()
			if errors.Is(err, redisv9.Nil) {
				return ErrNotFound
			}
			if err != nil {
				return fmt.Errorf("redis GET %s: %w", sandboxKey, err)
			}

			sessionKey := c.sessionKey(sessionID)

			_, err = tx.TxPipelined(ctx, func(pipe redisv9.Pipeliner) error {
				pipe.Del(ctx, sandboxKey)
				pipe.Del(ctx, sessionKey)
				pipe.ZRem(ctx, c.expiryIndexKey, sandboxID)
				pipe.ZRem(ctx, c.lastActivityIndexKey, sandboxID)
				return nil
			})
			if err != nil {
				return fmt.Errorf("redis TxPipelined: %w", err)
			}
			return nil
		}, sandboxKey)

		if errors.Is(err, redisv9.TxFailedErr) {
			// Retry on concurrent modification.
			continue
		}
		if errors.Is(err, ErrNotFound) {
			// Treat not-found as success.
			return nil
		}
		if err != nil {
			return fmt.Errorf("DeleteSessionBySandboxIDTx: %w", err)
		}
		return nil
	}
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

// ListInactiveSandboxes returns up to limit sandboxes whose LastActivityAt
// is before before, using the last-activity sorted-set index.
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
		var sb types.SandboxRedis
		if err := json.Unmarshal(data, &sb); err != nil {
			return nil, fmt.Errorf("loadSandboxesByIDs: unmarshal sandbox for session %s: %w", pairs[i].sessionID, err)
		}
		result = append(result, &sb)
	}

	return result, nil
}

// UpdateSandboxLastActivity updates LastActivityAt for the given sandbox and
// synchronizes the last-activity index. TTL on the session key is preserved.
func (c *client) UpdateSandboxLastActivity(ctx context.Context, sandboxID string, at time.Time) error {
	if sandboxID == "" {
		return errors.New("UpdateSandboxLastActivity: sandboxID is empty")
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}

	sandboxKey := c.sandboxKey(sandboxID)
	sessionID, err := c.rdb.Get(ctx, sandboxKey).Result()
	if errors.Is(err, redisv9.Nil) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("UpdateSandboxLastActivity: get sessionID for sandbox %s: %w", sandboxID, err)
	}

	sessionKey := c.sessionKey(sessionID)

	ttl, err := c.rdb.TTL(ctx, sessionKey).Result()
	if err != nil {
		return fmt.Errorf("UpdateSandboxLastActivity: TTL for session %s: %w", sessionKey, err)
	}

	data, err := c.rdb.Get(ctx, sessionKey).Bytes()
	if errors.Is(err, redisv9.Nil) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("UpdateSandboxLastActivity: get sandbox JSON for session %s: %w", sessionKey, err)
	}

	var sb types.SandboxRedis
	if err := json.Unmarshal(data, &sb); err != nil {
		return fmt.Errorf("UpdateSandboxLastActivity: unmarshal sandbox for session %s: %w", sessionKey, err)
	}

	sb.LastActivityAt = at

	newData, err := json.Marshal(&sb)
	if err != nil {
		return fmt.Errorf("UpdateSandboxLastActivity: marshal sandbox for session %s: %w", sessionKey, err)
	}

	pipe := c.rdb.TxPipeline()

	exp := ttl
	if exp <= 0 {
		exp = 0
	}
	pipe.Set(ctx, sessionKey, newData, exp)

	pipe.ZAdd(ctx, c.lastActivityIndexKey, redisv9.Z{
		Score:  float64(at.Unix()),
		Member: sandboxID,
	})

	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("UpdateSandboxLastActivity: TxPipeline EXEC: %w", err)
	}

	return nil
}
