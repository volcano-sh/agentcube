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
}

func (r *SandboxReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	sandbox := &sandboxv1alpha1.Sandbox{}
	if err := r.Get(ctx, req.NamespacedName, sandbox); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	status := getSandboxStatus(sandbox)

	// Check for pending requests with proper locking
	if status == "running" {
		klog.V(2).Infof("Sandbox %s/%s is running, sending notification", sandbox.Namespace, sandbox.Name)
		r.mu.Lock()
		resultChan, exists := r.watchers[req.NamespacedName]
		if exists {
			klog.V(2).Infof("Found %d pending requests for sandbox %s/%s", len(r.watchers), sandbox.Namespace, sandbox.Name)
			// Remove from map before sending to avoid memory leak
			delete(r.watchers, req.NamespacedName)
		} else {
			klog.V(2).Infof("No pending requests found for sandbox %s/%s", sandbox.Namespace, sandbox.Name)
		}
		r.mu.Unlock()

		if exists {
			// Send notification outside the lock to avoid deadlock
			select {
			case resultChan <- SandboxStatusUpdate{Sandbox: sandbox}:
				klog.V(2).Infof("Notified waiter about sandbox %s/%s reaching Running state", sandbox.Namespace, sandbox.Name)
			default:
				klog.Warningf("Failed to notify watcher for sandbox %s/%s: channel buffer full or not receiving", sandbox.Namespace, sandbox.Name)
			}
		}
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
