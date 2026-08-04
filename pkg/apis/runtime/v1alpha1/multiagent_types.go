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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// MultiAgentRuntime defines a group of collaborating AgentRuntime roles with
// unified lifecycle management.
//
// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:printcolumn:name="Ready",type="boolean",JSONPath=".status.ready"
// +kubebuilder:printcolumn:name="Policy",type="string",JSONPath=".spec.startupPolicy"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
type MultiAgentRuntime struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              MultiAgentRuntimeSpec   `json:"spec"`
	Status            MultiAgentRuntimeStatus `json:"status,omitempty"`
}

type MultiAgentRuntimeSpec struct {
	// StartupPolicy controls failure behavior during group creation.
	// +kubebuilder:default="Atomic"
	// +kubebuilder:validation:Enum=Atomic;BestEffort
	StartupPolicy StartupPolicyType `json:"startupPolicy,omitempty"`

	// Roles defines the set of agent roles in this group.
	// At least one role must be present, and exactly one must have IsCoordinator=true.
	// +kubebuilder:validation:MinItems=1
	Roles []RoleSpec `json:"roles"`

	// SessionTimeout is the idle timeout applied to all sandboxes in the group.
	// Defaults to 15m.
	// +kubebuilder:default="15m"
	SessionTimeout *metav1.Duration `json:"sessionTimeout,omitempty"`

	// MaxSessionDuration is the absolute TTL for all sandboxes in the group.
	// Defaults to 8h.
	// +kubebuilder:default="8h"
	MaxSessionDuration *metav1.Duration `json:"maxSessionDuration,omitempty"`
}

type RoleSpec struct {
	// Name is the unique identifier for this role within the group.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Kind specifies the type of the referenced runtime.
	// Defaults to "AgentRuntime". Set to "CodeInterpreter" to reference a CodeInterpreter CRD.
	// +optional
	// +kubebuilder:default="AgentRuntime"
	// +kubebuilder:validation:Enum=AgentRuntime;CodeInterpreter
	Kind string `json:"kind,omitempty"`

	// RuntimeRef is the name of an existing AgentRuntime or CodeInterpreter CRD in the same namespace.
	// +kubebuilder:validation:MinLength=1
	RuntimeRef string `json:"runtimeRef"`

	// IsCoordinator marks this role as the external entrypoint for the group.
	// Exactly one role must be marked as coordinator.
	// +optional
	IsCoordinator bool `json:"isCoordinator,omitempty"`

	// WarmPoolSize specifies the number of pre-warmed sandboxes for this role.
	// +optional
	// +kubebuilder:validation:Minimum=0
	WarmPoolSize *int32 `json:"warmPoolSize,omitempty"`

	// Dependencies lists the names of roles that must be ready before this role is created.
	// Circular dependencies are rejected at request time.
	// +optional
	Dependencies []string `json:"dependencies,omitempty"`

	// TargetPort specifies the name or number of the port in the referenced AgentRuntime
	// or CodeInterpreter to be used by dependent roles. If empty, the default Port Resolution Rule applies.
	// +optional
	TargetPort string `json:"targetPort,omitempty"`
}

type StartupPolicyType string

const (
	// StartupPolicyAtomic rolls back all created sandboxes if any role fails.
	StartupPolicyAtomic StartupPolicyType = "Atomic"
	// StartupPolicyBestEffort allows worker failures; coordinator failure still rolls back everything.
	StartupPolicyBestEffort StartupPolicyType = "BestEffort"
)

type MultiAgentRuntimeStatus struct {
	// Conditions reflect the current state of the MultiAgentRuntime.
	// Standard conditions: Ready, Degraded, Failed.
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Ready is true when all required roles are running and healthy.
	Ready bool `json:"ready,omitempty"`

	// RoleStatuses tracks per-role operational state.
	// +optional
	RoleStatuses []RoleStatusEntry `json:"roleStatuses,omitempty"`
}

type RoleStatusEntry struct {
	// Name is the role name matching RoleSpec.Name.
	Name string `json:"name"`
	// Status is the current operational state: "Ready", "Failed", "Replacing".
	Status string `json:"status"`
	// SessionID is the sandbox session ID for this role, if available.
	SessionID string `json:"sessionId,omitempty"`
}

// MultiAgentRuntimeList contains a list of MultiAgentRuntime
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
type MultiAgentRuntimeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MultiAgentRuntime `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MultiAgentRuntime{}, &MultiAgentRuntimeList{})
}
