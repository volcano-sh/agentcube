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
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	redisv9 "github.com/redis/go-redis/v9"

	"github.com/volcano-sh/agentcube/pkg/common/types"
)

type redisStore struct {
	cli                *redisv9.Client
	sessionPrefix      string
	expiryIndexKey     string
	idleExpiryIndexKey string
}

// initRedisStore init redis store client
func initRedisStore() (*redisStore, error) {
	redisOptions, err := makeRedisOptions()
	if err != nil {
		return nil, fmt.Errorf("make redis options failed: %w", err)
	}

	return &redisStore{
		cli:                redisv9.NewClient(redisOptions),
		sessionPrefix:      "session:",
		expiryIndexKey:     "session:expiry",
		idleExpiryIndexKey: "session:idle_expiry",
	}, nil
}

// makeRedisOptions creates redis options from environment variables
func makeRedisOptions() (*redisv9.Options, error) {
	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		return nil, fmt.Errorf("missing env var REDIS_ADDR")
	}

	redisPassword := os.Getenv("REDIS_PASSWORD")
	// Secure-by-default: require non-empty password unless explicitly disabled via REDIS_PASSWORD_REQUIRED=false.
	if strings.ToLower(os.Getenv("REDIS_PASSWORD_REQUIRED")) != "false" && redisPassword == "" {
		return nil, fmt.Errorf("REDIS_PASSWORD is required but not set")
	}

	return &redisv9.Options{
		Addr:     redisAddr,
		Password: redisPassword,
	}, nil
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
	if pipeErr != nil && !errors.Is(pipeErr, redisv9.Nil) {
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
	if sandboxRedis.CreatedAt.IsZero() {
		return errors.New("StoreSandbox: sandbox created at is zero")
	}

	pipe := rs.cli.Pipeline()
	pipe.SetNX(ctx, sessionKey, b, 0)
	pipe.ZAdd(ctx, rs.expiryIndexKey, redisv9.Z{
		Score:  float64(sandboxRedis.ExpiresAt.Unix()),
		Member: sandboxRedis.SessionID,
	})
	// Use a temporary grace deadline here so GC can still clean up orphaned
	// records if creation crashes before readiness; once ready, it is
	// overwritten with readyAt + IdleTimeout via UpdateSessionLastActivity(force=true).
	pipe.ZAdd(ctx, rs.idleExpiryIndexKey, redisv9.Z{
		Score:  float64(sandboxRedis.CreatedAt.Add(sandboxStartupGracePeriod).Unix()),
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
// update sandbox object only, do not update expiry and idleExpiry ZSet
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

	if !ok {
		return fmt.Errorf("UpdateSandbox: redis SETXX %s, key does not exist", sessionKey)
	}
	return nil
}

func (rs *redisStore) DeleteSandboxBySessionID(ctx context.Context, sessionID string) error {
	sessionKey := rs.sessionKey(sessionID)

	pipe := rs.cli.Pipeline()
	pipe.Del(ctx, sessionKey)
	pipe.ZRem(ctx, rs.expiryIndexKey, sessionID)
	pipe.ZRem(ctx, rs.idleExpiryIndexKey, sessionID)

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

// ListInactiveSandboxes returns up to limit sandboxes whose idle deadline is at
// or before the given time, ordered by deadline.
func (rs *redisStore) ListInactiveSandboxes(ctx context.Context, before time.Time, limit int64) ([]*types.SandboxInfo, error) {
	if limit <= 0 {
		return nil, nil
	}

	maxScore := before.Unix()
	ids, err := rs.cli.ZRangeByScore(ctx, rs.idleExpiryIndexKey, &redisv9.ZRangeBy{
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

// Close releases all resources held by the redis store.
func (rs *redisStore) Close() error {
	return rs.cli.Close()
}

var updateSessionIdleExpiryScript = redisv9.NewScript(updateSessionIdleExpiryLua)

// UpdateSessionLastActivity updates the session's idle expiry deadline (at + IdleTimeout).
// When force is false, the deadline only advances forward; when true, it overwrites unconditionally.
func (rs *redisStore) UpdateSessionLastActivity(ctx context.Context, sessionID string, at time.Time, force bool) error {
	if err := validateSessionActivity(sessionID, at); err != nil {
		return fmt.Errorf("UpdateSessionLastActivity: %w", err)
	}
	sandbox, err := rs.GetSandboxBySessionID(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("UpdateSessionLastActivity: session %s: %w", sessionID, err)
	}
	result, err := updateSessionIdleExpiryScript.Run(ctx, rs.cli,
		[]string{rs.sessionKey(sessionID), rs.idleExpiryIndexKey},
		sessionID, sandbox.IdleExpiryTime(at).Unix(), forceFlag(force)).Int64()
	if err != nil {
		return fmt.Errorf("UpdateSessionLastActivity: update deadline for session %s: %w", sessionID, err)
	}
	if result == 0 {
		return ErrNotFound
	}
	return nil
}
