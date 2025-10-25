package controller

import (
	"context"
	"fmt"
	"sync"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	Namespace string
	Name      string
	Status    string
}

func (r *SandboxReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	sandbox := &sandboxv1alpha1.Sandbox{}
	if err := r.Get(ctx, req.NamespacedName, sandbox); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	status := getSandboxStatus(sandbox)

	// Check for pending requests with proper locking
	if status == "Running" {
		fmt.Printf("Sandbox %s/%s is running, sending notification\n", sandbox.Namespace, sandbox.Name)
		r.mu.Lock()
		resultChan, exists := r.pendingRequests[req.NamespacedName]
		if exists {
			fmt.Printf("Found %d pending requests for sandbox %s/%s\n", len(r.pendingRequests), sandbox.Namespace, sandbox.Name)
			// Remove from map before sending to avoid memory leak
			delete(r.pendingRequests, req.NamespacedName)
		} else {
			fmt.Printf("No pending requests found for sandbox %s/%s, pendingRequests: %v\n", sandbox.Namespace, sandbox.Name, r.pendingRequests)
		}
		r.mu.Unlock()

		if exists {
			// Send notification outside the lock to avoid deadlock
			select {
			case resultChan <- SandboxStatusUpdate{
				Namespace: sandbox.Namespace,
				Name:      sandbox.Name,
				Status:    status,
			}:
				fmt.Printf("Notified waiter about sandbox %s/%s reaching Running state\n",
					sandbox.Namespace, sandbox.Name)
			default:
				fmt.Printf("Warning: resultChan is full for sandbox %s/%s\n",
					sandbox.Namespace, sandbox.Name)
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

func (r *SandboxReconciler) HandleSandboxRequest(ctx context.Context, namespace, name string) error {
	sandbox := &sandboxv1alpha1.Sandbox{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, sandbox); err != nil {
		return err
	}

	status := getSandboxStatus(sandbox)

	if status == "Running" {
		return nil
	} else {
		return fmt.Errorf("sandbox %s/%s is not in Running state", namespace, name)
	}
}

func (r *SandboxReconciler) nonRunningSandbox(sandbox *sandboxv1alpha1.Sandbox) {
	fmt.Printf("Processing non-running sandbox %s/%s with status %s\n",
		sandbox.Namespace, sandbox.Name, getSandboxStatus(sandbox))
}

func getSandboxStatus(sandbox *sandboxv1alpha1.Sandbox) string {
	for _, condition := range sandbox.Status.Conditions {
		if condition.Type == string(sandboxv1alpha1.SandboxConditionReady) && condition.Status == metav1.ConditionTrue {
			return "Running"
		}
	}
	return "Unknown"
}
