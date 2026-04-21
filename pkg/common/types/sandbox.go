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

package types

import (
	"fmt"
	"time"

	runtimev1alpha1 "github.com/volcano-sh/agentcube/pkg/apis/runtime/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type SandboxInfo struct {
	Kind             string              `json:"kind"`
	SandboxID        string              `json:"sandboxId"`
	SandboxNamespace string              `json:"sandboxNamespace"`
	Name             string              `json:"name"`
	EntryPoints      []SandboxEntryPoint `json:"entryPoints"`
	SessionID        string              `json:"sessionId"`
	CreatedAt        time.Time           `json:"createdAt"`
	ExpiresAt        time.Time           `json:"expiresAt"`
	// IdleTimeout is the per-sandbox idle timeout configured via SessionTimeout on the
	// AgentRuntime or CodeInterpreter spec. It is stored in the JSON blob so the
	// garbage collector can apply it per-sandbox rather than using a global constant.
	// metav1.Duration marshals as a human-readable string (e.g. "15m0s") rather than
	// a raw nanosecond integer, making the persisted JSON unambiguous.
	IdleTimeout metav1.Duration `json:"idleTimeout,omitempty"`
	// LastActivityAt is populated transiently from the store's last-activity sorted set
	// during ListInactiveSandboxes. It is intentionally excluded from JSON serialization.
	LastActivityAt time.Time `json:"-"`
	Status         string    `json:"status"`
}

type SandboxEntryPoint struct {
	Path     string `json:"path"`
	Protocol string `json:"protocol"`
	Endpoint string `json:"endpoint"`
}

type CreateSandboxRequest struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	// NetworkPolicy overrides the NetworkPolicy spec from the template for this session only.
	// When set, it replaces (not merges) the template-level NetworkPolicy entirely.
	// +optional
	NetworkPolicy *runtimev1alpha1.SandboxNetworkPolicy `json:"networkPolicy,omitempty"`
}

type CreateSandboxResponse struct {
	SessionID   string              `json:"sessionId"`
	SandboxID   string              `json:"sandboxId"`
	SandboxName string              `json:"sandboxName"`
	EntryPoints []SandboxEntryPoint `json:"entryPoints"`
}

func (car *CreateSandboxRequest) Validate() error {
	switch car.Kind {
	case AgentRuntimeKind:
	case CodeInterpreterKind:
	default:
		return fmt.Errorf("invalid kind %s", car.Kind)
	}
	if car.Namespace == "" {
		return fmt.Errorf("namespace is required")
	}
	if car.Name == "" {
		return fmt.Errorf("name is required")
	}
	if err := validateNetworkPolicyOverride(car.NetworkPolicy); err != nil {
		return fmt.Errorf("invalid networkPolicy: %w", err)
	}
	return nil
}

// validateNetworkPolicyOverride validates a per-session NetworkPolicy override from
// CreateSandboxRequest. Unlike the CRD field (which has kubebuilder markers), this
// comes in as raw JSON and bypasses API-server admission, so we validate it here.
func validateNetworkPolicyOverride(np *runtimev1alpha1.SandboxNetworkPolicy) error {
	if np == nil {
		return nil
	}
	switch np.Mode {
	case runtimev1alpha1.NetworkPolicyModeNone,
		runtimev1alpha1.NetworkPolicyModeRestricted,
		runtimev1alpha1.NetworkPolicyModeCustom,
		"": // empty is treated as None
	default:
		return fmt.Errorf("unknown mode %q: must be one of None, Restricted, Custom", np.Mode)
	}
	if np.Mode == runtimev1alpha1.NetworkPolicyModeCustom && np.Custom == nil {
		return fmt.Errorf("custom must be set when mode is Custom")
	}
	return nil
}
