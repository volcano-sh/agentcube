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
func (nopStore) UpdateSessionLastActivity(_ context.Context, _ string, _ time.Time, _ bool) error {
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

// Idle eligibility is determined by the store's deadline index, not by GC.
func TestGarbageCollector_once_idleExpiredSandboxDeleted(t *testing.T) {
	fs := &gcFakeStore{inactive: []*types.SandboxInfo{{
		SessionID: "session-idle-expired", Kind: types.SandboxKind,
		Name: "sb-idle-expired", SandboxNamespace: "default",
	}}}
	gc := newTestGC(fs)
	gc.once()
	assert.Equal(t, []string{"session-idle-expired"}, fs.deleted)
}

func TestGarbageCollector_once_deduplicatesHardAndIdleExpiry(t *testing.T) {
	sandbox := &types.SandboxInfo{
		SessionID: "session-expired", Kind: types.SandboxKind,
		Name: "sb-expired", SandboxNamespace: "default",
	}
	fs := &gcFakeStore{
		inactive: []*types.SandboxInfo{sandbox},
		expired:  []*types.SandboxInfo{sandbox},
	}
	gc := newTestGC(fs)
	gc.once()
	assert.Equal(t, []string{"session-expired"}, fs.deleted)
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

func TestGarbageCollector_once_idleDeadlineCutoffUsesNow(t *testing.T) {
	// Deadlines can be queried directly without a minimum idle lookback.
	fs := &gcFakeStore{}
	gc := newTestGC(fs)
	before := time.Now()
	gc.once()
	after := time.Now()

	assert.True(t,
		!fs.inactiveCutoff.Before(before) && !fs.inactiveCutoff.After(after),
		"idle deadline cutoff %v should be between %v and %v",
		fs.inactiveCutoff, before, after,
	)
}

func TestGarbageCollector_once_noSandboxes(t *testing.T) {
	fs := &gcFakeStore{}
	gc := newTestGC(fs)
	gc.once()
	assert.Empty(t, fs.deleted)
}

func TestGarbageCollector_once_k8sDeleteFailurePreservesSession(t *testing.T) {
	for _, kind := range []string{types.SandboxKind, types.SandboxClaimsKind} {
		t.Run(kind, func(t *testing.T) {
			fs := &gcFakeStore{expired: []*types.SandboxInfo{{
				Kind: kind, SessionID: "session-1", Name: "sandbox-1", SandboxNamespace: "default",
			}}}
			gc := newTestGC(fs)
			dc := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
			gc.k8sClient.dynamicClient = dc
			dc.PrependReactor("delete", "*",
				func(k8stesting.Action) (bool, runtime.Object, error) {
					return true, nil, fmt.Errorf("Kubernetes unavailable")
				})

			gc.once()

			assert.Empty(t, fs.deleted, "failed Kubernetes deletion must preserve the session for retry")
		})
	}
}
