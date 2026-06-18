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

package agentd

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	runtimev1alpha1 "github.com/volcano-sh/agentcube/pkg/apis/runtime/v1alpha1"
)

// SnapshotTaskReconciler watches SandboxSnapshotTask objects assigned to this node
// and drives snapshot creation via the registered SnapshotDriver.
// Mode-specific target validation is delegated to the registered SnapshotModeTaskHandler.
type SnapshotTaskReconciler struct {
	client.Client
	Scheme       *runtime.Scheme
	NodeName     string
	Drivers      map[string]SnapshotDriver
	ModeHandlers map[runtimev1alpha1.SandboxSnapshotMode]SnapshotModeTaskHandler
}

func (r *SnapshotTaskReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	task := &runtimev1alpha1.SandboxSnapshotTask{}
	if err := r.Get(ctx, req.NamespacedName, task); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Only handle tasks targeting this node.
	if task.Spec.TargetNodeName != r.NodeName {
		return ctrl.Result{}, nil
	}

	// Skip tasks that have already reached a terminal phase.
	if task.Status.Phase == runtimev1alpha1.SnapshotArtifactPhaseReady ||
		task.Status.Phase == runtimev1alpha1.SnapshotArtifactPhaseFailed {
		return ctrl.Result{}, nil
	}

	if done, err := r.validateSnapshotOwner(ctx, task); done || err != nil {
		return ctrl.Result{}, err
	}

	// Select driver.
	driver, ok := r.Drivers[task.Spec.ProviderName]
	if !ok {
		return r.reportFailed(ctx, task, fmt.Sprintf("no driver registered for provider %q", task.Spec.ProviderName))
	}

	// Validate driver capabilities.
	caps := driver.Capabilities(ctx)
	if !containsSnapshotMode(caps.SnapshotModes, task.Spec.SnapshotMode) {
		return r.reportFailed(ctx, task, fmt.Sprintf("driver does not support snapshot mode %q", task.Spec.SnapshotMode))
	}

	if result, done, err := r.validateTargetSandbox(ctx, task); done || err != nil {
		return result, err
	}

	logger.Info("calling snapshot driver", "task", task.Name, "provider", task.Spec.ProviderName)

	artifact, err := driver.Create(ctx, SnapshotDriverCreateRequest{
		TaskRef: corev1.ObjectReference{
			APIVersion: runtimev1alpha1.GroupVersion.String(),
			Kind:       "SandboxSnapshotTask",
			Namespace:  task.Namespace,
			Name:       task.Name,
			UID:        task.UID,
		},
		TargetSandboxRef: task.Spec.TargetSandboxRef,
		TargetNodeName:   task.Spec.TargetNodeName,
		SnapshotMode:     task.Spec.SnapshotMode,
		ProviderName:     task.Spec.ProviderName,
		SnapshotKey:      task.Spec.SnapshotKey,
		SnapshotHash:     task.Spec.SnapshotHash,
	})
	if err != nil {
		logger.Error(err, "snapshot driver create failed", "task", task.Name)
		return r.reportFailed(ctx, task, err.Error())
	}

	logger.Info("snapshot driver create succeeded", "task", task.Name, "snapshotKey", artifact.SnapshotKey)
	return r.reportReady(ctx, task)
}

func (r *SnapshotTaskReconciler) validateSnapshotOwner(ctx context.Context, task *runtimev1alpha1.SandboxSnapshotTask) (bool, error) {
	logger := log.FromContext(ctx)
	snapshot := &runtimev1alpha1.SandboxSnapshot{}
	if err := r.Get(ctx, types.NamespacedName{Name: task.Spec.SnapshotRef.Name, Namespace: task.Namespace}, snapshot); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("owning snapshot not found, skipping task", "task", task.Name)
			return true, nil
		}
		return true, err
	}
	if snapshot.UID != task.Spec.SnapshotUID {
		logger.Info("snapshot UID mismatch, skipping stale task", "task", task.Name)
		return true, nil
	}
	return false, nil
}

func (r *SnapshotTaskReconciler) validateTargetSandbox(ctx context.Context, task *runtimev1alpha1.SandboxSnapshotTask) (ctrl.Result, bool, error) {
	handler, ok := r.ModeHandlers[task.Spec.SnapshotMode]
	if !ok {
		result, err := r.reportFailed(ctx, task, fmt.Sprintf("unsupported snapshot mode %q", task.Spec.SnapshotMode))
		return result, true, err
	}
	return handler.ValidateTarget(ctx, task)
}

func (r *SnapshotTaskReconciler) reportReady(ctx context.Context, task *runtimev1alpha1.SandboxSnapshotTask) (ctrl.Result, error) {
	return ctrl.Result{}, r.patchTaskStatus(ctx, task, runtimev1alpha1.SnapshotArtifactPhaseReady, "")
}

func (r *SnapshotTaskReconciler) reportFailed(ctx context.Context, task *runtimev1alpha1.SandboxSnapshotTask, msg string) (ctrl.Result, error) {
	return ctrl.Result{}, r.patchTaskStatus(ctx, task, runtimev1alpha1.SnapshotArtifactPhaseFailed, msg)
}

func (r *SnapshotTaskReconciler) patchTaskStatus(ctx context.Context, task *runtimev1alpha1.SandboxSnapshotTask, phase runtimev1alpha1.SnapshotArtifactPhase, msg string) error {
	patch := client.MergeFrom(task.DeepCopy())
	now := metav1.Now()
	task.Status.Phase = phase
	task.Status.Message = msg
	task.Status.ObservedAt = &now
	return r.Status().Patch(ctx, task, patch)
}

func containsSnapshotMode(modes []runtimev1alpha1.SandboxSnapshotMode, mode runtimev1alpha1.SandboxSnapshotMode) bool {
	for _, m := range modes {
		if m == mode {
			return true
		}
	}
	return false
}

// SetupWithManager registers the controller and initializes mode handlers.
func (r *SnapshotTaskReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.ModeHandlers = map[runtimev1alpha1.SandboxSnapshotMode]SnapshotModeTaskHandler{
		runtimev1alpha1.SandboxSnapshotModeFork: &ForkModeTaskHandler{Client: r.Client},
	}

	nodeFilter := predicate.NewPredicateFuncs(func(obj client.Object) bool {
		task, ok := obj.(*runtimev1alpha1.SandboxSnapshotTask)
		if !ok {
			return false
		}
		return task.Spec.TargetNodeName == r.NodeName
	})

	return ctrl.NewControllerManagedBy(mgr).
		For(&runtimev1alpha1.SandboxSnapshotTask{}, builder.WithPredicates(nodeFilter)).
		Complete(r)
}
