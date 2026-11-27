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
	"strconv"
	"strings"
	"time"

	"k8s.io/klog/v2"

	"github.com/valkey-io/valkey-go"

	"github.com/volcano-sh/agentcube/pkg/common/types"
)

type valkeyStore struct {
	cli                valkey.Client
	sessionPrefix      string
	expiryIndexKey     string
	idleExpiryIndexKey string
}

// initValkeyStore init valkey store client
func initValkeyStore() (*valkeyStore, error) {
	clientOpts, err := makeValkeyOptions()
	if err != nil {
		return nil, fmt.Errorf("make valkey client options failed: %w", err)
	}

	client, err := valkey.NewClient(*clientOpts)
	if err != nil {
		return nil, fmt.Errorf("create valkey client failed: %w", err)
	}
	return &valkeyStore{
		cli:                client,
		sessionPrefix:      "session:",
		expiryIndexKey:     "session:expiry",
		idleExpiryIndexKey: "session:idle_expiry",
	}, nil
}

// makeValkeyOptions creates valkey ClientOption from environment variables
func makeValkeyOptions() (*valkey.ClientOption, error) {
	valkeyAddr := os.Getenv("VALKEY_ADDR")
	if valkeyAddr == "" {
		return nil, fmt.Errorf("missing env var VALKEY_ADDR")
	}

	valkeyPassword := os.Getenv("VALKEY_PASSWORD")
	// Secure-by-default: require non-empty password unless explicitly disabled via VALKEY_PASSWORD_REQUIRED=false.
	if strings.ToLower(os.Getenv("VALKEY_PASSWORD_REQUIRED")) != "false" && valkeyPassword == "" {
		return nil, fmt.Errorf("VALKEY_PASSWORD is required but not set")
	}

	valkeyClientOptions := &valkey.ClientOption{
		InitAddress: strings.Split(valkeyAddr, ","),
		Password:    valkeyPassword,
	}
	valkeyDisableCache := os.Getenv("VALKEY_DISABLE_CACHE")
	if valkeyDisableCache != "" {
		disableCache, err := strconv.ParseBool(valkeyDisableCache)
		if err == nil && disableCache {
			valkeyClientOptions.DisableCache = true
			klog.Info("valkeyClientOptions DisableCache is set to true")
		}
	}
	valkeyForceSingle := os.Getenv("VALKEY_FORCE_SINGLE")
	if valkeyForceSingle != "" {
		forceSingleCache, err := strconv.ParseBool(valkeyForceSingle)
		if err == nil && forceSingleCache {
			valkeyClientOptions.ForceSingleClient = true
			klog.Info("valkeyClientOptions ForceSingleClient is set to true")
		}
	}
	return valkeyClientOptions, nil
}

// sessionKey make sessionKey by sessionID
func (vs *valkeyStore) sessionKey(sessionID string) string {
	return vs.sessionPrefix + sessionID
}

// loadSandboxesBySessionIDs loads sandbox objects for the given session IDs.
func (vs *valkeyStore) loadSandboxesBySessionIDs(ctx context.Context, sessionIDs []string) ([]*types.SandboxInfo, error) {
	if len(sessionIDs) == 0 {
		return nil, nil
	}

	sessionIDKeys := make([]string, 0, len(sessionIDs))
	for _, sessionID := range sessionIDs {
		sessionIDKeys = append(sessionIDKeys, vs.sessionKey(sessionID))
	}
	// MGet should in same slot
	stringSliceResults, err := vs.cli.Do(ctx, vs.cli.B().Mget().Key(sessionIDKeys...).Build()).AsStrSlice()
	if err != nil {
		return nil, fmt.Errorf("loadSandboxesBySessionIDs: Valkey MGet sandboxes failed: %w", err)
	}

	if len(stringSliceResults) > len(sessionIDKeys) {
		return nil, fmt.Errorf("unexpected MGet result size: %d, param size: %d", len(stringSliceResults), len(sessionIDKeys))
	}

	sandboxResults := make([]*types.SandboxInfo, 0, len(stringSliceResults))
	for i, sandboxObjString := range stringSliceResults {
		if len(sandboxObjString) == 0 {
			// sandboxObjString is empty while sessionKey not exist, ignore
			continue
		}
		var sandboxRedis types.SandboxInfo
		if err = json.Unmarshal([]byte(sandboxObjString), &sandboxRedis); err != nil {
			return nil, fmt.Errorf("unmarshal sandbox failed: %w, index: %v, sessionID: %v", err, i, sessionIDs[i])
		}
		sandboxResults = append(sandboxResults, &sandboxRedis)
	}

	return sandboxResults, nil
}

// Ping check valkey store available or not
func (vs *valkeyStore) Ping(ctx context.Context) error {
	resp, err := vs.cli.Do(ctx, vs.cli.B().Ping().Build()).ToString()
	if err != nil {
		return fmt.Errorf("ping error: %w", err)
	}
	if resp != "PONG" {
		return fmt.Errorf("unexpected ping response: %s", resp)
	}
	return nil
}

// GetSandboxBySessionID get the sandbox by session ID
func (vs *valkeyStore) GetSandboxBySessionID(ctx context.Context, sessionID string) (*types.SandboxInfo, error) {
	key := vs.sessionKey(sessionID)

	b, err := vs.cli.Do(ctx, vs.cli.B().Get().Key(key).Build()).AsBytes()
	if err != nil {
		if valkey.IsValkeyNil(err) {
			// sandbox not found by session id
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("GetSandboxBySessionID: valkey GET %s: %w", key, err)
	}

	var sandboxRedis types.SandboxInfo
	if err := json.Unmarshal(b, &sandboxRedis); err != nil {
		return nil, fmt.Errorf("GetSandboxBySessionID: unmarshal sandbox failed: %w", err)
	}
	return &sandboxRedis, nil
}

// StoreSandbox store sandbox into storage
func (vs *valkeyStore) StoreSandbox(ctx context.Context, sandboxStore *types.SandboxInfo) error {
	if sandboxStore == nil {
		return errors.New("StoreSandbox: sandbox is nil")
	}

	if sandboxStore.ExpiresAt.IsZero() {
		return fmt.Errorf("StoreSandbox: sandbox expires time is zero")
	}
	if sandboxStore.CreatedAt.IsZero() {
		return errors.New("StoreSandbox: sandbox created time is zero")
	}

	sessionKey := vs.sessionKey(sandboxStore.SessionID)
	b, err := json.Marshal(sandboxStore)
	if err != nil {
		return fmt.Errorf("StoreSandbox: marshal sandbox: %w", err)
	}

	commands := make(valkey.Commands, 0, 5)
	commands = append(commands, vs.cli.B().Setnx().Key(sessionKey).Value(string(b)).Build())
	commands = append(commands, vs.cli.B().Zadd().Key(vs.expiryIndexKey).ScoreMember().
		ScoreMember(float64(sandboxStore.ExpiresAt.Unix()), sandboxStore.SessionID).Build())
	// Use a temporary grace deadline here so GC can still clean up orphaned
	// records if creation crashes before readiness; once ready, it is
	// overwritten with readyAt + IdleTimeout via UpdateSessionLastActivity(force=true).
	commands = append(commands, vs.cli.B().Zadd().Key(vs.idleExpiryIndexKey).ScoreMember().
		ScoreMember(float64(sandboxStore.CreatedAt.Add(sandboxStartupGracePeriod).Unix()), sandboxStore.SessionID).Build())

	for i, resp := range vs.cli.DoMulti(ctx, commands...) {
		if err = resp.Error(); err != nil {
			return fmt.Errorf("StoreSandbox: DoMulti failed: %w, command index: %v", err, i)
		}
	}

	return nil
}

// UpdateSandbox update sandbox obj in valkey
// update sandbox object only, do not update expiry and idleExpiry ZSet
func (vs *valkeyStore) UpdateSandbox(ctx context.Context, sandboxStore *types.SandboxInfo) error {
	if sandboxStore == nil {
		return errors.New("UpdateSandbox: sandbox is nil")
	}

	sessionKey := vs.sessionKey(sandboxStore.SessionID)

	b, err := json.Marshal(sandboxStore)
	if err != nil {
		return fmt.Errorf("UpdateSandbox: marshal sandbox failed: %w", err)
	}

	msg, err := vs.cli.Do(ctx, vs.cli.B().Set().Key(sessionKey).Value(string(b)).Xx().Build()).ToString()
	if err != nil && !valkey.IsValkeyNil(err) {
		return fmt.Errorf("UpdateSandbox: valkey SETXX %s failed: %w", sessionKey, err)
	}
	if msg != "OK" {
		return fmt.Errorf("UpdateSandbox: valkey SETXX %s, key not exists", sessionKey)
	}
	return nil
}

// DeleteSandboxBySessionID delete sandbox by session ID
func (vs *valkeyStore) DeleteSandboxBySessionID(ctx context.Context, sessionID string) error {
	sessionKey := vs.sessionKey(sessionID)

	commands := make(valkey.Commands, 0, 4)
	commands = append(commands, vs.cli.B().Del().Key(sessionKey).Build())
	commands = append(commands, vs.cli.B().Zrem().Key(vs.expiryIndexKey).Member(sessionID).Build())
	commands = append(commands, vs.cli.B().Zrem().Key(vs.idleExpiryIndexKey).Member(sessionID).Build())

	for i, resp := range vs.cli.DoMulti(ctx, commands...) {
		if err := resp.Error(); err != nil {
			return fmt.Errorf("DeleteSandboxBySessionID: DoMulti failed: %w, command index: %v", err, i)
		}
	}
	return nil
}

// ListExpiredSandboxes returns up to limit sandboxes with ExpiresAt before the given time
func (vs *valkeyStore) ListExpiredSandboxes(ctx context.Context, before time.Time, limit int64) ([]*types.SandboxInfo, error) {
	if limit <= 0 {
		return nil, nil
	}

	maxScore := before.Unix()
	ids, err := vs.cli.Do(ctx, vs.cli.B().Zrangebyscore().Key(vs.expiryIndexKey).Min("-inf").Max(fmt.Sprintf("%d", maxScore)).Limit(0, limit).Build()).AsStrSlice()

	if err != nil {
		return nil, fmt.Errorf("ListExpiredSandboxes: ZRangeByScore failed: %w", err)
	}

	return vs.loadSandboxesBySessionIDs(ctx, ids)
}

// ListInactiveSandboxes returns up to limit sandboxes whose idle deadline is at
// or before the given time, ordered by deadline.
func (vs *valkeyStore) ListInactiveSandboxes(ctx context.Context, before time.Time, limit int64) ([]*types.SandboxInfo, error) {
	if limit <= 0 {
		return nil, nil
	}

	maxScore := before.Unix()
	ids, err := vs.cli.Do(ctx,
		vs.cli.B().Zrangebyscore().Key(vs.idleExpiryIndexKey).
			Min("-inf").Max(fmt.Sprintf("%d", maxScore)).
			Limit(0, limit).Build(),
	).AsStrSlice()
	if err != nil {
		return nil, fmt.Errorf("ListInactiveSandboxes: ZRangeByScore failed: %w", err)
	}

	return vs.loadSandboxesBySessionIDs(ctx, ids)
}

// Close releases all resources held by the valkey store.
func (vs *valkeyStore) Close() error {
	vs.cli.Close()
	return nil
}

var updateValkeySessionIdleExpiryScript = valkey.NewLuaScript(updateSessionIdleExpiryLua)

// UpdateSessionLastActivity updates the session's idle expiry deadline (at + IdleTimeout).
// When force is false, the deadline only advances forward; when true, it overwrites unconditionally.
func (vs *valkeyStore) UpdateSessionLastActivity(ctx context.Context, sessionID string, at time.Time, force bool) error {
	if err := validateSessionActivity(sessionID, at); err != nil {
		return fmt.Errorf("UpdateSessionLastActivity: %w", err)
	}
	sandbox, err := vs.GetSandboxBySessionID(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("UpdateSessionLastActivity: session %s: %w", sessionID, err)
	}
	result, err := updateValkeySessionIdleExpiryScript.Exec(ctx, vs.cli,
		[]string{vs.sessionKey(sessionID), vs.idleExpiryIndexKey},
		[]string{sessionID, strconv.FormatInt(sandbox.IdleExpiryTime(at).Unix(), 10), forceFlag(force)}).AsInt64()
	if err != nil {
		return fmt.Errorf("UpdateSessionLastActivity: update deadline for session %s: %w", sessionID, err)
	}
	if result == 0 {
		return ErrNotFound
	}
	return nil
}
