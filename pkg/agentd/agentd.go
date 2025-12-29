package agentd

import (
	"context"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	sandboxv1alpha1 "sigs.k8s.io/agent-sandbox/api/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/volcano-sh/agentcube/pkg/workloadmanager"
)

var (
	SessionExpirationTimeout = 15 * time.Minute
)

// Reconciler reconciles a Sandbox object
type Reconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	sandbox := &sandboxv1alpha1.Sandbox{}
	err := r.Get(ctx, req.NamespacedName, sandbox)
	if err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !workloadmanager.IsSandboxReady(sandbox) {
		return ctrl.Result{}, nil
	}

	lastActivityStr, exists := sandbox.Annotations[workloadmanager.LastActivityAnnotationKey]
	var lastActivity time.Time
	if exists && lastActivityStr != "" {
		lastActivity, err = time.Parse(time.RFC3339, lastActivityStr)
		if err != nil {
			return ctrl.Result{RequeueAfter: 30 * time.Second}, err
		}

		expirationTime := lastActivity.Add(SessionExpirationTimeout)
		// Delete sandbox if expired
		if time.Now().After(expirationTime) {
			if err := r.Delete(ctx, sandbox); err != nil {
				if !errors.IsNotFound(err) {
					return ctrl.Result{}, err
				}
			}
		} else {
			return ctrl.Result{RequeueAfter: time.Until(expirationTime)}, nil
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&sandboxv1alpha1.Sandbox{}).
		Complete(r)
}
