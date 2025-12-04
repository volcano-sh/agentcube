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
	// UpdateSandbox update sandbox
	UpdateSandbox(ctx context.Context, sandboxRedis *types.SandboxRedis, ttl time.Duration) error
	// StoreSandbox store sandbox into redis
	StoreSandbox(ctx context.Context, sandboxRedis *types.SandboxRedis, ttl time.Duration) error
	// Ping redis ping
	Ping(ctx context.Context) error
	// DeleteSandboxBySessionIDTx delete sandbox by sessionID
	DeleteSandboxBySessionIDTx(ctx context.Context, sessionID string) error

	// ListExpiredSandboxes returns up to limit sandboxes with ExpiresAt before the given time.
	ListExpiredSandboxes(ctx context.Context, before time.Time, limit int64) ([]*types.SandboxRedis, error)
	// ListInactiveSandboxes returns up to limit sandboxes with last-activity time before the given time.
	ListInactiveSandboxes(ctx context.Context, before time.Time, limit int64) ([]*types.SandboxRedis, error)
	// UpdateSessionLastActivity updates the last-activity index for the given session.
	UpdateSessionLastActivity(ctx context.Context, sessionID string, at time.Time) error
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

func (c *client) UpdateSandbox(ctx context.Context, sandboxRedis *types.SandboxRedis, ttl time.Duration) error {
	if sandboxRedis == nil {
		return errors.New("UpdateSandbox: sandbox is nil")
	}

	sessionKey := c.sessionKey(sandboxRedis.SessionID)

	b, err := json.Marshal(sandboxRedis)
	if err != nil {
		return fmt.Errorf("UpdateSandbox: marshal sandbox: %w", err)
	}

	ok, err := c.rdb.SetXX(ctx, sessionKey, b, ttl).Result()
	if err != nil {
		return fmt.Errorf("UpdateSandbox: redis SETXX %s: %w", sessionKey, err)
	}

	if ok == false {
		return fmt.Errorf("UpdateSandbox: redis SETXX %s, key not exists", sessionKey)
	}
	return nil
}

func (c *client) StoreSandbox(ctx context.Context, sandboxRedis *types.SandboxRedis, ttl time.Duration) error {
	if sandboxRedis == nil {
		return errors.New("StoreSandbox: sandbox is nil")
	}

	sessionKey := c.sessionKey(sandboxRedis.SessionID)

	b, err := json.Marshal(sandboxRedis)
	if err != nil {
		return fmt.Errorf("StoreSandbox: marshal sandbox: %w", err)
	}

	pipe := c.rdb.TxPipeline()
	pipe.SetNX(ctx, sessionKey, b, ttl)

	if !sandboxRedis.ExpiresAt.IsZero() {
		pipe.ZAdd(ctx, c.expiryIndexKey, redisv9.Z{
			Score:  float64(sandboxRedis.ExpiresAt.Unix()),
			Member: sandboxRedis.SessionID,
		})
	}
	pipe.ZAdd(ctx, c.lastActivityIndexKey, redisv9.Z{
		Score:  float64(time.Now().Unix()),
		Member: sandboxRedis.SessionID,
	})

	cmder, err := pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("StoreSandbox: redis TxPipeline EXEC: %w", err)
	}

	if len(cmder) == 0 {
		return errors.New("StoreSandbox: unexpected empty cmder")
	}

	for i, cmd := range cmder {
		if err = cmd.Err(); err != nil {
			return fmt.Errorf("StoreSandbox: SET failed: %w, cmder index: %v", err, i)
		}
	}

	return nil
}

func (c *client) Ping(ctx context.Context) error {
	resp, err := c.rdb.Ping(ctx).Result()
	if err != nil {
		return fmt.Errorf("ping error: %w", err)
	}
	if resp != "PONG" {
		return fmt.Errorf("unexpected ping response: %s", resp)
	}
	return nil
}

func (c *client) DeleteSandboxBySessionIDTx(ctx context.Context, sessionID string) error {
	sessionKey := c.sessionKey(sessionID)

	pipe := c.rdb.TxPipeline()
	pipe.Del(ctx, sessionKey)
	pipe.ZRem(ctx, c.expiryIndexKey, sessionID)
	pipe.ZRem(ctx, c.lastActivityIndexKey, sessionID)

	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("DeleteSandboxBySessionIDTx: pipeline EXEC: %w", err)
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

	return c.loadSandboxesBySessionIDs(ctx, ids)
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

	return c.loadSandboxesBySessionIDs(ctx, ids)
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

// loadSandboxesBySessionIDs loads sandbox objects for the given session IDs.
func (c *client) loadSandboxesBySessionIDs(ctx context.Context, sessionIDs []string) ([]*types.SandboxRedis, error) {
	if len(sessionIDs) == 0 {
		return nil, nil
	}

	sandboxCmds := make([]*redisv9.StringCmd, len(sessionIDs))
	pipe := c.rdb.Pipeline()
	for i, sessionID := range sessionIDs {
		sessionKey := c.sessionKey(sessionID)
		sandboxCmds[i] = pipe.Get(ctx, sessionKey)
	}
	_, _ = pipe.Exec(ctx)

	result := make([]*types.SandboxRedis, 0, len(sessionIDs))
	for i, cmd := range sandboxCmds {
		data, err := cmd.Bytes()
		if errors.Is(err, redisv9.Nil) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("loadSandboxesBySessionIDs: get sandbox JSON for session %s: %w", sessionIDs[i], err)
		}
		var sandboxRedis types.SandboxRedis
		if err := json.Unmarshal(data, &sandboxRedis); err != nil {
			return nil, fmt.Errorf("loadSandboxesByIDs: unmarshal sandbox for session %s: %w", sessionIDs[i], err)
		}
		result = append(result, &sandboxRedis)
	}

	return result, nil
}

// UpdateSessionLastActivity updates the last-activity index for the given session.
func (c *client) UpdateSessionLastActivity(ctx context.Context, sessionID string, at time.Time) error {
	if sessionID == "" {
		return errors.New("UpdateSessionLastActivity: sessionID is empty")
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}

	// Ensure the sandbox mapping exists; otherwise treat as not found.
	sessionKey := c.sessionKey(sessionID)
	_, err := c.rdb.Get(ctx, sessionKey).Result()
	if errors.Is(err, redisv9.Nil) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("UpdateSessionLastActivity: get mapping for sessionID %s: %w", sessionID, err)
	}

	if _, err := c.rdb.ZAdd(ctx, c.lastActivityIndexKey, redisv9.Z{
		Score:  float64(at.Unix()),
		Member: sessionID,
	}).Result(); err != nil {
		return fmt.Errorf("UpdateSessionLastActivity: ZAdd: %w", err)
	}

	return nil
}
