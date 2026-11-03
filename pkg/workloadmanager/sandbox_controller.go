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
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	sandboxv1alpha1 "sigs.k8s.io/agent-sandbox/api/v1alpha1"
	extensionsv1alpha1 "sigs.k8s.io/agent-sandbox/extensions/api/v1alpha1"

	commontypes "github.com/volcano-sh/agentcube/pkg/common/types"
	"github.com/volcano-sh/agentcube/pkg/store"
)

// sessionCleanupTimeout bounds the store calls made when a Sandbox or
// SandboxClaim is deleted.
const sessionCleanupTimeout = 5 * time.Second

type SandboxReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// Store is used to remove a session when its Sandbox or SandboxClaim is
	// deleted. Cleanup is skipped when it is nil.
	Store store.Store

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

	if getSandboxStatus(sandbox) != sandboxStatusReady {
		return ctrl.Result{}, nil
	}

	klog.V(2).Infof("Sandbox %s/%s is ready, notifying waiter", sandbox.Namespace, sandbox.Name)

	r.mu.Lock()
	resultChan, exists := r.watchers[req.NamespacedName]
	if exists {
		delete(r.watchers, req.NamespacedName)
	}
	r.mu.Unlock()

	if exists {
		// WatchSandboxOnce always creates a buffered channel of size 1, and the
		// map entry is deleted before this point so only one sender can ever
		// reach here for a given key. The buffer is therefore always empty and
		// this send never blocks. The default branch guards against the unlikely
		// case where the receiver has already gone away (e.g. context canceled).
		select {
		case resultChan <- SandboxStatusUpdate{Sandbox: sandbox}:
			klog.V(2).Infof("Notified waiter about sandbox %s/%s", sandbox.Namespace, sandbox.Name)
		default:
			klog.V(2).Infof("No receiver for sandbox %s/%s notification, skipping", sandbox.Namespace, sandbox.Name)
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

// SessionCleanupHandler returns an event handler that removes a session from
// the store when its Sandbox or SandboxClaim is deleted. GC and the session
// DELETE API already remove the entry themselves; this covers deletions made
// by anything else, such as kubectl or a CodeInterpreter deletion cascading
// to its SandboxClaims. Without it the Router keeps proxying to the old pod
// instead of returning SESSION_NOT_FOUND.
func (r *SandboxReconciler) SessionCleanupHandler() handler.EventHandler {
	return handler.Funcs{
		DeleteFunc: func(ctx context.Context, e event.DeleteEvent, _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			r.cleanupSession(ctx, e.Object)
		},
	}
}

func (r *SandboxReconciler) cleanupSession(ctx context.Context, obj client.Object) {
	if r.Store == nil || obj == nil {
		return
	}

	var kind string
	switch obj.(type) {
	case *sandboxv1alpha1.Sandbox:
		kind = commontypes.SandboxKind
	case *extensionsv1alpha1.SandboxClaim:
		kind = commontypes.SandboxClaimsKind
	default:
		return
	}

	sessionID := obj.GetLabels()[SessionIdLabelKey]
	if sessionID == "" {
		return
	}

	ctx, cancel := context.WithTimeout(ctx, sessionCleanupTimeout)
	defer cancel()

	info, err := r.Store.GetSandboxBySessionID(ctx, sessionID)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			klog.Warningf("Failed to get session %s for deleted %s %s/%s: %v", sessionID, kind, obj.GetNamespace(), obj.GetName(), err)
		}
		return
	}
	// Only remove the entry if it still points at the deleted object, so an
	// object that carries the same label cannot remove a session stored
	// under a different Sandbox or SandboxClaim.
	if info == nil || info.Kind != kind || info.SandboxNamespace != obj.GetNamespace() || info.Name != obj.GetName() {
		return
	}

	if err := r.Store.DeleteSandboxBySessionID(ctx, sessionID); err != nil {
		klog.Warningf("Failed to delete session %s for deleted %s %s/%s: %v", sessionID, kind, obj.GetNamespace(), obj.GetName(), err)
		return
	}
	klog.Infof("%s %s/%s was deleted, removed session %s from store", kind, obj.GetNamespace(), obj.GetName(), sessionID)
}
