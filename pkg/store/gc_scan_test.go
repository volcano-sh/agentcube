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
	"fmt"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/require"
)

func newGCStore(t *testing.T, provider string) (Store, *miniredis.Miniredis) {
	t.Helper()
	var client Store
	var server *miniredis.Miniredis
	if provider == "redis" {
		client, server = newTestRedisClient(t)
	} else {
		client, server = newValkeyTestClient(t)
	}
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	return client, server
}

func TestGCStoreScan(t *testing.T) {
	for _, provider := range []string{"redis", "valkey"} {
		t.Run(provider, func(t *testing.T) {
			client, server := newGCStore(t, provider)
			ctx := context.Background()
			cutoff := time.Now().Add(-time.Minute).Truncate(time.Second)
			// Active IDs sort first in miniredis, exercising an empty filtered
			// page with a nonzero cursor. Real servers need not sort ZSCAN output.
			for i := 0; i < 50; i++ {
				_, err := server.ZAdd("sandbox:last_activity", float64(cutoff.Add(time.Minute).Unix()), fmt.Sprintf("active-%03d", i))
				require.NoError(t, err)
			}
			expected := make(map[string]bool)
			for i := 0; i < 237; i++ {
				id := fmt.Sprintf("inactive-%03d", i)
				expected[id] = true
				// Ties, inclusive cutoff, and IDs with no JSON record are all valid
				// scan results. Hydration decides whether the session still exists.
				_, err := server.ZAdd("sandbox:last_activity", float64(cutoff.Unix()), id)
				require.NoError(t, err)
			}
			seen := make(map[string]bool)
			var cursor uint64
			for page := 0; ; page++ {
				require.Less(t, page, 1000, "scan did not finish")
				ids, next, err := client.ScanInactiveSandboxIDs(ctx, cutoff, cursor, 17)
				require.NoError(t, err)
				for _, id := range ids {
					require.True(t, expected[id], "active session returned: %s", id)
					seen[id] = true
				}
				cursor = next
				if cursor == 0 {
					break
				}
			}
			require.Equal(t, expected, seen)
			for _, count := range []int64{0, -1} {
				_, next, err := client.ScanInactiveSandboxIDs(ctx, cutoff, 19, count)
				require.Error(t, err)
				require.Equal(t, uint64(19), next)
			}
		})
	}
}

func TestGCStoreHydration(t *testing.T) {
	for _, provider := range []string{"redis", "valkey"} {
		t.Run(provider, func(t *testing.T) {
			client, server := newGCStore(t, provider)
			ctx := context.Background()
			now := time.Now().Truncate(time.Second)
			sandbox := newTestSandbox("sb", "session", now.Add(time.Hour))
			require.NoError(t, client.StoreSandbox(ctx, sandbox))
			oldActivity := now.Add(-time.Hour)
			require.NoError(t, client.UpdateSessionLastActivity(ctx, sandbox.SessionID, oldActivity))
			loaded, err := client.GetSandboxWithLastActivity(ctx, sandbox.SessionID)
			require.NoError(t, err)
			require.True(t, loaded.LastActivityAt.Equal(oldActivity))
			// Activity comes from the current score, never the session JSON or
			// the earlier scan. Changes to the JSON are also observed.
			sandbox.Name = "updated-name"
			require.NoError(t, client.UpdateSandbox(ctx, sandbox))
			require.NoError(t, client.UpdateSessionLastActivity(ctx, sandbox.SessionID, now))
			loaded, err = client.GetSandboxWithLastActivity(ctx, sandbox.SessionID)
			require.NoError(t, err)
			require.True(t, loaded.LastActivityAt.Equal(now))
			require.Equal(t, "updated-name", loaded.Name)
			server.Del("sandbox:last_activity")
			_, err = client.GetSandboxWithLastActivity(ctx, sandbox.SessionID)
			require.ErrorIs(t, err, ErrNotFound)
			_, err = server.ZAdd("sandbox:last_activity", float64(oldActivity.Unix()), sandbox.SessionID)
			require.NoError(t, err)
			server.Del("session:" + sandbox.SessionID)
			_, err = client.GetSandboxWithLastActivity(ctx, sandbox.SessionID)
			require.ErrorIs(t, err, ErrNotFound)
			require.NoError(t, server.Set("session:"+sandbox.SessionID, "invalid JSON"))
			_, err = client.GetSandboxWithLastActivity(ctx, sandbox.SessionID)
			require.Error(t, err)
			require.NotErrorIs(t, err, ErrNotFound)
		})
	}
}

func TestGCStoreErrors(t *testing.T) {
	for _, provider := range []string{"redis", "valkey"} {
		t.Run(provider, func(t *testing.T) {
			client, server := newGCStore(t, provider)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_, next, err := client.ScanInactiveSandboxIDs(ctx, time.Now(), 13, 100)
			require.ErrorIs(t, err, context.Canceled)
			require.Equal(t, uint64(13), next)
			_, err = client.GetSandboxWithLastActivity(ctx, "session")
			require.ErrorIs(t, err, context.Canceled)

			ctx = context.Background()
			require.NoError(t, client.StoreSandbox(ctx, newTestSandbox("sb", "session", time.Now().Add(time.Hour))))
			server.Del("sandbox:last_activity")
			require.NoError(t, server.Set("sandbox:last_activity", "wrong type"))
			_, next, err = client.ScanInactiveSandboxIDs(ctx, time.Now(), 13, 100)
			require.Error(t, err)
			require.Equal(t, uint64(13), next)
			_, err = client.GetSandboxWithLastActivity(ctx, "session")
			require.Error(t, err)
			require.NotErrorIs(t, err, ErrNotFound)
		})
	}
}
