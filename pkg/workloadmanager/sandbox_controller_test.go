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

func setupSandboxReconciler() (*SandboxReconciler, *runtime.Scheme) {
	scheme := runtime.NewScheme()
	_ = sandboxv1alpha1.AddToScheme(scheme)

	c := fake.NewClientBuilder().WithScheme(scheme).Build()

	return &SandboxReconciler{
		Client: c,
		Scheme: scheme,
	}, scheme
}

func TestSandboxReconciler_Reconcile_NonExistentSandbox(t *testing.T) {
	reconciler, _ := setupSandboxReconciler()
	ctx := context.TODO()

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Namespace: "default",
			Name:      "non-existent",
		},
	}

	res, err := reconciler.Reconcile(ctx, req)
	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, res)
}

func TestSandboxReconciler_Reconcile_PendingSandbox(t *testing.T) {
	reconciler, scheme := setupSandboxReconciler()
	ctx := context.TODO()

	sandbox := &sandboxv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pending-sandbox",
			Namespace: "default",
		},
		Status: sandboxv1alpha1.SandboxStatus{
			Conditions: []metav1.Condition{
				{
					Type:   string(sandboxv1alpha1.SandboxConditionReady),
					Status: metav1.ConditionFalse,
				},
			},
		},
	}

	// Create sandbox in fake client
	c := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(sandbox).Build()
	reconciler.Client = c

	// Register watcher
	resultChan := reconciler.WatchSandboxOnce(ctx, "default", "pending-sandbox")

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Namespace: "default",
			Name:      "pending-sandbox",
		},
	}

	res, err := reconciler.Reconcile(ctx, req)
	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, res)

	// Watcher should not be notified since the sandbox is not ready
	select {
	case <-resultChan:
		t.Fatal("watcher was unexpectedly notified for non-ready sandbox")
	case <-time.After(100 * time.Millisecond):
		// Success: timeout means no notification was sent
	}
}

func TestSandboxReconciler_Reconcile_ReadySandboxNotification(t *testing.T) {
	reconciler, scheme := setupSandboxReconciler()
	ctx := context.TODO()

	sandbox := &sandboxv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ready-sandbox",
			Namespace: "default",
		},
		Status: sandboxv1alpha1.SandboxStatus{
			Conditions: []metav1.Condition{
				{
					Type:   string(sandboxv1alpha1.SandboxConditionReady),
					Status: metav1.ConditionTrue,
				},
			},
		},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(sandbox).Build()
	reconciler.Client = c

	// Register watcher
	resultChan := reconciler.WatchSandboxOnce(ctx, "default", "ready-sandbox")

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Namespace: "default",
			Name:      "ready-sandbox",
		},
	}

	res, err := reconciler.Reconcile(ctx, req)
	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, res)

	// Watcher should be notified since the sandbox is ready
	select {
	case update, ok := <-resultChan:
		require.True(t, ok)
		assert.NotNil(t, update.Sandbox)
		assert.Equal(t, "ready-sandbox", update.Sandbox.Name)
	case <-time.After(1 * time.Second):
		t.Fatal("watcher was not notified for ready sandbox within timeout")
	}

	// Verify the watcher has been removed from the map
	reconciler.mu.RLock()
	_, exists := reconciler.watchers[req.NamespacedName]
	reconciler.mu.RUnlock()
	assert.False(t, exists, "watcher should be removed from the map after notification")
}

func TestSandboxReconciler_WatchSandboxOnce_Duplicate(t *testing.T) {
	reconciler, _ := setupSandboxReconciler()
	ctx := context.TODO()

	namespace := "default"
	name := "test-sandbox"
	key := types.NamespacedName{Namespace: namespace, Name: name}

	// Register first watcher
	chan1 := reconciler.WatchSandboxOnce(ctx, namespace, name)
	require.NotNil(t, chan1)

	// Register second watcher (duplicate)
	chan2 := reconciler.WatchSandboxOnce(ctx, namespace, name)
	require.NotNil(t, chan2)
	assert.NotEqual(t, chan1, chan2)

	// The first channel should be closed immediately when replaced
	select {
	case _, ok := <-chan1:
		assert.False(t, ok, "first channel should be closed")
	default:
		t.Fatal("first channel was not closed after duplicate registration")
	}

	// The second channel should still be registered in the map
	reconciler.mu.RLock()
	registeredChan, exists := reconciler.watchers[key]
	reconciler.mu.RUnlock()
	assert.True(t, exists)
	assert.Equal(t, chan2, (<-chan SandboxStatusUpdate)(registeredChan))
}

func TestSandboxReconciler_UnWatchSandbox(t *testing.T) {
	reconciler, _ := setupSandboxReconciler()
	ctx := context.TODO()

	namespace := "default"
	name := "test-sandbox"
	key := types.NamespacedName{Namespace: namespace, Name: name}

	// Register watcher
	reconciler.WatchSandboxOnce(ctx, namespace, name)

	// Verify it exists in map
	reconciler.mu.RLock()
	_, existsBefore := reconciler.watchers[key]
	reconciler.mu.RUnlock()
	assert.True(t, existsBefore)

	// Unwatch
	reconciler.UnWatchSandbox(namespace, name)

	// Verify it is removed from map
	reconciler.mu.RLock()
	_, existsAfter := reconciler.watchers[key]
	reconciler.mu.RUnlock()
	assert.False(t, existsAfter)
}
