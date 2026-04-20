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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"

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
}

func (f *gcFakeStore) ListInactiveSandboxes(_ context.Context, before time.Time, _ int64) ([]*types.SandboxInfo, error) {
	f.inactiveCutoff = before
	return f.inactive, f.inactiveErr
}

func (f *gcFakeStore) ListExpiredSandboxes(_ context.Context, _ time.Time, _ int64) ([]*types.SandboxInfo, error) {
	return f.expired, f.expiredErr
}

func (f *gcFakeStore) DeleteSandboxBySessionID(_ context.Context, sessionID string) error {
	if err, ok := f.deleteErr[sessionID]; ok {
		return err
	}
	f.deleted = append(f.deleted, sessionID)
	return nil
}

// newTestGC builds a garbageCollector backed by fake dynamic and clientset clients
// and the provided store. Both fakes return "not found" for all deletes (no pre-loaded
// objects), which garbageCollector treats as success. The clientset is needed so the
// NetworkPolicy cleanup path in deleteSandbox/deleteSandboxClaim does not nil-panic.
func newTestGC(s store.Store) *garbageCollector {
	scheme := runtime.NewScheme()
	fakeDynamic := dynamicfake.NewSimpleDynamicClient(scheme)
	fakeClientset := fake.NewSimpleClientset()
	return &garbageCollector{
		k8sClient:   &K8sClient{dynamicClient: fakeDynamic, clientset: fakeClientset},
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
