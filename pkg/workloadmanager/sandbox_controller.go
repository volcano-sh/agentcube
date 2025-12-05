package workloadmanager

import (
	"context"
	"fmt"
	"log"
	"sync"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
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
		log.Printf("Sandbox %s/%s is running, sending notification\n", sandbox.Namespace, sandbox.Name)
		r.mu.Lock()
		resultChan, exists := r.pendingRequests[req.NamespacedName]
		if exists {
			log.Printf("Found %d pending requests for sandbox %s/%s", len(r.pendingRequests), sandbox.Namespace, sandbox.Name)
			// Remove from map before sending to avoid memory leak
			delete(r.pendingRequests, req.NamespacedName)
		} else {
			log.Printf("No pending requests found for sandbox %s/%s, pendingRequests: %v", sandbox.Namespace, sandbox.Name, r.pendingRequests)
		}
		r.mu.Unlock()

		if exists {
			// Send notification outside the lock to avoid deadlock
			select {
			case resultChan <- SandboxStatusUpdate{Sandbox: sandbox}:
				log.Printf("Notified waiter about sandbox %s/%s reaching Running state", sandbox.Namespace, sandbox.Name)
			default:
				log.Printf("Warning: resultChan is full for sandbox %s/%s", sandbox.Namespace, sandbox.Name)
			}
		}
	}

	return ctrl.Result{}, nil
}

func (r *SandboxReconciler) WatchSandboxOnce(ctx context.Context, namespace, name string) <-chan SandboxStatusUpdate {
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

	return resultChan
}
