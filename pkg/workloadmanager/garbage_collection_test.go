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

	"github.com/volcano-sh/agentcube/pkg/common/types"
	"github.com/volcano-sh/agentcube/pkg/store"
)

// gcFakeStore is a controllable store for GC tests.
type gcFakeStore struct {
	store.Store
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

func TestGarbageCollector_once(t *testing.T) {
	tests := []struct {
		name           string
		inactive       []*types.SandboxInfo
		expired        []*types.SandboxInfo
		wantDeleted    []string
		wantNotDeleted []string
	}{
		{
			name: "inactive sandbox is deleted",
			inactive: []*types.SandboxInfo{
				{
					SessionID:        "session-inactive",
					Kind:             types.SandboxKind,
					Name:             "sb-inactive",
					SandboxNamespace: "default",
				},
			},
			wantDeleted: []string{"session-inactive"},
		},
		{
			name: "expired sandbox is deleted",
			expired: []*types.SandboxInfo{
				{
					SessionID:        "session-expired",
					Kind:             types.SandboxKind,
					Name:             "sb-expired",
					SandboxNamespace: "default",
				},
			},
			wantDeleted: []string{"session-expired"},
		},
		{
			name: "SandboxClaim kind goes through claim delete path",
			expired: []*types.SandboxInfo{
				{
					SessionID:        "session-claim",
					Kind:             types.SandboxClaimsKind,
					Name:             "sc-claim",
					SandboxNamespace: "default",
				},
			},
			wantDeleted: []string{"session-claim"},
		},
		{
			name: "both inactive and expired sandboxes are collected",
			inactive: []*types.SandboxInfo{
				{
					SessionID:        "session-inactive",
					Kind:             types.SandboxKind,
					Name:             "sb-inactive",
					SandboxNamespace: "default",
				},
			},
			expired: []*types.SandboxInfo{
				{
					SessionID:        "session-expired",
					Kind:             types.SandboxKind,
					Name:             "sb-expired",
					SandboxNamespace: "default",
				},
			},
			wantDeleted: []string{"session-inactive", "session-expired"},
		},
		{
			name:        "no sandboxes — nothing deleted",
			inactive:    nil,
			expired:     nil,
			wantDeleted: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := &gcFakeStore{
				inactive: tt.inactive,
				expired:  tt.expired,
			}
			gc := newTestGC(fs)
			gc.once()

			for _, id := range tt.wantDeleted {
				assert.Contains(t, fs.deleted, id, "expected session %s to be deleted", id)
			}
			for _, id := range tt.wantNotDeleted {
				assert.NotContains(t, fs.deleted, id, "expected session %s NOT to be deleted", id)
			}
		})
	}
}

func TestGarbageCollector_once_inactiveCutoffUsesDefaultIdleTimeout(t *testing.T) {
	// once() must pass `now - DefaultSandboxIdleTimeout` as the cutoff to
	// ListInactiveSandboxes. Verify the cutoff is within 2s of that value.
	fs := &gcFakeStore{}
	gc := newTestGC(fs)
	before := time.Now()
	gc.once()
	after := time.Now()

	expectedCutoffMin := before.Add(-DefaultSandboxIdleTimeout)
	expectedCutoffMax := after.Add(-DefaultSandboxIdleTimeout)
	assert.True(t,
		!fs.inactiveCutoff.Before(expectedCutoffMin) && !fs.inactiveCutoff.After(expectedCutoffMax),
		"inactive cutoff %v should be between %v and %v (now - DefaultSandboxIdleTimeout)",
		fs.inactiveCutoff, expectedCutoffMin, expectedCutoffMax,
	)
}

func TestGarbageCollector_once_storeDeleteErrorDoesNotAbortOthers(t *testing.T) {
	// Store delete fails for the first sandbox. The second must still be deleted.
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

	assert.Contains(t, fs.deleted, "session-ok",
		"session-ok must be deleted even though session-fail errored")
	assert.NotContains(t, fs.deleted, "session-fail")
}
