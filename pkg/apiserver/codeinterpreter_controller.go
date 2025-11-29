package apiserver

import (
	"context"
	"fmt"
	"reflect"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	runtimev1alpha1 "github.com/volcano-sh/agentcube/pkg/apis/runtime/v1alpha1"
	sandboxv1alpha1 "sigs.k8s.io/agent-sandbox/api/v1alpha1"
	extensionsv1alpha1 "sigs.k8s.io/agent-sandbox/extensions/api/v1alpha1"
)

// CodeInterpreterReconciler reconciles a CodeInterpreter object
type CodeInterpreterReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=runtime.agentcube.io,resources=codeinterpreters,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=runtime.agentcube.io,resources=codeinterpreters/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=runtime.agentcube.io,resources=codeinterpreters/finalizers,verbs=update
//+kubebuilder:rbac:groups=agents.x-k8s.io,resources=sandboxes,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=extensions.agents.x-k8s.io,resources=sandboxtemplates,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=extensions.agents.x-k8s.io,resources=sandboxwarmpools,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=extensions.agents.x-k8s.io,resources=sandboxwarmpools/status,verbs=get;update;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *CodeInterpreterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	codeInterpreter := &runtimev1alpha1.CodeInterpreter{}
	if err := r.Get(ctx, req.NamespacedName, codeInterpreter); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Ensure SandboxTemplate exists
	if err := r.ensureSandboxTemplate(ctx, codeInterpreter); err != nil {
		logger.Error(err, "failed to ensure SandboxTemplate")
		return ctrl.Result{}, err
	}

	// Manage SandboxWarmPool if configured
	if codeInterpreter.Spec.WarmPoolSize != nil && *codeInterpreter.Spec.WarmPoolSize > 0 {
		if err := r.ensureSandboxWarmPool(ctx, codeInterpreter); err != nil {
			logger.Error(err, "failed to ensure SandboxWarmPool")
			return ctrl.Result{RequeueAfter: 30 * time.Second}, err
		}
	} else {
		// Delete SandboxWarmPool if WarmPoolSize is 0 or nil
		if err := r.deleteSandboxWarmPool(ctx, codeInterpreter); err != nil {
			logger.Error(err, "failed to delete SandboxWarmPool")
			return ctrl.Result{}, err
		}
	}

	// Update status with ready condition
	if err := r.updateStatus(ctx, codeInterpreter); err != nil {
		logger.Error(err, "failed to update status")
		return ctrl.Result{}, err
	}

	// Requeue periodically to check status
	return ctrl.Result{RequeueAfter: 1 * time.Minute}, nil
}

// updateStatus updates the CodeInterpreter status
func (r *CodeInterpreterReconciler) updateStatus(ctx context.Context, ci *runtimev1alpha1.CodeInterpreter) error {
	// Count active sandboxes for this code interpreter
	activeSessions, err := r.countActiveSandboxes(ctx, ci)
	if err != nil {
		return fmt.Errorf("failed to count active sandboxes: %w", err)
	}

	// Get warm pool ready count from SandboxWarmPool status
	warmPoolReady, err := r.getWarmPoolReadyCount(ctx, ci)
	if err != nil {
		return fmt.Errorf("failed to get warm pool ready count: %w", err)
	}

	// Update status
	ci.Status.ActiveSessions = activeSessions
	ci.Status.WarmPoolReady = warmPoolReady
	ci.Status.Ready = true

	// Update conditions
	readyCondition := metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionTrue,
		Reason:             "Reconciled",
		Message:            fmt.Sprintf("CodeInterpreter is ready with %d active sessions", activeSessions),
		LastTransitionTime: metav1.Now(),
		ObservedGeneration: ci.Generation,
	}

	// Update or add condition
	conditionIndex := -1
	for i, cond := range ci.Status.Conditions {
		if cond.Type == "Ready" {
			conditionIndex = i
			break
		}
	}

	if conditionIndex >= 0 {
		ci.Status.Conditions[conditionIndex] = readyCondition
	} else {
		ci.Status.Conditions = append(ci.Status.Conditions, readyCondition)
	}

	return r.Status().Update(ctx, ci)
}

// countActiveSandboxes counts sandboxes that are using this code interpreter runtime
func (r *CodeInterpreterReconciler) countActiveSandboxes(ctx context.Context, ci *runtimev1alpha1.CodeInterpreter) (int32, error) {
	sandboxList := &sandboxv1alpha1.SandboxList{}
	if err := r.List(ctx, sandboxList, client.InNamespace(ci.Namespace)); err != nil {
		return 0, err
	}

	count := int32(0)
	for _, sandbox := range sandboxList.Items {
		// Check if sandbox is using this code interpreter runtime
		// This is determined by labels or annotations
		if sandbox.Labels != nil {
			if runtimeName, ok := sandbox.Labels["codeinterpreter.runtime.agentcube.io/name"]; ok {
				if runtimeName == ci.Name {
					// Check if sandbox is running by checking Ready condition
					for _, condition := range sandbox.Status.Conditions {
						if condition.Type == string(sandboxv1alpha1.SandboxConditionReady) && condition.Status == metav1.ConditionTrue {
							count++
							break
						}
					}
				}
			}
		}
	}

	return count, nil
}

// getWarmPoolReadyCount gets the ready count from SandboxWarmPool status
func (r *CodeInterpreterReconciler) getWarmPoolReadyCount(ctx context.Context, ci *runtimev1alpha1.CodeInterpreter) (int32, error) {
	if ci.Spec.WarmPoolSize == nil || *ci.Spec.WarmPoolSize == 0 {
		return 0, nil
	}

	warmPoolName := r.getWarmPoolName(ci)
	warmPool := &extensionsv1alpha1.SandboxWarmPool{}
	if err := r.Get(ctx, types.NamespacedName{Name: warmPoolName, Namespace: ci.Namespace}, warmPool); err != nil {
		if errors.IsNotFound(err) {
			return 0, nil
		}
		return 0, err
	}

	return warmPool.Status.Replicas, nil
}

// ensureSandboxTemplate ensures that a SandboxTemplate exists for this CodeInterpreter
func (r *CodeInterpreterReconciler) ensureSandboxTemplate(ctx context.Context, ci *runtimev1alpha1.CodeInterpreter) error {
	template := ci.Spec.Template
	if template == nil {
		return fmt.Errorf("template is required")
	}

	templateName := r.getTemplateName(ci)
	sandboxTemplate := &extensionsv1alpha1.SandboxTemplate{}
	err := r.Get(ctx, types.NamespacedName{Name: templateName, Namespace: ci.Namespace}, sandboxTemplate)

	// Convert CodeInterpreterSandboxTemplate to PodTemplate
	podTemplate := r.convertToPodTemplate(template, ci)

	if errors.IsNotFound(err) {
		// Create new SandboxTemplate
		sandboxTemplate = &extensionsv1alpha1.SandboxTemplate{
			ObjectMeta: metav1.ObjectMeta{
				Name:      templateName,
				Namespace: ci.Namespace,
				Labels: map[string]string{
					"codeinterpreter.runtime.agentcube.io/name": ci.Name,
				},
			},
			Spec: extensionsv1alpha1.SandboxTemplateSpec{
				PodTemplate: podTemplate,
			},
		}

		// Set owner reference
		if err := controllerutil.SetControllerReference(ci, sandboxTemplate, r.Scheme); err != nil {
			return fmt.Errorf("failed to set controller reference: %w", err)
		}

		if err := r.Create(ctx, sandboxTemplate); err != nil {
			return fmt.Errorf("failed to create SandboxTemplate: %w", err)
		}
		return nil
	} else if err != nil {
		return fmt.Errorf("failed to get SandboxTemplate: %w", err)
	}

	// Update existing SandboxTemplate if needed
	if !r.podTemplateEqual(sandboxTemplate.Spec.PodTemplate, podTemplate) {
		sandboxTemplate.Spec.PodTemplate = podTemplate
		if err := r.Update(ctx, sandboxTemplate); err != nil {
			return fmt.Errorf("failed to update SandboxTemplate: %w", err)
		}
	}

	return nil
}

// ensureSandboxWarmPool ensures that a SandboxWarmPool exists for this CodeInterpreter
func (r *CodeInterpreterReconciler) ensureSandboxWarmPool(ctx context.Context, ci *runtimev1alpha1.CodeInterpreter) error {
	if ci.Spec.WarmPoolSize == nil || *ci.Spec.WarmPoolSize == 0 {
		return nil
	}

	templateName := r.getTemplateName(ci)
	warmPoolName := r.getWarmPoolName(ci)
	warmPool := &extensionsv1alpha1.SandboxWarmPool{}
	err := r.Get(ctx, types.NamespacedName{Name: warmPoolName, Namespace: ci.Namespace}, warmPool)

	if errors.IsNotFound(err) {
		// Create new SandboxWarmPool
		warmPool = &extensionsv1alpha1.SandboxWarmPool{
			ObjectMeta: metav1.ObjectMeta{
				Name:      warmPoolName,
				Namespace: ci.Namespace,
				Labels: map[string]string{
					"codeinterpreter.runtime.agentcube.io/name": ci.Name,
				},
			},
			Spec: extensionsv1alpha1.SandboxWarmPoolSpec{
				Replicas: *ci.Spec.WarmPoolSize,
				TemplateRef: extensionsv1alpha1.SandboxTemplateRef{
					Name: templateName,
				},
			},
		}

		// Set owner reference
		if err := controllerutil.SetControllerReference(ci, warmPool, r.Scheme); err != nil {
			return fmt.Errorf("failed to set controller reference: %w", err)
		}

		if err := r.Create(ctx, warmPool); err != nil {
			return fmt.Errorf("failed to create SandboxWarmPool: %w", err)
		}
		return nil
	} else if err != nil {
		return fmt.Errorf("failed to get SandboxWarmPool: %w", err)
	}

	// Update existing SandboxWarmPool if needed
	needsUpdate := false
	if warmPool.Spec.Replicas != *ci.Spec.WarmPoolSize {
		warmPool.Spec.Replicas = *ci.Spec.WarmPoolSize
		needsUpdate = true
	}
	if warmPool.Spec.TemplateRef.Name != templateName {
		warmPool.Spec.TemplateRef.Name = templateName
		needsUpdate = true
	}

	if needsUpdate {
		if err := r.Update(ctx, warmPool); err != nil {
			return fmt.Errorf("failed to update SandboxWarmPool: %w", err)
		}
	}

	return nil
}

// deleteSandboxWarmPool deletes the SandboxWarmPool if it exists
func (r *CodeInterpreterReconciler) deleteSandboxWarmPool(ctx context.Context, ci *runtimev1alpha1.CodeInterpreter) error {
	warmPoolName := r.getWarmPoolName(ci)
	warmPool := &extensionsv1alpha1.SandboxWarmPool{}
	err := r.Get(ctx, types.NamespacedName{Name: warmPoolName, Namespace: ci.Namespace}, warmPool)
	if errors.IsNotFound(err) {
		return nil
	} else if err != nil {
		return fmt.Errorf("failed to get SandboxWarmPool: %w", err)
	}

	if err := r.Delete(ctx, warmPool); err != nil {
		if !errors.IsNotFound(err) {
			return fmt.Errorf("failed to delete SandboxWarmPool: %w", err)
		}
	}

	return nil
}

// getTemplateName returns the name for the SandboxTemplate
func (r *CodeInterpreterReconciler) getTemplateName(ci *runtimev1alpha1.CodeInterpreter) string {
	return fmt.Sprintf("codeinterpreter-%s", ci.Name)
}

// getWarmPoolName returns the name for the SandboxWarmPool
func (r *CodeInterpreterReconciler) getWarmPoolName(ci *runtimev1alpha1.CodeInterpreter) string {
	return fmt.Sprintf("codeinterpreter-%s-warmpool", ci.Name)
}

// convertToPodTemplate converts CodeInterpreterSandboxTemplate to sandboxv1alpha1.PodTemplate
func (r *CodeInterpreterReconciler) convertToPodTemplate(template *runtimev1alpha1.CodeInterpreterSandboxTemplate, ci *runtimev1alpha1.CodeInterpreter) sandboxv1alpha1.PodTemplate {
	// Build pod spec
	podSpec := corev1.PodSpec{
		Containers: []corev1.Container{
			{
				Name:      "codeinterpreter",
				Image:     template.Image,
				Command:   template.Command,
				Args:      template.Args,
				Env:       template.Environment,
				Resources: template.Resources,
			},
		},
		RuntimeClassName: template.RuntimeClassName,
	}

	return sandboxv1alpha1.PodTemplate{
		Spec: podSpec,
	}
}

// podTemplateEqual checks if two PodTemplates are equal
func (r *CodeInterpreterReconciler) podTemplateEqual(a, b sandboxv1alpha1.PodTemplate) bool {
	// Use reflect.DeepEqual for a comprehensive comparison.
	return reflect.DeepEqual(a.Spec, b.Spec)
}

// SetupWithManager sets up the controller with the Manager.
func (r *CodeInterpreterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&runtimev1alpha1.CodeInterpreter{}).
		Complete(r)
}
