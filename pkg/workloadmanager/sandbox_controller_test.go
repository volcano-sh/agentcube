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
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	sandboxv1alpha1 "sigs.k8s.io/agent-sandbox/api/v1alpha1"
)

func TestWatchSandboxOnce(t *testing.T) {
	reconciler := &SandboxReconciler{}
	ctx := context.Background()

	ch := reconciler.WatchSandboxOnce(ctx, "default", "test-sb")
	require.NotNil(t, ch)

	reconciler.mu.RLock()
	defer reconciler.mu.RUnlock()
	key := types.NamespacedName{Namespace: "default", Name: "test-sb"}
	_, exists := reconciler.watchers[key]
	assert.True(t, exists)
}

func TestUnWatchSandbox(t *testing.T) {
	reconciler := &SandboxReconciler{}
	ctx := context.Background()

	reconciler.WatchSandboxOnce(ctx, "default", "test-sb")
	reconciler.UnWatchSandbox("default", "test-sb")

	reconciler.mu.RLock()
	defer reconciler.mu.RUnlock()
	key := types.NamespacedName{Namespace: "default", Name: "test-sb"}
	_, exists := reconciler.watchers[key]
	assert.False(t, exists)
}

func TestReconcile(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = sandboxv1alpha1.AddToScheme(scheme)

	t.Run("SandboxNotFound", func(t *testing.T) {
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
		reconciler := &SandboxReconciler{Client: fakeClient, Scheme: scheme}

		req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-sb", Namespace: "default"}}
		_, err := reconciler.Reconcile(context.Background(), req)
		assert.NoError(t, err)
	})

	t.Run("SandboxNotRunning", func(t *testing.T) {
		sb := &sandboxv1alpha1.Sandbox{
			ObjectMeta: metav1.ObjectMeta{Name: "test-sb", Namespace: "default"},
			Status: sandboxv1alpha1.SandboxStatus{
				Conditions: []metav1.Condition{
					{
						Type:   string(sandboxv1alpha1.SandboxConditionReady),
						Status: metav1.ConditionFalse,
					},
				},
			},
		}
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sb).Build()
		reconciler := &SandboxReconciler{Client: fakeClient, Scheme: scheme}

		// Register watcher
		ch := reconciler.WatchSandboxOnce(context.Background(), "default", "test-sb")

		req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-sb", Namespace: "default"}}
		_, err := reconciler.Reconcile(context.Background(), req)
		assert.NoError(t, err)

		// Expect nothing on channel
		select {
		case <-ch:
			t.Fatal("Unexpected status update")
		default:
		}
	})

	t.Run("SandboxRunning_WithWatcher", func(t *testing.T) {
		sb := &sandboxv1alpha1.Sandbox{
			ObjectMeta: metav1.ObjectMeta{Name: "test-sb", Namespace: "default"},
			Status: sandboxv1alpha1.SandboxStatus{
				Conditions: []metav1.Condition{
					{
						Type:   string(sandboxv1alpha1.SandboxConditionReady),
						Status: metav1.ConditionTrue,
					},
				},
			},
		}
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sb).Build()
		reconciler := &SandboxReconciler{Client: fakeClient, Scheme: scheme}

		// Register watcher
		ch := reconciler.WatchSandboxOnce(context.Background(), "default", "test-sb")

		req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-sb", Namespace: "default"}}
		_, err := reconciler.Reconcile(context.Background(), req)
		assert.NoError(t, err)

		// Expect status update
		select {
		case update := <-ch:
			assert.Equal(t, "test-sb", update.Sandbox.Name)
		case <-time.After(time.Second):
			t.Fatal("Timeout waiting for update")
		}

		// Check watcher removed
		reconciler.mu.RLock()
		key := types.NamespacedName{Namespace: "default", Name: "test-sb"}
		_, exists := reconciler.watchers[key]
		reconciler.mu.RUnlock()
		assert.False(t, exists)
	})
	
	t.Run("SandboxReady_WithWatcher", func(t *testing.T) {
		// Ready implies Running in our logic (getSandboxStatus returns "running")
		sb := &sandboxv1alpha1.Sandbox{
			ObjectMeta: metav1.ObjectMeta{Name: "test-sb", Namespace: "default"},
			Status: sandboxv1alpha1.SandboxStatus{
				Conditions: []metav1.Condition{
					{
						Type:   string(sandboxv1alpha1.SandboxConditionReady),
						Status: metav1.ConditionTrue,
					},
				},
			},
		}
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sb).Build()
		reconciler := &SandboxReconciler{Client: fakeClient, Scheme: scheme}

		// Register watcher
		ch := reconciler.WatchSandboxOnce(context.Background(), "default", "test-sb")

		req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-sb", Namespace: "default"}}
		_, err := reconciler.Reconcile(context.Background(), req)
		assert.NoError(t, err)

		// Expect status update
		select {
		case update := <-ch:
			assert.Equal(t, "test-sb", update.Sandbox.Name)
		case <-time.After(time.Second):
			t.Fatal("Timeout waiting for update")
		}
	})

	t.Run("SandboxRunning_NoWatcher", func(t *testing.T) {
		sb := &sandboxv1alpha1.Sandbox{
			ObjectMeta: metav1.ObjectMeta{Name: "test-sb", Namespace: "default"},
			Status: sandboxv1alpha1.SandboxStatus{
				Conditions: []metav1.Condition{
					{
						Type:   string(sandboxv1alpha1.SandboxConditionReady),
						Status: metav1.ConditionTrue,
					},
				},
			},
		}
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sb).Build()
		reconciler := &SandboxReconciler{Client: fakeClient, Scheme: scheme}

		req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-sb", Namespace: "default"}}
		_, err := reconciler.Reconcile(context.Background(), req)
		assert.NoError(t, err)
	})
}
