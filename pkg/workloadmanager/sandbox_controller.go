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

	pendingRequests map[types.NamespacedName]chan SandboxStatusUpdate
	mu              sync.RWMutex // Protect pendingRequests map
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
		klog.Infof("Sandbox %s/%s is running, sending notification", sandbox.Namespace, sandbox.Name)
		r.mu.Lock()
		resultChan, exists := r.pendingRequests[req.NamespacedName]
		if exists {
			klog.Infof("Found %d pending requests for sandbox %s/%s", len(r.pendingRequests), sandbox.Namespace, sandbox.Name)
			// Remove from map before sending to avoid memory leak
			delete(r.pendingRequests, req.NamespacedName)
		} else {
			klog.Infof("No pending requests found for sandbox %s/%s", sandbox.Namespace, sandbox.Name)
		}
		r.mu.Unlock()

		if exists {
			// Send notification outside the lock to avoid deadlock
			select {
			case resultChan <- SandboxStatusUpdate{Sandbox: sandbox}:
				klog.Infof("Notified waiter about sandbox %s/%s reaching Running state", sandbox.Namespace, sandbox.Name)
			default:
				klog.Warningf("resultChan is full for sandbox %s/%s", sandbox.Namespace, sandbox.Name)
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
	if r.pendingRequests == nil {
		r.pendingRequests = make(map[types.NamespacedName]chan SandboxStatusUpdate)
	}
	r.pendingRequests[key] = resultChan
	klog.Infof("Registered for future notification for sandbox %s/%s", key.Namespace, key.Name)

	return resultChan
}

func (r *SandboxReconciler) UnWatchSandbox(namespace, name string) {
	key := types.NamespacedName{Namespace: namespace, Name: name}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.pendingRequests[key]; exists {
		klog.Infof("Cleaning up pending request for sandbox %s/%s", key.Namespace, key.Name)
		delete(r.pendingRequests, key)
	}
}
