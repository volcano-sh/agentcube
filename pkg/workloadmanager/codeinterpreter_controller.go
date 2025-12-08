package workloadmanager

import (
	"context"
	"fmt"
	"reflect"

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
	mgr    ctrl.Manager
}

// NewCodeInterpreterReconciler creates a new CodeInterpreterReconciler.
// The cache will be initialized when SetupWithManager is called.
func NewCodeInterpreterReconciler(client client.Client, scheme *runtime.Scheme) *CodeInterpreterReconciler {
	return &CodeInterpreterReconciler{
		Client: client,
		Scheme: scheme,
	}
}

//+kubebuilder:rbac:groups=runtime.agentcube.volcano.sh,resources=codeinterpreters,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=runtime.agentcube.volcano.sh,resources=codeinterpreters/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=runtime.agentcube.volcano.sh,resources=codeinterpreters/finalizers,verbs=update
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

	// Manage SandboxTemplate and SandboxWarmPool if configured
	if codeInterpreter.Spec.WarmPoolSize != nil && *codeInterpreter.Spec.WarmPoolSize > 0 {
		// Ensure SandboxTemplate exists (required for SandboxWarmPool)
		if err := r.ensureSandboxTemplate(ctx, codeInterpreter); err != nil {
			logger.Error(err, "failed to ensure SandboxTemplate")
			return ctrl.Result{}, err
		}
		// Ensure SandboxWarmPool exists
		if err := r.ensureSandboxWarmPool(ctx, codeInterpreter); err != nil {
			logger.Error(err, "failed to ensure SandboxWarmPool")
			return ctrl.Result{}, err
		}
	} else {
		// Delete SandboxWarmPool if WarmPoolSize is 0 or nil
		if err := r.deleteSandboxWarmPool(ctx, codeInterpreter); err != nil {
			logger.Error(err, "failed to delete SandboxWarmPool")
			return ctrl.Result{}, err
		}
		// Delete SandboxTemplate if WarmPoolSize is 0 or nil
		if err := r.deleteSandboxTemplate(ctx, codeInterpreter); err != nil {
			logger.Error(err, "failed to delete SandboxTemplate")
			return ctrl.Result{}, err
		}
	}

	// Update status with ready condition
	if err := r.updateStatus(ctx, codeInterpreter); err != nil {
		logger.Error(err, "failed to update status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// updateStatus updates the CodeInterpreter status
func (r *CodeInterpreterReconciler) updateStatus(ctx context.Context, ci *runtimev1alpha1.CodeInterpreter) error {
	// Update status
	ci.Status.Ready = true

	// Update conditions
	readyCondition := metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionTrue,
		Reason:             "Reconciled",
		Message:            "CodeInterpreter is ready",
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

// ensureSandboxTemplate ensures that a SandboxTemplate exists for this CodeInterpreter
func (r *CodeInterpreterReconciler) ensureSandboxTemplate(ctx context.Context, ci *runtimev1alpha1.CodeInterpreter) error {
	template := ci.Spec.Template
	if template == nil {
		return fmt.Errorf("template is required")
	}

	templateName := ci.Name
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
			if !errors.IsAlreadyExists(err) {
				return fmt.Errorf("failed to create SandboxTemplate: %w", err)
			}
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

	templateName := ci.Name
	warmPoolName := ci.Name
	warmPool := &extensionsv1alpha1.SandboxWarmPool{}
	err := r.Get(ctx, types.NamespacedName{Name: warmPoolName, Namespace: ci.Namespace}, warmPool)

	if errors.IsNotFound(err) {
		// Create new SandboxWarmPool
		warmPool = &extensionsv1alpha1.SandboxWarmPool{
			ObjectMeta: metav1.ObjectMeta{
				Name:      warmPoolName,
				Namespace: ci.Namespace,
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
			if !errors.IsAlreadyExists(err) {
				return fmt.Errorf("failed to create SandboxWarmPool: %w", err)
			}
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
	warmPoolName := ci.Name
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

// deleteSandboxTemplate deletes the SandboxTemplate if it exists
func (r *CodeInterpreterReconciler) deleteSandboxTemplate(ctx context.Context, ci *runtimev1alpha1.CodeInterpreter) error {
	templateName := ci.Name
	sandboxTemplate := &extensionsv1alpha1.SandboxTemplate{}
	err := r.Get(ctx, types.NamespacedName{Name: templateName, Namespace: ci.Namespace}, sandboxTemplate)
	if errors.IsNotFound(err) {
		return nil
	} else if err != nil {
		return fmt.Errorf("failed to get SandboxTemplate: %w", err)
	}

	if err := r.Delete(ctx, sandboxTemplate); err != nil {
		if !errors.IsNotFound(err) {
			return fmt.Errorf("failed to delete SandboxTemplate: %w", err)
		}
	}

	return nil
}

// convertToPodTemplate converts CodeInterpreterSandboxTemplate to sandboxv1alpha1.PodTemplate
func (r *CodeInterpreterReconciler) convertToPodTemplate(template *runtimev1alpha1.CodeInterpreterSandboxTemplate, _ *runtimev1alpha1.CodeInterpreter) sandboxv1alpha1.PodTemplate {
	// Build pod spec
	podSpec := corev1.PodSpec{
		ImagePullSecrets: template.ImagePullSecrets,
		Containers: []corev1.Container{
			{
				Name:            "codeinterpreter",
				Image:           template.Image,
				ImagePullPolicy: template.ImagePullPolicy,
				Command:         template.Command,
				Args:            template.Args,
				Env:             template.Environment,
				Resources:       template.Resources,
				VolumeMounts: []corev1.VolumeMount{
					{
						Name:      "jwt-public-key",
						MountPath: "/etc/picod",
						ReadOnly:  true,
					},
				},
			},
		},
		RuntimeClassName: template.RuntimeClassName,
		Volumes: []corev1.Volume{
			{
				Name: "jwt-public-key",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: "agentcube-jwt-public-key",
					},
				},
			},
		},
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

// GetCodeInterpreter retrieves a CodeInterpreter from the cache by namespace and name.
// The cache uses Kubernetes informer cache which is automatically maintained by controller-runtime
// and stays synchronized with the Kubernetes API server through watch mechanism.
//
// Returns nil if the CodeInterpreter is not found in the cache.
// The returned object is a deep copy to prevent external modifications.
//
// Example usage:
//
//	reconciler := NewCodeInterpreterReconciler(client, scheme)
//	ci := reconciler.GetCodeInterpreter("my-codeinterpreter", "default")
func (r *CodeInterpreterReconciler) GetCodeInterpreter(name, namespace string) *runtimev1alpha1.CodeInterpreter {
	if r.mgr == nil {
		return nil
	}

	ci := &runtimev1alpha1.CodeInterpreter{}
	key := types.NamespacedName{Namespace: namespace, Name: name}
	if err := r.mgr.GetCache().Get(context.Background(), key, ci); err != nil {
		return nil
	}
	return ci.DeepCopy()
}

// SetupWithManager sets up the controller with the Manager.
func (r *CodeInterpreterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.mgr = mgr

	return ctrl.NewControllerManagedBy(mgr).
		For(&runtimev1alpha1.CodeInterpreter{}).
		Complete(r)
}
