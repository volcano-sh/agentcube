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
	"fmt"
	"reflect"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	runtimev1alpha1 "github.com/volcano-sh/agentcube/pkg/apis/runtime/v1alpha1"
	sandboxv1beta1 "sigs.k8s.io/agent-sandbox/api/v1beta1"
	extensionsv1beta1 "sigs.k8s.io/agent-sandbox/extensions/api/v1beta1"
)

const (
	codeInterpreterNetworkPolicyManagement = extensionsv1beta1.NetworkPolicyManagementUnmanaged

	codeInterpreterReadyCondition         = "Ready"
	codeInterpreterWarmPoolCondition      = "WarmPoolAvailable"
	codeInterpreterReadyReason            = "Reconciled"
	codeInterpreterWarmPoolDisabled       = "WarmPoolDisabled"
	codeInterpreterWarmPoolReady          = "WarmPoolReady"
	codeInterpreterWarmPoolBelowWatermark = "WarmPoolBelowWatermark"
	codeInterpreterWarmPoolEmpty          = "WarmPoolEmpty"
	codeInterpreterWarmPoolProgressing    = "WarmPoolProgressing"
	codeInterpreterOwnershipConflict      = "OwnershipConflict"
	codeInterpreterWarmPoolEventAction    = "WarmPoolAvailability"
)

// CodeInterpreterReconciler reconciles a CodeInterpreter object
type CodeInterpreterReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder events.EventRecorder
}

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
		result, err := r.ensureSandboxTemplate(ctx, codeInterpreter)
		if err != nil {
			logger.Error(err, "failed to ensure SandboxTemplate")
			return ctrl.Result{}, err
		}
		if result.RequeueAfter > 0 {
			return result, nil
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

	// Warm-pool degradation does not make the CodeInterpreter unavailable:
	// requests can still use the cold-start path.
	if err := r.updateReconciledStatus(ctx, codeInterpreter); err != nil {
		logger.Error(err, "failed to update status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// updateStatus updates the CodeInterpreter status. It skips the API write
// when the status is already up-to-date to avoid triggering a new watch event
// that would re-enqueue the object unnecessarily.
func (r *CodeInterpreterReconciler) updateStatus(
	ctx context.Context,
	ci *runtimev1alpha1.CodeInterpreter,
	ready bool,
	reason, message string,
	additionalConditions ...metav1.Condition,
) error {
	oldReady := ci.Status.Ready
	oldConditions := append([]metav1.Condition(nil), ci.Status.Conditions...)
	previousWarmPoolCondition := apimeta.FindStatusCondition(oldConditions, codeInterpreterWarmPoolCondition)

	conditionStatus := metav1.ConditionFalse
	if ready {
		conditionStatus = metav1.ConditionTrue
	}

	ci.Status.Ready = ready
	// SetStatusCondition only updates LastTransitionTime when the condition
	// Status actually changes, preventing spurious status writes that would
	// trigger an infinite reconciliation loop.
	apimeta.SetStatusCondition(&ci.Status.Conditions, metav1.Condition{
		Type:               codeInterpreterReadyCondition,
		Status:             conditionStatus,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: ci.Generation,
	})
	for _, condition := range additionalConditions {
		apimeta.SetStatusCondition(&ci.Status.Conditions, condition)
	}

	if oldReady == ci.Status.Ready && reflect.DeepEqual(oldConditions, ci.Status.Conditions) {
		return nil
	}

	if err := r.Status().Update(ctx, ci); err != nil {
		return err
	}
	currentWarmPoolCondition := apimeta.FindStatusCondition(ci.Status.Conditions, codeInterpreterWarmPoolCondition)
	if r.Recorder != nil && currentWarmPoolCondition != nil && r.shouldRecordWarmPoolWarningEvent(previousWarmPoolCondition, *currentWarmPoolCondition) {
		r.Recorder.Eventf(
			ci,
			nil,
			corev1.EventTypeWarning,
			currentWarmPoolCondition.Reason,
			codeInterpreterWarmPoolEventAction,
			"%s",
			currentWarmPoolCondition.Message,
		)
	}
	return nil
}

func (r *CodeInterpreterReconciler) updateReconciledStatus(ctx context.Context, ci *runtimev1alpha1.CodeInterpreter) error {
	warmPoolCondition, err := r.warmPoolAvailableCondition(ctx, ci)
	if err != nil {
		return err
	}
	if warmPoolCondition.Reason == codeInterpreterOwnershipConflict {
		if err := r.updateStatus(
			ctx,
			ci,
			false,
			codeInterpreterOwnershipConflict,
			warmPoolCondition.Message,
			warmPoolCondition,
		); err != nil {
			return err
		}
		return fmt.Errorf("warm pool ownership conflict: %s", warmPoolCondition.Message)
	}
	return r.updateStatus(
		ctx,
		ci,
		true,
		codeInterpreterReadyReason,
		"CodeInterpreter is ready",
		warmPoolCondition,
	)
}

func (r *CodeInterpreterReconciler) warmPoolAvailableCondition(ctx context.Context, ci *runtimev1alpha1.CodeInterpreter) (metav1.Condition, error) {
	desired := ptr.Deref(ci.Spec.WarmPoolSize, int32(0))
	if desired <= 0 {
		return metav1.Condition{
			Type:               codeInterpreterWarmPoolCondition,
			Status:             metav1.ConditionUnknown,
			Reason:             codeInterpreterWarmPoolDisabled,
			Message:            "Warm pool is not configured",
			ObservedGeneration: ci.Generation,
		}, nil
	}

	warmPool := &extensionsv1beta1.SandboxWarmPool{}
	err := r.Get(ctx, types.NamespacedName{Name: ci.Name, Namespace: ci.Namespace}, warmPool)
	if errors.IsNotFound(err) {
		return metav1.Condition{
			Type:               codeInterpreterWarmPoolCondition,
			Status:             metav1.ConditionUnknown,
			Reason:             codeInterpreterWarmPoolProgressing,
			Message:            fmt.Sprintf("SandboxWarmPool %s/%s has not been observed yet", ci.Namespace, ci.Name),
			ObservedGeneration: ci.Generation,
		}, nil
	}
	if err != nil {
		return metav1.Condition{}, fmt.Errorf("failed to get SandboxWarmPool for status: %w", err)
	}
	if !metav1.IsControlledBy(warmPool, ci) {
		return metav1.Condition{
			Type:               codeInterpreterWarmPoolCondition,
			Status:             metav1.ConditionUnknown,
			Reason:             codeInterpreterOwnershipConflict,
			Message:            fmt.Sprintf("SandboxWarmPool %s/%s is not controlled by CodeInterpreter %s", ci.Namespace, ci.Name, ci.Name),
			ObservedGeneration: ci.Generation,
		}, nil
	}

	ready := warmPool.Status.ReadyReplicas
	// Compute ceil(desired/2) without overflowing at math.MaxInt32.
	lowWatermark := desired/2 + desired%2
	if ready == 0 {
		return metav1.Condition{
			Type:               codeInterpreterWarmPoolCondition,
			Status:             metav1.ConditionFalse,
			Reason:             codeInterpreterWarmPoolEmpty,
			Message:            fmt.Sprintf("SandboxWarmPool has 0 ready replicas out of %d desired", desired),
			ObservedGeneration: ci.Generation,
		}, nil
	}
	if ready < lowWatermark {
		return metav1.Condition{
			Type:               codeInterpreterWarmPoolCondition,
			Status:             metav1.ConditionFalse,
			Reason:             codeInterpreterWarmPoolBelowWatermark,
			Message:            fmt.Sprintf("SandboxWarmPool has %d ready replicas out of %d desired, below low watermark %d", ready, desired, lowWatermark),
			ObservedGeneration: ci.Generation,
		}, nil
	}

	return metav1.Condition{
		Type:               codeInterpreterWarmPoolCondition,
		Status:             metav1.ConditionTrue,
		Reason:             codeInterpreterWarmPoolReady,
		Message:            fmt.Sprintf("SandboxWarmPool has %d ready replicas out of %d desired", ready, desired),
		ObservedGeneration: ci.Generation,
	}, nil
}

func (r *CodeInterpreterReconciler) shouldRecordWarmPoolWarningEvent(previous *metav1.Condition, current metav1.Condition) bool {
	if current.Type != codeInterpreterWarmPoolCondition || current.Status != metav1.ConditionFalse {
		return false
	}
	if current.Reason != codeInterpreterWarmPoolEmpty && current.Reason != codeInterpreterWarmPoolBelowWatermark {
		return false
	}
	return previous == nil || previous.Status != current.Status || previous.Reason != current.Reason
}

func (r *CodeInterpreterReconciler) validateChildOwnership(ctx context.Context, ci *runtimev1alpha1.CodeInterpreter, child metav1.Object, kind string) error {
	if metav1.IsControlledBy(child, ci) {
		return nil
	}

	ownershipErr := fmt.Errorf("existing %s %s/%s is not controlled by CodeInterpreter %s", kind, child.GetNamespace(), child.GetName(), ci.Name)
	if err := r.updateStatus(ctx, ci, false, codeInterpreterOwnershipConflict, ownershipErr.Error(), metav1.Condition{
		Type:               codeInterpreterWarmPoolCondition,
		Status:             metav1.ConditionUnknown,
		Reason:             codeInterpreterOwnershipConflict,
		Message:            ownershipErr.Error(),
		ObservedGeneration: ci.Generation,
	}); err != nil {
		return fmt.Errorf("%v: failed to update CodeInterpreter status: %w", ownershipErr, err)
	}
	return ownershipErr
}

// ensureSandboxTemplate ensures that a SandboxTemplate exists for this CodeInterpreter
func (r *CodeInterpreterReconciler) ensureSandboxTemplate(ctx context.Context, ci *runtimev1alpha1.CodeInterpreter) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Check if public key is cached before creating SandboxTemplate that requires it
	// Skip this check if authMode is "none" (custom images that don't use PicoD auth)
	if ci.Spec.AuthMode != runtimev1alpha1.AuthModeNone && !IsPublicKeyCached() {
		logger.Info("waiting for public key to be cached from Router Secret; ensure Router has started and created the identity Secret")
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	template := ci.Spec.Template
	if template == nil {
		return ctrl.Result{}, fmt.Errorf("template is required")
	}

	templateName := ci.Name
	sandboxTemplate := &extensionsv1beta1.SandboxTemplate{}
	err := r.Get(ctx, types.NamespacedName{Name: templateName, Namespace: ci.Namespace}, sandboxTemplate)

	// Convert CodeInterpreterSandboxTemplate to PodTemplate
	podTemplate := r.convertToPodTemplate(template, ci)

	if errors.IsNotFound(err) {
		// Create new SandboxTemplate
		sandboxTemplate = &extensionsv1beta1.SandboxTemplate{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "extensions.agents.x-k8s.io/v1beta1",
				Kind:       "SandboxTemplate",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      templateName,
				Namespace: ci.Namespace,
			},
			Spec: extensionsv1beta1.SandboxTemplateSpec{
				SandboxBlueprint: sandboxv1beta1.SandboxBlueprint{
					PodTemplate: podTemplate,
				},
				NetworkPolicyManagement: codeInterpreterNetworkPolicyManagement,
			},
		}

		// Set owner reference
		if err := controllerutil.SetControllerReference(ci, sandboxTemplate, r.Scheme); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to set controller reference: %w", err)
		}

		if err := r.Create(ctx, sandboxTemplate); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to create SandboxTemplate: %w", err)
		}
		return ctrl.Result{}, nil
	} else if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get SandboxTemplate: %w", err)
	}
	if err := r.validateChildOwnership(ctx, ci, sandboxTemplate, "SandboxTemplate"); err != nil {
		return ctrl.Result{}, err
	}

	// Update existing SandboxTemplate if needed.
	needsUpdate := false
	if !r.podTemplateEqual(sandboxTemplate.Spec.PodTemplate, podTemplate) {
		sandboxTemplate.Spec.PodTemplate = podTemplate
		needsUpdate = true
	}
	if sandboxTemplate.Spec.NetworkPolicyManagement != codeInterpreterNetworkPolicyManagement {
		sandboxTemplate.Spec.NetworkPolicyManagement = codeInterpreterNetworkPolicyManagement
		needsUpdate = true
	}
	if needsUpdate {
		if err := r.Update(ctx, sandboxTemplate); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update SandboxTemplate: %w", err)
		}
	}

	return ctrl.Result{}, nil
}

// ensureSandboxWarmPool ensures that a SandboxWarmPool exists for this CodeInterpreter
func (r *CodeInterpreterReconciler) ensureSandboxWarmPool(ctx context.Context, ci *runtimev1alpha1.CodeInterpreter) error {
	if ci.Spec.WarmPoolSize == nil || *ci.Spec.WarmPoolSize == 0 {
		return nil
	}

	templateName := ci.Name
	warmPoolName := ci.Name
	warmPool := &extensionsv1beta1.SandboxWarmPool{}
	err := r.Get(ctx, types.NamespacedName{Name: warmPoolName, Namespace: ci.Namespace}, warmPool)

	if errors.IsNotFound(err) {
		// Create new SandboxWarmPool
		warmPool = &extensionsv1beta1.SandboxWarmPool{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "extensions.agents.x-k8s.io/v1beta1",
				Kind:       "SandboxWarmPool",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      warmPoolName,
				Namespace: ci.Namespace,
			},
			Spec: extensionsv1beta1.SandboxWarmPoolSpec{
				Replicas: ci.Spec.WarmPoolSize,
				TemplateRef: extensionsv1beta1.SandboxTemplateRef{
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
	if err := r.validateChildOwnership(ctx, ci, warmPool, "SandboxWarmPool"); err != nil {
		return err
	}

	// Update existing SandboxWarmPool if needed
	needsUpdate := false

	if !replicasEqual(warmPool.Spec.Replicas, ci.Spec.WarmPoolSize) {
		warmPool.Spec.Replicas = ci.Spec.WarmPoolSize
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
	warmPool := &extensionsv1beta1.SandboxWarmPool{}
	err := r.Get(ctx, types.NamespacedName{Name: warmPoolName, Namespace: ci.Namespace}, warmPool)
	if errors.IsNotFound(err) {
		return nil
	} else if err != nil {
		return fmt.Errorf("failed to get SandboxWarmPool: %w", err)
	}
	if err := r.validateChildOwnership(ctx, ci, warmPool, "SandboxWarmPool"); err != nil {
		return err
	}

	if err := r.Delete(ctx, warmPool, client.Preconditions{
		UID:             &warmPool.UID,
		ResourceVersion: &warmPool.ResourceVersion,
	}); err != nil {
		if !errors.IsNotFound(err) {
			return fmt.Errorf("failed to delete SandboxWarmPool: %w", err)
		}
	}

	return nil
}

// deleteSandboxTemplate deletes the SandboxTemplate if it exists
func (r *CodeInterpreterReconciler) deleteSandboxTemplate(ctx context.Context, ci *runtimev1alpha1.CodeInterpreter) error {
	templateName := ci.Name
	sandboxTemplate := &extensionsv1beta1.SandboxTemplate{}
	err := r.Get(ctx, types.NamespacedName{Name: templateName, Namespace: ci.Namespace}, sandboxTemplate)
	if errors.IsNotFound(err) {
		return nil
	} else if err != nil {
		return fmt.Errorf("failed to get SandboxTemplate: %w", err)
	}
	if err := r.validateChildOwnership(ctx, ci, sandboxTemplate, "SandboxTemplate"); err != nil {
		return err
	}

	if err := r.Delete(ctx, sandboxTemplate, client.Preconditions{
		UID:             &sandboxTemplate.UID,
		ResourceVersion: &sandboxTemplate.ResourceVersion,
	}); err != nil {
		if !errors.IsNotFound(err) {
			return fmt.Errorf("failed to delete SandboxTemplate: %w", err)
		}
	}

	return nil
}

// convertToPodTemplate converts CodeInterpreterSandboxTemplate to sandboxv1beta1.PodTemplate
func (r *CodeInterpreterReconciler) convertToPodTemplate(template *runtimev1alpha1.CodeInterpreterSandboxTemplate, ci *runtimev1alpha1.CodeInterpreter) sandboxv1beta1.PodTemplate {
	// Normalize RuntimeClassName: if it's an empty string, set it to nil
	runtimeClassName := template.RuntimeClassName
	if runtimeClassName != nil && *runtimeClassName == "" {
		runtimeClassName = nil
	}

	// Build environment variables - create a copy to avoid mutating the cached object
	envVars := make([]corev1.EnvVar, len(template.Environment))
	copy(envVars, template.Environment)
	// Only inject public key for picod auth mode (default behavior)
	if ci.Spec.AuthMode != runtimev1alpha1.AuthModeNone {
		envVars = append(envVars, corev1.EnvVar{
			Name:  "PICOD_AUTH_PUBLIC_KEY",
			Value: GetCachedPublicKey(),
		})
	}

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
				Env:             envVars,
				Resources:       template.Resources,
			},
		},
		RuntimeClassName: runtimeClassName,
	}

	return sandboxv1beta1.PodTemplate{
		Spec: podSpec,
	}
}

// podTemplateEqual checks if two PodTemplates are equal
func (r *CodeInterpreterReconciler) podTemplateEqual(a, b sandboxv1beta1.PodTemplate) bool {
	// Use reflect.DeepEqual for a comprehensive comparison.
	return reflect.DeepEqual(a.Spec, b.Spec)
}

// SetupWithManager sets up the controller with the Manager.
// GenerationChangedPredicate filters out status-only update events so that
// the controller is not re-enqueued by its own status writes.
func (r *CodeInterpreterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&runtimev1alpha1.CodeInterpreter{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Owns(&extensionsv1beta1.SandboxWarmPool{}).
		Complete(r)
}

// replicasEqual safely compares two *int32 pointers for equality.
func replicasEqual(a, b *int32) bool {
	if a == nil && b == nil {
		return true
	}
	if a != nil && b != nil && *a == *b {
		return true
	}
	return false
}
