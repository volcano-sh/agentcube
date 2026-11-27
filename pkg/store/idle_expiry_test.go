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
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/volcano-sh/agentcube/pkg/common/types"
)

// Exercise the same deadline contract against both storage backends.
func TestIdleExpiry(t *testing.T) {
	backends := map[string]func(*testing.T) (Store, *miniredis.Miniredis){
		"redis": func(t *testing.T) (Store, *miniredis.Miniredis) {
			return newTestRedisClient(t)
		},
		"valkey": func(t *testing.T) (Store, *miniredis.Miniredis) {
			return newValkeyTestClient(t)
		},
	}
	for name, setup := range backends {
		t.Run(name, func(t *testing.T) {
			t.Run("initial deadline uses provisional grace period", func(t *testing.T) {
				for _, timeout := range []time.Duration{0, 30 * time.Second, 24 * time.Hour} {
					t.Run(timeout.String(), func(t *testing.T) {
						client, mr := setup(t)
						t.Cleanup(func() { _ = client.Close() })
						sandbox := newTestSandbox("sandbox", "session", time.Now().Add(48*time.Hour))
						sandbox.IdleTimeout = metav1.Duration{Duration: timeout}
						sandbox.CreatedAt = time.Date(2026, time.January, 1, 12, 0, 0, 0, time.UTC)
						require.NoError(t, client.StoreSandbox(context.Background(), sandbox))
						score, err := mr.ZScore("session:idle_expiry", sandbox.SessionID)
						require.NoError(t, err)
						require.Equal(t, sandbox.CreatedAt.Add(sandboxStartupGracePeriod).Unix(), int64(score))
						data, err := mr.Get("session:" + sandbox.SessionID)
						require.NoError(t, err)
						require.NotContains(t, data, `"lastActivityAt"`)
					})
				}
			})

			t.Run("long timeouts cannot starve eligible sessions", func(t *testing.T) {
				client, _ := setup(t)
				t.Cleanup(func() { _ = client.Close() })
				ctx := context.Background()
				now := time.Now().UTC().Truncate(time.Second)
				for i := 0; i < 100; i++ {
					id := fmt.Sprintf("long-timeout-%03d", i)
					sandbox := newTestSandbox(id, id, now.Add(48*time.Hour))
					sandbox.IdleTimeout = metav1.Duration{Duration: 24 * time.Hour}
					sandbox.CreatedAt = now.Add(-3 * time.Hour)
					require.NoError(t, client.StoreSandbox(ctx, sandbox))
					require.NoError(t, client.UpdateSessionLastActivity(ctx, id, now.Add(-2*time.Hour), true))
				}
				eligible := newTestSandbox("eligible", "eligible", now.Add(time.Hour))
				eligible.IdleTimeout = metav1.Duration{Duration: 5 * time.Minute}
				eligible.CreatedAt = now.Add(-2 * time.Hour)
				require.NoError(t, client.StoreSandbox(ctx, eligible))
				require.NoError(t, client.UpdateSessionLastActivity(ctx, eligible.SessionID, now.Add(-time.Hour), true))
				for i := 0; i < 3; i++ {
					results, err := client.ListInactiveSandboxes(ctx, now, 100)
					require.NoError(t, err)
					require.Len(t, results, 1)
					require.Equal(t, eligible.SessionID, results[0].SessionID)
				}
			})

			t.Run("inclusive cutoff refresh and cleanup", func(t *testing.T) {
				client, mr := setup(t)
				t.Cleanup(func() { _ = client.Close() })
				ctx := context.Background()
				now := time.Now().UTC().Truncate(time.Second)
				sandbox := newTestSandbox("sandbox", "session", now.Add(time.Hour))
				sandbox.CreatedAt = now.Add(-types.DefaultSandboxIdleTimeout)
				require.NoError(t, client.StoreSandbox(ctx, sandbox))
				original, err := mr.Get("session:" + sandbox.SessionID)
				require.NoError(t, err)
				require.NoError(t, client.UpdateSessionLastActivity(ctx, sandbox.SessionID, now.Add(-types.DefaultSandboxIdleTimeout), true))
				results, err := client.ListInactiveSandboxes(ctx, now, 1)
				require.NoError(t, err)
				require.Len(t, results, 1)
				require.NoError(t, client.UpdateSessionLastActivity(ctx, sandbox.SessionID, now, false))
				results, err = client.ListInactiveSandboxes(ctx, now, 1)
				require.NoError(t, err)
				require.Empty(t, results)
				updated, err := mr.Get("session:" + sandbox.SessionID)
				require.NoError(t, err)
				require.Equal(t, original, updated)
				results, err = client.ListExpiredSandboxes(ctx, now, 1)
				require.NoError(t, err)
				require.Empty(t, results)
				for _, limit := range []int64{0, -1} {
					results, err = client.ListInactiveSandboxes(ctx, now.Add(time.Hour), limit)
					require.NoError(t, err)
					require.Empty(t, results)
				}
				require.NoError(t, client.DeleteSandboxBySessionID(ctx, sandbox.SessionID))
				_, err = mr.ZScore("session:idle_expiry", sandbox.SessionID)
				require.Error(t, err)
				require.ErrorIs(t, client.UpdateSessionLastActivity(ctx, sandbox.SessionID, now, false), ErrNotFound)
				_, err = mr.ZScore("session:idle_expiry", sandbox.SessionID)
				require.Error(t, err, "updating a deleted session must not recreate its index entry")
				require.Error(t, client.UpdateSessionLastActivity(ctx, "", now, false))
				require.Error(t, client.UpdateSessionLastActivity(ctx, "session", time.Time{}, false))
			})

			t.Run("concurrent activity and metadata updates preserve latest deadline", func(t *testing.T) {
				client, mr := setup(t)
				t.Cleanup(func() { _ = client.Close() })
				ctx := context.Background()
				sandbox := newTestSandbox("sandbox", "session", time.Now().Add(48*time.Hour))
				sandbox.IdleTimeout = metav1.Duration{Duration: 5 * time.Minute}
				require.NoError(t, client.StoreSandbox(ctx, sandbox))
				base := time.Now().UTC().Add(time.Hour)
				const requests = 16
				errs := make(chan error, requests+1)
				var wg sync.WaitGroup
				for i := 0; i < requests; i++ {
					wg.Add(1)
					go func(i int) {
						defer wg.Done()
						errs <- client.UpdateSessionLastActivity(ctx, sandbox.SessionID, base.Add(time.Duration(i)*time.Second), false)
					}(i)
				}
				wg.Add(1)
				go func() {
					defer wg.Done()
					stale := *sandbox
					stale.Name = "updated"
					errs <- client.UpdateSandbox(ctx, &stale)
				}()
				wg.Wait()
				close(errs)
				for err := range errs {
					require.NoError(t, err)
				}
				// An older request arriving last must not move the deadline backward.
				require.NoError(t, client.UpdateSessionLastActivity(ctx, sandbox.SessionID, base, false))
				stored, err := client.GetSandboxBySessionID(ctx, sandbox.SessionID)
				require.NoError(t, err)
				require.Equal(t, "updated", stored.Name)
				require.True(t, sandbox.ExpiresAt.Equal(stored.ExpiresAt))
				score, err := mr.ZScore("session:idle_expiry", sandbox.SessionID)
				require.NoError(t, err)
				want := base.Add((requests - 1) * time.Second).Add(sandbox.IdleTimeout.Duration)
				require.Equal(t, want.Unix(), int64(score))
			})

			t.Run("rejects zero CreatedAt", func(t *testing.T) {
				client, mr := setup(t)
				t.Cleanup(func() { _ = client.Close() })
				sandbox := newTestSandbox("sandbox", "session", time.Now().Add(time.Hour))
				sandbox.CreatedAt = time.Time{}
				require.Error(t, client.StoreSandbox(context.Background(), sandbox))
				require.False(t, mr.Exists("session:"+sandbox.SessionID))
				_, err := mr.ZScore("session:idle_expiry", sandbox.SessionID)
				require.Error(t, err)
			})

			t.Run("creating placeholder is protected by grace period and overwritten on ready", func(t *testing.T) {
				client, mr := setup(t)
				t.Cleanup(func() { _ = client.Close() })
				ctx := context.Background()
				createdAt := time.Now().UTC().Add(-50 * time.Second).Truncate(time.Second)
				sandbox := newTestSandbox("sandbox", "creating-session", createdAt.Add(time.Hour))
				sandbox.CreatedAt = createdAt
				sandbox.Status = "creating"
				sandbox.IdleTimeout = metav1.Duration{Duration: 30 * time.Second}
				require.NoError(t, client.StoreSandbox(ctx, sandbox))

				// Provisional deadline protects the sandbox during startup (50s > 30s IdleTimeout)
				// and bounds orphan cleanup if workload-manager crashes mid-creation.
				provisional := createdAt.Add(sandboxStartupGracePeriod)
				score, err := mr.ZScore("session:idle_expiry", sandbox.SessionID)
				require.NoError(t, err)
				require.Equal(t, provisional.Unix(), int64(score))

				results, err := client.ListInactiveSandboxes(ctx, createdAt.Add(50*time.Second), 10)
				require.NoError(t, err)
				require.Empty(t, results)
				results, err = client.ListInactiveSandboxes(ctx, provisional, 10)
				require.NoError(t, err)
				require.Len(t, results, 1)

				// Once ready, force=true overwrites the 3m provisional deadline with readyAt + 30s.
				readyAt := createdAt.Add(50 * time.Second)
				sandbox.Status = "ready"
				require.NoError(t, client.UpdateSandbox(ctx, sandbox))
				require.NoError(t, client.UpdateSessionLastActivity(ctx, sandbox.SessionID, readyAt, true))
				score, err = mr.ZScore("session:idle_expiry", sandbox.SessionID)
				require.NoError(t, err)
				require.Equal(t, readyAt.Add(30*time.Second).Unix(), int64(score))
				results, err = client.ListInactiveSandboxes(ctx, readyAt.Add(29*time.Second), 10)
				require.NoError(t, err)
				require.Empty(t, results)
				results, err = client.ListInactiveSandboxes(ctx, readyAt.Add(30*time.Second), 10)
				require.NoError(t, err)
				require.Len(t, results, 1)
			})
		})
	}
}
