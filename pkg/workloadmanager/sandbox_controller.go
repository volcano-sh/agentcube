package workloadmanager

import (
	"context"
	"fmt"
	"sync"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
		// If resource is deleted (NotFound), clean up any pending requests to prevent memory leak
		if apierrors.IsNotFound(err) {
			r.mu.Lock()
			if _, exists := r.pendingRequests[req.NamespacedName]; exists {
				klog.Infof("Cleaning up pending request for deleted sandbox %s/%s", req.Namespace, req.Name)
				delete(r.pendingRequests, req.NamespacedName)
			}
			r.mu.Unlock()
		}
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

func (r *SandboxReconciler) WatchSandboxOnce(_ context.Context, namespace, name string) (<-chan SandboxStatusUpdate, func()) {
	resultChan := make(chan SandboxStatusUpdate, 1)
	key := types.NamespacedName{Namespace: namespace, Name: name}

	// Not running yet, register for future notification
	r.mu.Lock()
	if r.pendingRequests == nil {
		r.pendingRequests = make(map[types.NamespacedName]chan SandboxStatusUpdate)
	}
	r.pendingRequests[key] = resultChan
	fmt.Printf("Registered for future notification for sandbox %s/%s\n", key.Namespace, key.Name)
	r.mu.Unlock()

	// Return cleanup function that removes the entry from pendingRequests
	cleanup := func() {
		r.mu.Lock()
		if _, exists := r.pendingRequests[key]; exists {
			klog.Infof("Cleaning up pending request for sandbox %s/%s", key.Namespace, key.Name)
			delete(r.pendingRequests, key)
		}
		r.mu.Unlock()
	}

	return resultChan, cleanup
}
