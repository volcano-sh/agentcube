/*
Copyright The Volcano Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
you may obtain a copy of the License at

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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/volcano-sh/agentcube/pkg/common/types"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic/fake"
	sandboxv1alpha1 "sigs.k8s.io/agent-sandbox/api/v1alpha1"
)

// gcFakeStore implements store.Store for testing garbage collection
type gcFakeStore struct {
	inactive []*types.SandboxInfo
	expired  []*types.SandboxInfo
	deleted  []string // List of sessionIDs deleted
}

func (s *gcFakeStore) Ping(ctx context.Context) error { return nil }
func (s *gcFakeStore) GetSandboxBySessionID(ctx context.Context, sessionID string) (*types.SandboxInfo, error) {
	return nil, nil
}
func (s *gcFakeStore) CreateSandbox(ctx context.Context, sandbox *types.SandboxInfo) error { return nil }
func (s *gcFakeStore) StoreSandbox(ctx context.Context, sandboxStore *types.SandboxInfo) error { return nil }
func (s *gcFakeStore) UpdateSandbox(ctx context.Context, sandbox *types.SandboxInfo) error { return nil }
func (s *gcFakeStore) DeleteSandboxBySessionID(ctx context.Context, sessionID string) error {
	s.deleted = append(s.deleted, sessionID)
	return nil
}
func (s *gcFakeStore) UpdateSessionLastActivity(ctx context.Context, sessionID string, at time.Time) error { return nil }
func (s *gcFakeStore) ListInactiveSandboxes(ctx context.Context, inactiveTime time.Time, limit int64) ([]*types.SandboxInfo, error) {
	return s.inactive, nil
}
func (s *gcFakeStore) ListExpiredSandboxes(ctx context.Context, expiredTime time.Time, limit int64) ([]*types.SandboxInfo, error) {
	return s.expired, nil
}

func TestGarbageCollector_Once(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = sandboxv1alpha1.AddToScheme(scheme)

	// Setup fake K8s client with one existing sandbox
	sb := &sandboxv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sandbox-1",
			Namespace: "default",
		},
	}
	fakeDynamic := fake.NewSimpleDynamicClient(scheme, sb)

	// Setup fake Store with one inactive sandbox (which matches the K8s one)
	// and one expired sandbox (which is already gone from K8s, simulating zombie)
	store := &gcFakeStore{
		inactive: []*types.SandboxInfo{
			{
				Name:             "sandbox-1",
				SandboxNamespace: "default",
				Kind:             types.AgentRuntimeKind,
				SessionID:        "session-1",
			},
		},
		expired: []*types.SandboxInfo{
			{
				Name:             "sandbox-2",
				SandboxNamespace: "default",
				Kind:             types.AgentRuntimeKind,
				SessionID:        "session-2",
			},
		},
		deleted: []string{},
	}

	client := &K8sClient{
		dynamicClient: fakeDynamic,
	}

	gc := newGarbageCollector(client, store, time.Minute)

	// Run once
	gc.once()

	// Verify deletions
	assert.Contains(t, store.deleted, "session-1")
	assert.Contains(t, store.deleted, "session-2")
	assert.Len(t, store.deleted, 2)

	// Verify K8s deletion
	// sandbox-1 should be deleted.
	// fakeDynamic doesn't return error on delete if not found unless Check is enabled?
	// But let's verify it is gone.
	_, err := fakeDynamic.Resource(SandboxGVR).Namespace("default").Get(context.Background(), "sandbox-1", metav1.GetOptions{})
	assert.Error(t, err) // Should be not found
}
