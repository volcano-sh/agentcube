package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	redisv9 "github.com/redis/go-redis/v9"

	"github.com/volcano-sh/agentcube/pkg/common/types"
)

type redisStore struct {
	cli                  *redisv9.Client
	sessionPrefix        string
	expiryIndexKey       string
	lastActivityIndexKey string
}

// initRedisStore init redis store client
func initRedisStore() (*redisStore, error) {
	redisOptions, err := makeRedisOptions()
	if err != nil {
		return nil, fmt.Errorf("make redis options failed: %w", err)
	}

	return &redisStore{
		cli:                  redisv9.NewClient(redisOptions),
		sessionPrefix:        "session:",
		expiryIndexKey:       "session:expiry",
		lastActivityIndexKey: "session:last_activity",
	}, nil
}

// makeRedisOptions creates redis options from environment variables
func makeRedisOptions() (*redisv9.Options, error) {
	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		return nil, fmt.Errorf("missing env var REDIS_ADDR")
	}

	redisPassword := os.Getenv("REDIS_PASSWORD")
	if redisPassword == "" {
		return nil, fmt.Errorf("missing env var REDIS_PASSWORD")
	}

	redisOptions := &redisv9.Options{
		Addr:     redisAddr,
		Password: redisPassword,
	}
	return redisOptions, nil
}

// sessionKey make sessionKey by sessionID
func (rs *redisStore) sessionKey(sessionID string) string {
	return rs.sessionPrefix + sessionID
}

// loadSandboxesBySessionIDs loads sandbox objects for the given session IDs.
func (rs *redisStore) loadSandboxesBySessionIDs(ctx context.Context, sessionIDs []string) ([]*types.SandboxInfo, error) {
	if len(sessionIDs) == 0 {
		return nil, nil
	}

	sandboxCommands := make([]*redisv9.StringCmd, len(sessionIDs))
	pipe := rs.cli.Pipeline()
	for i, sessionID := range sessionIDs {
		sessionKey := rs.sessionKey(sessionID)
		sandboxCommands[i] = pipe.Get(ctx, sessionKey)
	}
	_, pipeErr := pipe.Exec(ctx)
	if pipeErr != nil {
		return nil, fmt.Errorf("redis pipeline exec failed: %w", pipeErr)
	}

	result := make([]*types.SandboxInfo, 0, len(sessionIDs))
	for i, cmd := range sandboxCommands {
		data, err := cmd.Bytes()
		if errors.Is(err, redisv9.Nil) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("loadSandboxesBySessionIDs: get sandbox JSON for session %s: %w", sessionIDs[i], err)
		}
		var sandboxRedis types.SandboxInfo
		if err := json.Unmarshal(data, &sandboxRedis); err != nil {
			return nil, fmt.Errorf("loadSandboxesBySessionIDs: unmarshal sandbox for session %s: %w", sessionIDs[i], err)
		}
		result = append(result, &sandboxRedis)
	}

	return result, nil
}

func (rs *redisStore) Ping(ctx context.Context) error {
	resp, err := rs.cli.Ping(ctx).Result()
	if err != nil {
		return fmt.Errorf("ping error: %w", err)
	}
	if resp != "PONG" {
		return fmt.Errorf("unexpected ping response: %s", resp)
	}
	return nil
}

// GetSandboxBySessionID looks up the sandbox bound to the given session ID.
// Underlying Redis: GET session:{sessionID} -> Sandbox Info(JSON).
func (rs *redisStore) GetSandboxBySessionID(ctx context.Context, sessionID string) (*types.SandboxInfo, error) {
	key := rs.sessionKey(sessionID)

	b, err := rs.cli.Get(ctx, key).Bytes()
	if errors.Is(err, redisv9.Nil) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("GetSandboxBySessionID: redis GET %s failed: %w", key, err)
	}

	var sandboxRedis types.SandboxInfo
	if err := json.Unmarshal(b, &sandboxRedis); err != nil {
		return nil, fmt.Errorf("GetSandboxBySessionID: unmarshal sandbox failed: %w", err)
	}
	return &sandboxRedis, nil
}

func (rs *redisStore) StoreSandbox(ctx context.Context, sandboxRedis *types.SandboxInfo) error {
	if sandboxRedis == nil {
		return errors.New("StoreSandbox: sandbox is nil")
	}

	sessionKey := rs.sessionKey(sandboxRedis.SessionID)

	b, err := json.Marshal(sandboxRedis)
	if err != nil {
		return fmt.Errorf("StoreSandbox: marshal sandbox failed: %w", err)
	}

	if sandboxRedis.ExpiresAt.IsZero() {
		return fmt.Errorf("StoreSandbox: sandbox expired at is zero")
	}

	pipe := rs.cli.Pipeline()
	pipe.SetNX(ctx, sessionKey, b, 0)
	pipe.ZAdd(ctx, rs.expiryIndexKey, redisv9.Z{
		Score:  float64(sandboxRedis.ExpiresAt.Unix()),
		Member: sandboxRedis.SessionID,
	})
	pipe.ZAdd(ctx, rs.lastActivityIndexKey, redisv9.Z{
		Score:  float64(time.Now().Unix()),
		Member: sandboxRedis.SessionID,
	})

	cmder, err := pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("StoreSandbox: redis Pipeline EXEC: %w", err)
	}

	if len(cmder) == 0 {
		return errors.New("StoreSandbox: unexpected empty cmder")
	}

	for i, cmd := range cmder {
		if err = cmd.Err(); err != nil {
			return fmt.Errorf("StoreSandbox: EXEC pipeline failed: %w, cmder index: %v", err, i)
		}
	}

	return nil
}

// UpdateSandbox update sandbox obj in redis
// update sandbox object only, do not update expiry and lastActivity ZSet
func (rs *redisStore) UpdateSandbox(ctx context.Context, sandboxRedis *types.SandboxInfo) error {
	if sandboxRedis == nil {
		return errors.New("UpdateSandbox: sandbox is nil")
	}

	sessionKey := rs.sessionKey(sandboxRedis.SessionID)

	b, err := json.Marshal(sandboxRedis)
	if err != nil {
		return fmt.Errorf("UpdateSandbox: marshal sandbox: %w", err)
	}

	ok, err := rs.cli.SetXX(ctx, sessionKey, b, 0).Result()
	if err != nil {
		return fmt.Errorf("UpdateSandbox: redis SETXX %s: %w", sessionKey, err)
	}

	if ok == false {
		return fmt.Errorf("UpdateSandbox: redis SETXX %s, key not exists", sessionKey)
	}
	return nil
}

func (rs *redisStore) DeleteSandboxBySessionID(ctx context.Context, sessionID string) error {
	sessionKey := rs.sessionKey(sessionID)

	pipe := rs.cli.Pipeline()
	pipe.Del(ctx, sessionKey)
	pipe.ZRem(ctx, rs.expiryIndexKey, sessionID)
	pipe.ZRem(ctx, rs.lastActivityIndexKey, sessionID)

	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("DeleteSandboxBySessionID: pipeline EXEC: %w", err)
	}
	return nil
}

// ListExpiredSandboxes returns up to limit sandboxes whose ExpiresAt is before.
// It uses a sorted-set index and is linear in the number of results.
func (rs *redisStore) ListExpiredSandboxes(ctx context.Context, before time.Time, limit int64) ([]*types.SandboxInfo, error) {
	if limit <= 0 {
		return nil, nil
	}

	maxScore := before.Unix()
	ids, err := rs.cli.ZRangeByScore(ctx, rs.expiryIndexKey, &redisv9.ZRangeBy{
		Min:    "-inf",
		Max:    fmt.Sprintf("%d", maxScore),
		Offset: 0,
		Count:  limit,
	}).Result()
	if err != nil {
		return nil, fmt.Errorf("ListExpiredSandboxes: ZRangeByScore failed: %w", err)
	}

	return rs.loadSandboxesBySessionIDs(ctx, ids)
}

// ListInactiveSandboxes returns up to limit sandboxes whose last activity
// time is before, using the last-activity sorted-set index.
func (rs *redisStore) ListInactiveSandboxes(ctx context.Context, before time.Time, limit int64) ([]*types.SandboxInfo, error) {
	if limit <= 0 {
		return nil, nil
	}

	maxScore := before.Unix()
	ids, err := rs.cli.ZRangeByScore(ctx, rs.lastActivityIndexKey, &redisv9.ZRangeBy{
		Min:    "-inf",
		Max:    fmt.Sprintf("%d", maxScore),
		Offset: 0,
		Count:  limit,
	}).Result()
	if err != nil {
		return nil, fmt.Errorf("ListInactiveSandboxes: ZRangeByScore failed: %w", err)
	}

	return rs.loadSandboxesBySessionIDs(ctx, ids)
}

// UpdateSessionLastActivity updates the last-activity index for the given session.
func (rs *redisStore) UpdateSessionLastActivity(ctx context.Context, sessionID string, at time.Time) error {
	if sessionID == "" {
		return errors.New("UpdateSessionLastActivity: sessionID is empty")
	}
	if at.IsZero() {
		at = time.Now()
	}

	// Ensure the sandbox mapping exists; otherwise treat as not found.
	sessionKey := rs.sessionKey(sessionID)
	_, err := rs.cli.Get(ctx, sessionKey).Result()
	if errors.Is(err, redisv9.Nil) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("UpdateSessionLastActivity: get mapping for sessionID %s: %w", sessionID, err)
	}

	if _, err := rs.cli.ZAdd(ctx, rs.lastActivityIndexKey, redisv9.Z{
		Score:  float64(at.Unix()),
		Member: sessionID,
	}).Result(); err != nil {
		return fmt.Errorf("UpdateSessionLastActivity: ZAdd: %w", err)
	}

	return nil
}
