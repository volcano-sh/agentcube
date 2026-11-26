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

	if sandboxRedis.ExpiresAt.IsZero() {
		return fmt.Errorf("StoreSandbox: sandbox expired at is zero")
	}

	sessionKey := rs.sessionKey(sandboxRedis.SessionID)
	normalized := *sandboxRedis
	normalized.ExpiresAt = normalized.ExpiresAt.UTC()

	b, err := json.Marshal(&normalized)
	if err != nil {
		return fmt.Errorf("StoreSandbox: marshal sandbox failed: %w", err)
	}

	pipe := rs.cli.Pipeline()
	pipe.SetNX(ctx, sessionKey, b, 0)
	pipe.ZAdd(ctx, rs.expiryIndexKey, redisv9.Z{
		Score:  float64(sandboxRedis.ExpiresAt.Unix()),
		Member: sandboxRedis.SessionID,
	})
	if !sandboxRedis.DisableIdleTimeout {
		pipe.ZAdd(ctx, rs.lastActivityIndexKey, redisv9.Z{
			Score:  float64(time.Now().Unix()),
			Member: sandboxRedis.SessionID,
		})
	}
	if sandboxRedis.OwnerID != "" {
		pipe.SAdd(ctx, ownerSessionsKey(sandboxRedis.OwnerID), sandboxRedis.SessionID)
	}

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
	normalized := *sandboxRedis
	if !normalized.ExpiresAt.IsZero() {
		normalized.ExpiresAt = normalized.ExpiresAt.UTC()
	}

	b, err := json.Marshal(&normalized)
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

	var ownerID string
	if existing, err := rs.GetSandboxBySessionID(ctx, sessionID); err == nil && existing != nil {
		ownerID = existing.OwnerID
	}

	pipe := rs.cli.Pipeline()
	pipe.Del(ctx, sessionKey)
	pipe.ZRem(ctx, rs.expiryIndexKey, sessionID)
	pipe.ZRem(ctx, rs.lastActivityIndexKey, sessionID)
	if ownerID != "" {
		pipe.SRem(ctx, ownerSessionsKey(ownerID), sessionID)
	}

	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("DeleteSandboxBySessionID: pipeline EXEC: %w", err)
	}
	return nil
}

var (
	updateSandboxExpiryScript = redisv9.NewScript(updateSandboxExpiryLua)
	claimExpiredSandboxScript = redisv9.NewScript(claimExpiredSandboxLua)
)

// ClaimExpiredSandbox atomically marks an expired session as deleting if its expiry is <= now
// and refreshes its expiry-index lease so failed cleanups are retried automatically.
func (rs *redisStore) ClaimExpiredSandbox(ctx context.Context, sessionID string, now time.Time) (bool, error) {
	if sessionID == "" {
		return false, errors.New("ClaimExpiredSandbox: sessionID is empty")
	}
	nowUTC := now.UTC()
	leaseUntil := nowUTC.Add(expiredClaimLease).Unix()
	res, err := claimExpiredSandboxScript.Run(
		ctx,
		rs.cli,
		[]string{rs.sessionKey(sessionID)},
		nowUTC.Format("20060102150405"),
		nowUTC.Unix(),
		leaseUntil,
	).Int64()
	if err != nil {
		return false, fmt.Errorf("ClaimExpiredSandbox: script execution failed for sessionID %s: %w", sessionID, err)
	}
	if res == -1 {
		_ = rs.cli.ZRem(ctx, rs.expiryIndexKey, sessionID).Err()
		return false, nil
	}
	if res != 1 {
		return false, nil
	}
	if err := rs.cli.ZAdd(ctx, rs.expiryIndexKey, redisv9.Z{
		Score:  float64(leaseUntil),
		Member: sessionID,
	}).Err(); err != nil {
		return false, fmt.Errorf("ClaimExpiredSandbox: ZADD lease failed for sessionID %s: %w", sessionID, err)
	}
	return true, nil
}

// UpdateSandboxExpiry atomically updates ExpiresAt on the stored sandbox and its expiry index.
func (rs *redisStore) UpdateSandboxExpiry(ctx context.Context, sessionID string, expiresAt time.Time, extendOnly bool) (*types.SandboxInfo, error) {
	if sessionID == "" {
		return nil, errors.New("UpdateSandboxExpiry: sessionID is empty")
	}
	if expiresAt.IsZero() {
		return nil, errors.New("UpdateSandboxExpiry: expiresAt is zero")
	}

	extendFlag := "0"
	if extendOnly {
		extendFlag = "1"
	}
	expiresUTC := expiresAt.UTC()
	res, err := updateSandboxExpiryScript.Run(
		ctx,
		rs.cli,
		[]string{rs.sessionKey(sessionID)},
		expiresUTC.Format(time.RFC3339Nano),
		expiresUTC.Format("20060102150405"),
		extendFlag,
	).Result()
	if err != nil {
		return nil, fmt.Errorf("UpdateSandboxExpiry: script execution failed for sessionID %s: %w", sessionID, err)
	}

	arr, ok := res.([]interface{})
	if !ok || len(arr) != 3 {
		return nil, fmt.Errorf("UpdateSandboxExpiry: unexpected script response %T", res)
	}
	found, _ := arr[0].(int64)
	if found == 0 {
		return nil, ErrNotFound
	}
	updatedJSON, _ := arr[1].(string)
	changed, _ := arr[2].(int64)
	var sandbox types.SandboxInfo
	if err := json.Unmarshal([]byte(updatedJSON), &sandbox); err != nil {
		return nil, fmt.Errorf("UpdateSandboxExpiry: unmarshal sandbox failed: %w", err)
	}
	if changed == 1 {
		if err := rs.cli.ZAdd(ctx, rs.expiryIndexKey, redisv9.Z{
			Score:  float64(expiresUTC.Unix()),
			Member: sessionID,
		}).Err(); err != nil {
			return nil, fmt.Errorf("UpdateSandboxExpiry: ZADD failed for sessionID %s: %w", sessionID, err)
		}
	}
	return &sandbox, nil
}

// ListActiveSandboxes returns non-expired sandboxes, optionally filtered by ownerID and namespace.
func (rs *redisStore) ListActiveSandboxes(ctx context.Context, ownerID string, namespace string) ([]*types.SandboxInfo, error) {
	now := time.Now()
	var ids []string
	var err error
	if ownerID != "" {
		ids, err = rs.cli.SMembers(ctx, ownerSessionsKey(ownerID)).Result()
		if err != nil {
			return nil, fmt.Errorf("ListActiveSandboxes: SMembers failed: %w", err)
		}
	} else {
		minScore := fmt.Sprintf("%d", now.Unix())
		ids, err = rs.cli.ZRangeByScore(ctx, rs.expiryIndexKey, &redisv9.ZRangeBy{
			Min: minScore,
			Max: "+inf",
		}).Result()
		if err != nil {
			return nil, fmt.Errorf("ListActiveSandboxes: ZRangeByScore failed: %w", err)
		}
	}

	sandboxes, err := rs.loadSandboxesBySessionIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	return filterAndSortActiveSandboxes(sandboxes, namespace, now), nil
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
// LastActivityAt is populated on each returned SandboxInfo from the sorted-set
// score so the caller can apply per-sandbox idle-timeout logic.
func (rs *redisStore) ListInactiveSandboxes(ctx context.Context, before time.Time, limit int64) ([]*types.SandboxInfo, error) {
	if limit <= 0 {
		return nil, nil
	}

	maxScore := before.Unix()
	zs, err := rs.cli.ZRangeByScoreWithScores(ctx, rs.lastActivityIndexKey, &redisv9.ZRangeBy{
		Min:    "-inf",
		Max:    fmt.Sprintf("%d", maxScore),
		Offset: 0,
		Count:  limit,
	}).Result()
	if err != nil {
		return nil, fmt.Errorf("ListInactiveSandboxes: ZRangeByScoreWithScores failed: %w", err)
	}

	ids := make([]string, len(zs))
	scores := make(map[string]time.Time, len(zs))
	for i, z := range zs {
		id, ok := z.Member.(string)
		if !ok {
			return nil, fmt.Errorf("ListInactiveSandboxes: unexpected member type %T", z.Member)
		}
		ids[i] = id
		scores[id] = time.Unix(int64(z.Score), 0)
	}

	sandboxes, err := rs.loadSandboxesBySessionIDs(ctx, ids)
	if err != nil {
		return nil, err
	}

	for _, s := range sandboxes {
		if t, ok := scores[s.SessionID]; ok {
			s.LastActivityAt = t
		}
	}
	return sandboxes, nil
}

// Close releases all resources held by the redis store.
func (rs *redisStore) Close() error {
	return rs.cli.Close()
}

// updateSessionLastActivityScript atomically checks if the session key exists
// and updates its last activity time in the sorted-set index.
// It uses EXISTS to verify presence rather than GET (which retrieved the
// actual value in older implementations). This saves data transfer overhead,
// if in future the value is required GET can be used instead.
var updateSessionLastActivityScript = redisv9.NewScript(`
if redis.call("EXISTS", KEYS[1]) == 1 then
	redis.call("ZADD", KEYS[2], ARGV[2], ARGV[1])
	return 1
else
	return 0
end
`)

// UpdateSessionLastActivity updates the last-activity index for the given session.
func (rs *redisStore) UpdateSessionLastActivity(ctx context.Context, sessionID string, at time.Time) error {
	if sessionID == "" {
		return errors.New("UpdateSessionLastActivity: sessionID is empty")
	}
	if at.IsZero() {
		at = time.Now()
	}

	sessionKey := rs.sessionKey(sessionID)
	res, err := updateSessionLastActivityScript.Run(ctx, rs.cli, []string{sessionKey, rs.lastActivityIndexKey}, sessionID, at.Unix()).Int64()
	if err != nil {
		return fmt.Errorf("UpdateSessionLastActivity: script execution failed for sessionID %s: %w", sessionID, err)
	}

	if res == 0 {
		return ErrNotFound
	}

	return nil
}
