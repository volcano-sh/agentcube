package picolet

import (
	"context"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	sandboxv1alpha1 "sigs.k8s.io/agent-sandbox/api/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	picoapiserver "github.com/agent-box/pico-apiserver/pkg/pico-apiserver"
)

var (
	SandboxExpirationInterval = 15 * time.Minute
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

	if sandboxIsRunning(sandbox) {
		lastActivityStr, exists := sandbox.Annotations[picoapiserver.LastActivityAnnotationKey]
		var lastActivity time.Time
		if exists && lastActivityStr != "" {
			lastActivity, err = time.Parse(time.RFC3339, lastActivityStr)
			if err != nil {
				return ctrl.Result{RequeueAfter: 30 * time.Second}, err
			}

			expirationTime := lastActivity.Add(SandboxExpirationInterval)
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
		} else {
			sandbox.Annotations[picoapiserver.LastActivityAnnotationKey] = time.Now().Format(time.RFC3339)
			if err := r.Update(ctx, sandbox); err != nil {
				return ctrl.Result{}, err
			}
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

func sandboxIsRunning(sandbox *sandboxv1alpha1.Sandbox) bool {
	for _, condition := range sandbox.Status.Conditions {
		if condition.Type == string(sandboxv1alpha1.SandboxConditionReady) && condition.Status == metav1.ConditionTrue {
			return true
		}
	}
	return false
}
