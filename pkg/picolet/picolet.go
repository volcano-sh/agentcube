package picolet

import (
	"context"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	sandboxv1alpha1 "sigs.k8s.io/agent-sandbox/api/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	picoapiserver "github.com/agent-box/pico-apiserver/pkg/pico-apiserver"
)

var (
	SessionExpirationTimeout = 15 * time.Minute
)

// PicoletReconciler reconciles a Sandbox object
type PicoletReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

func (r *PicoletReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	sandbox := &sandboxv1alpha1.Sandbox{}
	err := r.Get(ctx, req.NamespacedName, sandbox)
	if err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	lastActivityStr, exists := sandbox.Annotations[picoapiserver.LastActivityAnnotationKey]
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
			return ctrl.Result{RequeueAfter: expirationTime.Sub(time.Now())}, nil
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *PicoletReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&sandboxv1alpha1.Sandbox{}).
		Complete(r)
}
