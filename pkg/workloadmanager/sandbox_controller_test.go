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
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"

	sandboxv1alpha1 "sigs.k8s.io/agent-sandbox/api/v1alpha1"
	extensionsv1alpha1 "sigs.k8s.io/agent-sandbox/extensions/api/v1alpha1"

	"github.com/volcano-sh/agentcube/pkg/common/types"
	"github.com/volcano-sh/agentcube/pkg/store"
)

// sessionFakeStore keeps sessions in a map for session cleanup tests.
type sessionFakeStore struct {
	nopStore
	sessions  map[string]*types.SandboxInfo
	getErr    error
	deleteErr error
	deleted   []string
}

func (f *sessionFakeStore) GetSandboxBySessionID(_ context.Context, sessionID string) (*types.SandboxInfo, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	info, ok := f.sessions[sessionID]
	if !ok {
		return nil, store.ErrNotFound
	}
	return info, nil
}

func (f *sessionFakeStore) DeleteSandboxBySessionID(_ context.Context, sessionID string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	delete(f.sessions, sessionID)
	f.deleted = append(f.deleted, sessionID)
	return nil
}

func objectMeta(namespace, name, sessionID string) metav1.ObjectMeta {
	meta := metav1.ObjectMeta{Namespace: namespace, Name: name}
	if sessionID != "" {
		meta.Labels = map[string]string{SessionIdLabelKey: sessionID}
	}
	return meta
}

func TestSessionCleanupHandler_Delete(t *testing.T) {
	directSession := &types.SandboxInfo{
		Kind:             types.SandboxKind,
		SessionID:        "session-direct",
		SandboxNamespace: "default",
		Name:             "sandbox-a",
	}
	claimSession := &types.SandboxInfo{
		Kind:             types.SandboxClaimsKind,
		SessionID:        "session-claim",
		SandboxNamespace: "default",
		Name:             "claim-a",
	}

	tests := []struct {
		name        string
		obj         client.Object
		getErr      error
		deleteErr   error
		wantDeleted []string
	}{
		{
			name:        "deleted Sandbox removes its session",
			obj:         &sandboxv1alpha1.Sandbox{ObjectMeta: objectMeta("default", "sandbox-a", "session-direct")},
			wantDeleted: []string{"session-direct"},
		},
		{
			name:        "deleted SandboxClaim removes its session",
			obj:         &extensionsv1alpha1.SandboxClaim{ObjectMeta: objectMeta("default", "claim-a", "session-claim")},
			wantDeleted: []string{"session-claim"},
		},
		{
			name: "name mismatch keeps the session",
			obj:  &sandboxv1alpha1.Sandbox{ObjectMeta: objectMeta("default", "sandbox-other", "session-direct")},
		},
		{
			name: "kind mismatch keeps the session",
			obj:  &sandboxv1alpha1.Sandbox{ObjectMeta: objectMeta("default", "claim-a", "session-claim")},
		},
		{
			name: "namespace mismatch keeps the session",
			obj:  &sandboxv1alpha1.Sandbox{ObjectMeta: objectMeta("other", "sandbox-a", "session-direct")},
		},
		{
			name: "object without session label is ignored",
			obj:  &sandboxv1alpha1.Sandbox{ObjectMeta: objectMeta("default", "sandbox-a", "")},
		},
		{
			name: "session already gone from store",
			obj:  &sandboxv1alpha1.Sandbox{ObjectMeta: objectMeta("default", "sandbox-b", "session-missing")},
		},
		{
			name:   "store get error keeps the session",
			obj:    &sandboxv1alpha1.Sandbox{ObjectMeta: objectMeta("default", "sandbox-a", "session-direct")},
			getErr: errors.New("connection refused"),
		},
		{
			name:      "store delete error is not fatal",
			obj:       &sandboxv1alpha1.Sandbox{ObjectMeta: objectMeta("default", "sandbox-a", "session-direct")},
			deleteErr: errors.New("connection refused"),
		},
		{
			name: "other object types are ignored",
			obj:  &corev1.Pod{ObjectMeta: objectMeta("default", "sandbox-a", "session-direct")},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &sessionFakeStore{
				sessions: map[string]*types.SandboxInfo{
					directSession.SessionID: directSession,
					claimSession.SessionID:  claimSession,
				},
				getErr:    tt.getErr,
				deleteErr: tt.deleteErr,
			}
			r := &SandboxReconciler{Store: fake}

			r.SessionCleanupHandler().Delete(context.Background(), event.DeleteEvent{Object: tt.obj}, nil)

			assert.Equal(t, tt.wantDeleted, fake.deleted)
		})
	}
}

func TestSessionCleanupHandler_NilStore(t *testing.T) {
	r := &SandboxReconciler{}
	obj := &sandboxv1alpha1.Sandbox{ObjectMeta: objectMeta("default", "sandbox-a", "session-direct")}

	assert.NotPanics(t, func() {
		r.SessionCleanupHandler().Delete(context.Background(), event.DeleteEvent{Object: obj}, nil)
	})
}
