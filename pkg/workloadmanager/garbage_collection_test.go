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

package workloadmanager

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/volcano-sh/agentcube/pkg/common/types"
	"github.com/volcano-sh/agentcube/pkg/store"
)

// nopStore provides no-op implementations of every store.Store method.
// gcFakeStore embeds it so that any future store call added to gc.once()
// fails as a clear test error rather than panicking on a nil interface.
type nopStore struct{}

func (nopStore) Ping(_ context.Context) error { return nil }
func (nopStore) GetSandboxBySessionID(_ context.Context, _ string) (*types.SandboxInfo, error) {
	return nil, nil
}
func (nopStore) StoreSandbox(_ context.Context, _ *types.SandboxInfo) error  { return nil }
func (nopStore) UpdateSandbox(_ context.Context, _ *types.SandboxInfo) error { return nil }
func (nopStore) DeleteSandboxBySessionID(_ context.Context, _ string) error  { return nil }
func (nopStore) ListExpiredSandboxes(_ context.Context, _ time.Time, _ int64) ([]*types.SandboxInfo, error) {
	return nil, nil
}
func (nopStore) ListInactiveSandboxes(_ context.Context, _ time.Time, _ int64) ([]*types.SandboxInfo, error) {
	return nil, nil
}
func (nopStore) UpdateSessionLastActivity(_ context.Context, _ string, _ time.Time) error {
	return nil
}
func (nopStore) Close() error { return nil }

// gcFakeStore is a controllable store for GC tests.
type gcFakeStore struct {
	nopStore
	inactive    []*types.SandboxInfo
	inactiveErr error
	expired     []*types.SandboxInfo
	expiredErr  error
	// deleteErr maps sessionID → error returned by DeleteSandboxBySessionID.
	deleteErr map[string]error
	// deleted records sessions successfully deleted from the store.
	deleted []string
	// inactiveCutoff captures the `before` argument passed to ListInactiveSandboxes.
	inactiveCutoff time.Time
	scanFn         func(context.Context, time.Time, uint64, int64) ([]string, uint64, error)
	getFn          func(context.Context, string) (*types.SandboxInfo, error)
	deleteFn       func(context.Context, string) error
	expiredFn      func(context.Context, time.Time, int64) ([]*types.SandboxInfo, error)
}

func (f *gcFakeStore) ListInactiveSandboxes(_ context.Context, before time.Time, limit int64) ([]*types.SandboxInfo, error) {
	f.inactiveCutoff = before
	return f.inactive[:min(int64(len(f.inactive)), limit)], f.inactiveErr
}

func (f *gcFakeStore) ListExpiredSandboxes(ctx context.Context, before time.Time, limit int64) ([]*types.SandboxInfo, error) {
	if f.expiredFn != nil {
		return f.expiredFn(ctx, before, limit)
	}
	return f.expired, f.expiredErr
}

func (f *gcFakeStore) DeleteSandboxBySessionID(ctx context.Context, sessionID string) error {
	if f.deleteFn != nil {
		return f.deleteFn(ctx, sessionID)
	}
	if err, ok := f.deleteErr[sessionID]; ok {
		return err
	}
	f.deleted = append(f.deleted, sessionID)
	return nil
}

// newTestGC builds a garbageCollector backed by a fake dynamic client and the
// provided store. The fake dynamic client returns "not found" for all deletes
// (no pre-loaded objects), which garbageCollector treats as success.
func newTestGC(s store.Store) *garbageCollector {
	scheme := runtime.NewScheme()
	fakeDynamic := dynamicfake.NewSimpleDynamicClient(scheme)
	return &garbageCollector{
		k8sClient:   &K8sClient{dynamicClient: fakeDynamic},
		storeClient: s,
		interval:    time.Minute,
	}
}

// TestGarbageCollector_once_perSandboxIdleFilter covers the four cases the
// reviewer requested: elapsed, within, zero-fallback, and zero LastActivityAt.
func TestGarbageCollector_once_perSandboxIdleFilter(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name        string
		sandbox     *types.SandboxInfo
		wantDeleted bool
	}{
		{
			name: "IdleTimeout elapsed — should be collected",
			sandbox: &types.SandboxInfo{
				SessionID:        "session-elapsed",
				Kind:             types.SandboxKind,
				Name:             "sb-elapsed",
				SandboxNamespace: "default",
				IdleTimeout:      metav1.Duration{Duration: 15 * time.Minute},
				LastActivityAt:   now.Add(-20 * time.Minute),
			},
			wantDeleted: true,
		},
		{
			name: "within IdleTimeout — should be skipped",
			sandbox: &types.SandboxInfo{
				SessionID:        "session-active",
				Kind:             types.SandboxKind,
				Name:             "sb-active",
				SandboxNamespace: "default",
				IdleTimeout:      metav1.Duration{Duration: 15 * time.Minute},
				LastActivityAt:   now.Add(-5 * time.Minute),
			},
			wantDeleted: false,
		},
		{
			name: "IdleTimeout zero — falls back to DefaultSandboxIdleTimeout",
			sandbox: &types.SandboxInfo{
				SessionID:        "session-default-timeout",
				Kind:             types.SandboxKind,
				Name:             "sb-default",
				SandboxNamespace: "default",
				IdleTimeout:      metav1.Duration{Duration: 0},
				LastActivityAt:   now.Add(-(DefaultSandboxIdleTimeout + time.Minute)),
			},
			wantDeleted: true,
		},
		{
			name: "LastActivityAt zero, CreatedAt old — falls back to CreatedAt, should be collected",
			sandbox: &types.SandboxInfo{
				SessionID:        "session-zero-activity-old",
				Kind:             types.SandboxKind,
				Name:             "sb-zero-activity-old",
				SandboxNamespace: "default",
				IdleTimeout:      metav1.Duration{Duration: 15 * time.Minute},
				LastActivityAt:   time.Time{},
				CreatedAt:        now.Add(-20 * time.Minute),
			},
			wantDeleted: true,
		},
		{
			name: "LastActivityAt zero, CreatedAt recent — falls back to CreatedAt, should be skipped",
			sandbox: &types.SandboxInfo{
				SessionID:        "session-zero-activity-new",
				Kind:             types.SandboxKind,
				Name:             "sb-zero-activity-new",
				SandboxNamespace: "default",
				IdleTimeout:      metav1.Duration{Duration: 15 * time.Minute},
				LastActivityAt:   time.Time{},
				CreatedAt:        now.Add(-5 * time.Minute),
			},
			wantDeleted: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := &gcFakeStore{inactive: []*types.SandboxInfo{tt.sandbox}}
			gc := newTestGC(fs)
			gc.once()

			if tt.wantDeleted {
				assert.Contains(t, fs.deleted, tt.sandbox.SessionID,
					"expected session %s to be deleted", tt.sandbox.SessionID)
			} else {
				assert.NotContains(t, fs.deleted, tt.sandbox.SessionID,
					"expected session %s NOT to be deleted", tt.sandbox.SessionID)
			}
		})
	}
}

func TestGarbageCollector_once_expiredSandboxDeleted(t *testing.T) {
	fs := &gcFakeStore{
		expired: []*types.SandboxInfo{
			{
				SessionID:        "session-expired",
				Kind:             types.SandboxKind,
				Name:             "sb-expired",
				SandboxNamespace: "default",
			},
		},
	}
	gc := newTestGC(fs)
	gc.once()
	assert.Contains(t, fs.deleted, "session-expired")
}

func TestGarbageCollector_once_sandboxClaimKind(t *testing.T) {
	fs := &gcFakeStore{
		expired: []*types.SandboxInfo{
			{
				SessionID:        "session-claim",
				Kind:             types.SandboxClaimsKind,
				Name:             "sc-claim",
				SandboxNamespace: "default",
			},
		},
	}
	gc := newTestGC(fs)
	gc.once()
	assert.Contains(t, fs.deleted, "session-claim")
}

func TestGarbageCollector_once_storeDeleteErrorDoesNotAbortOthers(t *testing.T) {
	fs := &gcFakeStore{
		expired: []*types.SandboxInfo{
			{
				SessionID:        "session-fail",
				Kind:             types.SandboxKind,
				Name:             "sb-fail",
				SandboxNamespace: "default",
			},
			{
				SessionID:        "session-ok",
				Kind:             types.SandboxKind,
				Name:             "sb-ok",
				SandboxNamespace: "default",
			},
		},
		deleteErr: map[string]error{
			"session-fail": fmt.Errorf("store delete failed"),
		},
	}
	gc := newTestGC(fs)
	gc.once()

	assert.Contains(t, fs.deleted, "session-ok")
	assert.NotContains(t, fs.deleted, "session-fail")
}

func TestGarbageCollector_once_inactiveCutoffUsesGcMinLookback(t *testing.T) {
	// once() must pass `now - gcMinInactiveLookback` as the cutoff.
	fs := &gcFakeStore{}
	gc := newTestGC(fs)
	before := time.Now()
	gc.once()
	after := time.Now()

	expectedMin := before.Add(-gcMinInactiveLookback)
	expectedMax := after.Add(-gcMinInactiveLookback)
	assert.True(t,
		!fs.inactiveCutoff.Before(expectedMin) && !fs.inactiveCutoff.After(expectedMax),
		"inactive cutoff %v should be between %v and %v (now - gcMinInactiveLookback)",
		fs.inactiveCutoff, expectedMin, expectedMax,
	)
}

func TestGarbageCollector_once_noSandboxes(t *testing.T) {
	fs := &gcFakeStore{}
	gc := newTestGC(fs)
	gc.once()
	assert.Empty(t, fs.deleted)
}

// The first page contains valid sessions whose individual timeouts have not elapsed.
func TestGarbageCollector_once_candidateLimitStarvation(t *testing.T) {
	now := time.Now()
	fs := &gcFakeStore{}
	for i := 0; i < gcCandidateLimit; i++ {
		fs.inactive = append(fs.inactive, &types.SandboxInfo{
			SessionID: fmt.Sprintf("long-%03d", i),
			Name:      fmt.Sprintf("long-%03d", i), SandboxNamespace: "default",
			LastActivityAt: now.Add(-2 * time.Hour),
			IdleTimeout:    metav1.Duration{Duration: 24 * time.Hour},
		})
	}
	fs.inactive = append(fs.inactive, &types.SandboxInfo{
		SessionID: "eligible", Name: "eligible", SandboxNamespace: "default",
		LastActivityAt: now.Add(-time.Hour),
		IdleTimeout:    metav1.Duration{Duration: 5 * time.Minute},
	})
	gc := newTestGC(fs)
	gc.once()
	assert.Equal(t, []string{"eligible"}, fs.deleted)
}

func (*nopStore) ScanInactiveSandboxIDs(_ context.Context, _ time.Time, _ uint64, _ int64) ([]string, uint64, error) {
	return nil, 0, nil
}
func (*nopStore) GetSandboxWithLastActivity(_ context.Context, _ string) (*types.SandboxInfo, error) {
	return nil, nil
}

// Scan positions are stable in this fake, even after a deletion. Explicit page
// tests below exercise opaque cursor values and empty/oversized responses.
func (f *gcFakeStore) ScanInactiveSandboxIDs(ctx context.Context, before time.Time, cursor uint64, count int64) ([]string, uint64, error) {
	if f.scanFn != nil {
		return f.scanFn(ctx, before, cursor, count)
	}
	f.inactiveCutoff = before
	if f.inactiveErr != nil {
		return nil, cursor, f.inactiveErr
	}
	if count <= 0 || cursor >= uint64(len(f.inactive)) {
		return nil, 0, nil
	}
	end := cursor + min(uint64(count), uint64(len(f.inactive))-cursor)
	ids := make([]string, 0, end-cursor)
	for i := cursor; i < end; i++ {
		ids = append(ids, f.inactive[i].SessionID)
	}
	next := end
	if end == uint64(len(f.inactive)) {
		next = 0
	}
	return ids, next, nil
}

func (f *gcFakeStore) GetSandboxWithLastActivity(ctx context.Context, id string) (*types.SandboxInfo, error) {
	if f.getFn != nil {
		return f.getFn(ctx, id)
	}
	for _, sandbox := range f.inactive {
		if sandbox.SessionID == id {
			return sandbox, nil
		}
	}
	return nil, store.ErrNotFound
}

func gcIdleSandbox(id string, now time.Time) *types.SandboxInfo {
	return &types.SandboxInfo{
		SessionID: id, Name: id, SandboxNamespace: "default", Kind: types.SandboxKind,
		LastActivityAt: now.Add(-time.Hour), IdleTimeout: metav1.Duration{Duration: 5 * time.Minute},
	}
}

func TestGarbageCollector_scanPages(t *testing.T) {
	now := time.Now()
	fs := &gcFakeStore{}
	ids := make([]string, 0, 201)
	for i := 0; i < 201; i++ {
		id := fmt.Sprintf("session-%03d", i)
		ids = append(ids, id)
		fs.inactive = append(fs.inactive, gcIdleSandbox(id, now))
	}
	fs.inactive[1].Kind = types.SandboxClaimsKind
	fs.expired = []*types.SandboxInfo{fs.inactive[0]}
	var cursors []uint64
	fs.scanFn = func(_ context.Context, _ time.Time, cursor uint64, count int64) ([]string, uint64, error) {
		require.EqualValues(t, 100, count)
		cursors = append(cursors, cursor)
		switch cursor {
		case 0:
			return nil, 73, nil // Empty nonterminal page.
		case 73:
			return append([]string(nil), ids...), 12, nil // COUNT is only a hint.
		case 12:
			// All preceding candidates have been deleted. The returned cursor
			// must still be used; rank-based offsets would skip remaining work.
			require.Len(t, fs.deleted, len(ids))
			fs.inactive = append(fs.inactive, gcIdleSandbox("last", now))
			return []string{ids[1], "missing", "last", "last"}, 0, nil
		default:
			t.Fatalf("unexpected cursor %d", cursor)
			return nil, 0, nil
		}
	}
	gc := newTestGC(fs)
	gc.collect(context.Background(), now)
	require.Equal(t, []uint64{0, 73, 12}, cursors)
	require.Equal(t, append(ids, "last"), fs.deleted)
	require.Empty(t, gc.inactivePending)
	require.Zero(t, gc.inactiveCursor)
	actions := gcTestDynamicClient(t, gc).Actions()
	require.Len(t, actions, len(ids)+1)
	require.Equal(t, SandboxClaimGVR, actions[1].GetResource())
}

func TestGarbageCollector_resumeFinalPageRefreshesActivity(t *testing.T) {
	now := time.Now()
	fs := &gcFakeStore{inactive: []*types.SandboxInfo{
		gcIdleSandbox("first", now), gcIdleSandbox("refreshed", now), gcIdleSandbox("last", now),
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fs.getFn = func(_ context.Context, id string) (*types.SandboxInfo, error) {
		if id == "refreshed" {
			cancel()
			return nil, ctx.Err()
		}
		return fs.inactive[0], nil
	}
	gc := newTestGC(fs)
	gc.collect(ctx, now)
	require.Equal(t, []string{"first"}, fs.deleted)
	require.Equal(t, []string{"refreshed", "last"}, gc.inactivePending)
	require.Zero(t, gc.inactiveCursor, "terminal scan page still has pending work")

	fs.getFn = nil
	fs.inactive[1].LastActivityAt = now
	fs.inactive[1].IdleTimeout.Duration = time.Second // Even an elapsed short timeout respects the lookback.
	fs.scanFn = func(context.Context, time.Time, uint64, int64) ([]string, uint64, error) {
		t.Fatal("must drain pending final page without starting a new scan")
		return nil, 0, nil
	}
	gc.collect(context.Background(), now.Add(30*time.Second))
	require.Equal(t, []string{"first", "last"}, fs.deleted)
	require.Empty(t, gc.inactivePending)
}

func TestGarbageCollector_resumeCursorAfterScanCancellation(t *testing.T) {
	now := time.Now()
	fs := &gcFakeStore{inactive: []*types.SandboxInfo{gcIdleSandbox("eligible", now)}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var cursors []uint64
	fs.scanFn = func(_ context.Context, _ time.Time, cursor uint64, _ int64) ([]string, uint64, error) {
		cursors = append(cursors, cursor)
		if cursor == 0 {
			return nil, 29, nil
		}
		cancel()
		return nil, 0, ctx.Err()
	}
	gc := newTestGC(fs)
	gc.collect(ctx, now)
	require.Equal(t, uint64(29), gc.inactiveCursor)
	require.Empty(t, fs.deleted)
	fs.scanFn = func(_ context.Context, _ time.Time, cursor uint64, _ int64) ([]string, uint64, error) {
		cursors = append(cursors, cursor)
		return []string{"eligible"}, 0, nil
	}
	gc.collect(context.Background(), now)
	require.Equal(t, []uint64{0, 29, 29}, cursors)
	require.Equal(t, []string{"eligible"}, fs.deleted)
}

func TestGarbageCollector_sessionFailuresDoNotBlockSweep(t *testing.T) {
	now := time.Now()
	fs := &gcFakeStore{deleteErr: map[string]error{"store-error": fmt.Errorf("store unavailable")}}
	for _, id := range []string{"missing", "read-error", "k8s-error", "store-error", "healthy"} {
		fs.inactive = append(fs.inactive, gcIdleSandbox(id, now))
	}
	fs.getFn = func(_ context.Context, id string) (*types.SandboxInfo, error) {
		switch id {
		case "missing":
			return nil, store.ErrNotFound
		case "read-error":
			return nil, fmt.Errorf("invalid JSON")
		default:
			return gcIdleSandbox(id, now), nil
		}
	}
	gc := newTestGC(fs)
	client := gcTestDynamicClient(t, gc)
	client.PrependReactor("delete", "sandboxes", func(action k8stesting.Action) (bool, runtime.Object, error) {
		deleteAction, ok := action.(k8stesting.DeleteAction)
		require.True(t, ok)
		if deleteAction.GetName() == "k8s-error" {
			return true, nil, fmt.Errorf("Kubernetes unavailable")
		}
		return false, nil, nil
	})
	gc.collect(context.Background(), now)
	require.Equal(t, []string{"healthy"}, fs.deleted)
	require.Empty(t, gc.inactivePending)
	// Failed sessions are eligible for retry when the next sweep starts.
	fs.getFn = nil
	fs.deleteErr = nil
	client.ReactionChain = nil
	gc.collect(context.Background(), now)
	require.Contains(t, fs.deleted, "read-error")
	require.Contains(t, fs.deleted, "store-error")
	require.Contains(t, fs.deleted, "k8s-error")
}

func TestGarbageCollector_expiryBudgetLeavesTimeForInactive(t *testing.T) {
	now := time.Now()
	fs := &gcFakeStore{inactive: []*types.SandboxInfo{gcIdleSandbox("eligible", now)}}
	fs.expiredFn = func(ctx context.Context, _ time.Time, _ int64) ([]*types.SandboxInfo, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	gc := newTestGC(fs)
	gc.collect(ctx, now)
	require.NoError(t, ctx.Err(), "expiry phase consumed the entire run deadline")
	require.Equal(t, []string{"eligible"}, fs.deleted)
}

func TestGarbageCollector_cancellationAfterKubernetesDelete(t *testing.T) {
	now := time.Now()
	fs := &gcFakeStore{inactive: []*types.SandboxInfo{gcIdleSandbox("eligible", now)}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	gc := newTestGC(fs)
	client := gcTestDynamicClient(t, gc)
	client.PrependReactor("delete", "sandboxes", func(k8stesting.Action) (bool, runtime.Object, error) {
		cancel()
		return true, nil, nil
	})
	gc.collect(ctx, now)
	require.Empty(t, fs.deleted, "must not delete the store entry using a canceled context")
	require.Equal(t, []string{"eligible"}, gc.inactivePending)
	client.ReactionChain = nil
	gc.collect(context.Background(), now)
	require.Equal(t, []string{"eligible"}, fs.deleted)
}

func gcTestDynamicClient(t *testing.T, gc *garbageCollector) *dynamicfake.FakeDynamicClient {
	t.Helper()
	client, ok := gc.k8sClient.dynamicClient.(*dynamicfake.FakeDynamicClient)
	require.True(t, ok)
	return client
}

func TestGarbageCollector_listingFailuresAreIndependent(t *testing.T) {
	now := time.Now()
	for _, failing := range []string{"expiry", "inactive"} {
		t.Run(failing, func(t *testing.T) {
			fs := &gcFakeStore{}
			if failing == "expiry" {
				fs.expiredErr = fmt.Errorf("expiry index unavailable")
				fs.inactive = []*types.SandboxInfo{gcIdleSandbox("idle", now)}
			} else {
				fs.inactiveErr = fmt.Errorf("activity index unavailable")
				fs.expired = []*types.SandboxInfo{gcIdleSandbox("expired", now)}
			}
			newTestGC(fs).collect(context.Background(), now)
			require.Len(t, fs.deleted, 1, "one listing failure must not block the other path")
		})
	}
}

func TestGarbageCollector_cancellationDuringStoreDelete(t *testing.T) {
	now := time.Now()
	fs := &gcFakeStore{inactive: []*types.SandboxInfo{gcIdleSandbox("eligible", now)}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fs.deleteFn = func(context.Context, string) error {
		cancel()
		return ctx.Err()
	}
	gc := newTestGC(fs)
	gc.collect(ctx, now)
	require.Equal(t, []string{"eligible"}, gc.inactivePending)
	require.Empty(t, fs.deleted)
	fs.deleteFn = nil
	gc.collect(context.Background(), now)
	require.Equal(t, []string{"eligible"}, fs.deleted)
}
