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
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	sandboxv1alpha1 "sigs.k8s.io/agent-sandbox/api/v1alpha1"

	runtimev1alpha1 "github.com/volcano-sh/agentcube/pkg/apis/runtime/v1alpha1"
)

// SnapshotModeTaskHandler encapsulates mode-specific logic for executing a SandboxSnapshotTask.
// To add support for a new snapshot mode in the node agent, implement this interface
// and register it in SnapshotTaskReconciler.SetupWithManager.
type SnapshotModeTaskHandler interface {
	// ValidateTarget checks whether the target sandbox is in the expected state for snapshotting.
	// Returns (result, done, err): done=true means the reconcile should stop for this cycle.
	ValidateTarget(ctx context.Context, task *runtimev1alpha1.SandboxSnapshotTask) (ctrl.Result, bool, error)
}

// ForkModeTaskHandler implements SnapshotModeTaskHandler for Fork-mode snapshot tasks.
// It waits until the build Sandbox is Running before allowing the snapshot driver to proceed.
type ForkModeTaskHandler struct {
	client.Client
}

func (h *ForkModeTaskHandler) ValidateTarget(ctx context.Context, task *runtimev1alpha1.SandboxSnapshotTask) (ctrl.Result, bool, error) {
	running, err := h.isSandboxRunning(ctx, task.Namespace, task.Spec.TargetSandboxRef.Name)
	if err != nil {
		return ctrl.Result{}, true, err
	}
	if !running {
		return ctrl.Result{RequeueAfter: 5 * time.Second}, true, nil
	}
	return ctrl.Result{}, false, nil
}

func (h *ForkModeTaskHandler) isSandboxRunning(ctx context.Context, namespace, name string) (bool, error) {
	sandbox := &sandboxv1alpha1.Sandbox{}
	if err := h.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, sandbox); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("get sandbox %s/%s: %w", namespace, name, err)
	}
	for _, cond := range sandbox.Status.Conditions {
		if cond.Type == string(sandboxv1alpha1.SandboxConditionReady) && cond.Status == metav1.ConditionTrue {
			return true, nil
		}
	}
	return false, nil
}
