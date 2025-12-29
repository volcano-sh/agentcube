package workloadmanager

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	sandboxv1alpha1 "sigs.k8s.io/agent-sandbox/api/v1alpha1"
)

const sandboxReadyReasonDependenciesReady = "DependenciesReady"

// IsSandboxReady returns true when the sandbox Ready condition is true and dependencies are confirmed healthy.
func IsSandboxReady(sandbox *sandboxv1alpha1.Sandbox) bool {
	if sandbox == nil {
		return false
	}
	return IsSandboxReadyConditionTrue(sandbox.Status)
}

// IsSandboxReadyConditionTrue inspects the Ready condition ensuring the DependenciesReady reason is set.
func IsSandboxReadyConditionTrue(status sandboxv1alpha1.SandboxStatus) bool {
	for _, condition := range status.Conditions {
		if condition.Type != string(sandboxv1alpha1.SandboxConditionReady) {
			continue
		}
		return condition.Status == metav1.ConditionTrue && condition.Reason == sandboxReadyReasonDependenciesReady
	}
	return false
}
