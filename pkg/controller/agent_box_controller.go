package controller

import (
	"context"
	"fmt"

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

	StatusNotifier  chan SandboxStatusUpdate
	pendingRequests map[types.NamespacedName]chan SandboxStatusUpdate
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
	resultChan, exists := r.pendingRequests[req.NamespacedName]

	if exists && status == "Running" {
		select {
		case resultChan <- SandboxStatusUpdate{
			Namespace: sandbox.Namespace,
			Name:      sandbox.Name,
			Status:    status,
		}:
			// delete chan to above memory leak.
			// Since it's an unbuffered channel, channel is only deleted once it has been read from the channel.
			delete(r.pendingRequests, req.NamespacedName)
		default:
		}
	}

	if status == "Running" && r.StatusNotifier != nil {
		select {
		case r.StatusNotifier <- SandboxStatusUpdate{
			Namespace: sandbox.Namespace,
			Name:      sandbox.Name,
			Status:    status,
		}:
			fmt.Printf("Notified handler about sandbox %s/%s reaching Running state\n",
				sandbox.Namespace, sandbox.Name)
		default:
			fmt.Printf("Warning: StatusNotifier channel is full, could not notify about sandbox %s/%s\n",
				sandbox.Namespace, sandbox.Name)
		}
	}
	return ctrl.Result{}, nil
}

func (r *SandboxReconciler) WatchSandboxOnce(namespace, name string) <-chan SandboxStatusUpdate {
	resultChan := make(chan SandboxStatusUpdate, 1)

	if r.pendingRequests == nil {
		r.pendingRequests = make(map[types.NamespacedName]chan SandboxStatusUpdate)
	}
	key := types.NamespacedName{Namespace: namespace, Name: name}
	r.pendingRequests[key] = resultChan

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
