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
	"net"
	"strconv"
	"time"

	"github.com/volcano-sh/agentcube/pkg/common/types"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	sandboxv1alpha1 "sigs.k8s.io/agent-sandbox/api/v1alpha1"
)

func buildSandboxPlaceHolder(sandboxCR *sandboxv1alpha1.Sandbox, entry *sandboxEntry) *types.SandboxInfo {
	return &types.SandboxInfo{
		Kind:             entry.Kind,
		SessionID:        entry.SessionID,
		SandboxNamespace: sandboxCR.GetNamespace(),
		Name:             sandboxCR.GetName(),
		ExpiresAt:        time.Now().Add(DefaultSandboxTTL),
		Status:           "creating",
	}
}

func buildSandboxInfo(sandbox *sandboxv1alpha1.Sandbox, podIP string, entry *sandboxEntry) *types.SandboxInfo {
	createdAt := sandbox.GetCreationTimestamp().Time
	expiresAt := createdAt.Add(DefaultSandboxTTL)
	if sandbox.Spec.Lifecycle.ShutdownTime != nil {
		expiresAt = sandbox.Spec.Lifecycle.ShutdownTime.Time
	}
	accesses := make([]types.SandboxEntryPoint, 0, len(entry.Ports))
	for _, port := range entry.Ports {
		accesses = append(accesses, types.SandboxEntryPoint{
			Path:     port.PathPrefix,
			Protocol: string(port.Protocol),
			Endpoint: net.JoinHostPort(podIP, strconv.Itoa(int(port.Port))),
		})
	}
	return &types.SandboxInfo{
		Kind:             entry.Kind,
		SandboxID:        string(sandbox.GetUID()),
		Name:             sandbox.GetName(),
		SandboxNamespace: sandbox.GetNamespace(),
		EntryPoints:      accesses,
		SessionID:        entry.SessionID,
		CreatedAt:        createdAt,
		ExpiresAt:        expiresAt,
		Status:           getSandboxStatus(sandbox),
	}
}

// getSandboxStatus extracts status from Sandbox CRD conditions
func getSandboxStatus(sandbox *sandboxv1alpha1.Sandbox) string {
	// Check conditions for Ready status
	for _, condition := range sandbox.Status.Conditions {
		if condition.Type == string(sandboxv1alpha1.SandboxConditionReady) && condition.Status == metav1.ConditionTrue {
			return "running"
		}
	}
	return "unknown"
}
