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
	"sync"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	sandboxv1alpha1 "sigs.k8s.io/agent-sandbox/api/v1alpha1"
)

type SandboxReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	// Map of watchers waiting for sandbox to reach Running state
	watchers map[types.NamespacedName]chan SandboxStatusUpdate
	mu       sync.RWMutex // Protect watchers map
}

type SandboxStatusUpdate struct {
	Sandbox *sandboxv1alpha1.Sandbox
	Err     error
}

func (r *SandboxReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	sandbox := &sandboxv1alpha1.Sandbox{}
	if err := r.Get(ctx, req.NamespacedName, sandbox); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	status, failMsg := getSandboxStatus(sandbox)

	// Only notify the waiter on a terminal state (running or failed).
	// "unknown" means the sandbox is still being scheduled/started; stay quiet.
	var (
		update  SandboxStatusUpdate
		hasWork bool
	)
	switch status {
	case sandboxStatusRunning:
		klog.V(2).Infof("Sandbox %s/%s is running, notifying waiter", sandbox.Namespace, sandbox.Name)
		update = SandboxStatusUpdate{Sandbox: sandbox}
		hasWork = true
	case sandboxStatusFailed:
		klog.Warningf("Sandbox %s/%s entered a terminal failure state: %s", sandbox.Namespace, sandbox.Name, failMsg)
		update = SandboxStatusUpdate{
			Sandbox: sandbox,
			Err:     fmt.Errorf("sandbox %s/%s failed: %s", sandbox.Namespace, sandbox.Name, failMsg),
		}
		hasWork = true
	default:
		return ctrl.Result{}, nil
	}

	r.mu.Lock()
	resultChan, exists := r.watchers[req.NamespacedName]
	if exists {
		delete(r.watchers, req.NamespacedName)
	}
	r.mu.Unlock()

	if exists && hasWork {
		// WatchSandboxOnce always creates a buffered channel of size 1, and the
		// map entry is deleted before this point so only one sender can ever
		// reach here for a given key. The buffer is therefore always empty and
		// this send never blocks.
		resultChan <- update
		klog.V(2).Infof("Notified waiter about sandbox %s/%s (status: %s)", sandbox.Namespace, sandbox.Name, status)
	}

	return ctrl.Result{}, nil
}

func (r *SandboxReconciler) WatchSandboxOnce(_ context.Context, namespace, name string) <-chan SandboxStatusUpdate {
	resultChan := make(chan SandboxStatusUpdate, 1)
	key := types.NamespacedName{Namespace: namespace, Name: name}

	// Not running yet, register for future notification
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.watchers == nil {
		r.watchers = make(map[types.NamespacedName]chan SandboxStatusUpdate)
	}
	r.watchers[key] = resultChan
	klog.V(2).Infof("Registered for future notification for sandbox %s/%s", key.Namespace, key.Name)

	return resultChan
}

func (r *SandboxReconciler) UnWatchSandbox(namespace, name string) {
	key := types.NamespacedName{Namespace: namespace, Name: name}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.watchers, key)
}
